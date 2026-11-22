package architecture

import (
	"go/ast"
	"go/token"
	"slices"
	"strings"
	"testing"
)

// carry (Task 20 review): the parse-only pool-bypass sensor missed a pool laundered through a var spec or an
// interface. Each shape below compiles in the adapter and wrote through the agents' read-only pool in autocommit
// while TestPostgresAdapterNeverBypassesBegin stayed green (reviewed in a scratch clone, task-20-review.txt M1).
func TestPostgresPoolBypassSensorFollowsLaunderedPools(t *testing.T) {
	const execer = "type execer interface{ Exec(context.Context, string, ...any) (any, error) }\n\n" +
		"type querier interface{ Exec(context.Context, string, ...any) (any, error) }\n\n"
	bad := map[string]string{
		"a var spec without a type (m2)": "func (r *Fleets) M2(ctx context.Context) { var db = r.pool; _, _ = db.Exec(ctx, `DELETE FROM robots`) }",
		"the package's own querier interface (m5)": execer +
			"func (r *Fleets) M5(ctx context.Context) { var q querier = r.pool; _, _ = q.Exec(ctx, `DELETE FROM robots`) }",
		"an interface-typed parameter (m3)": execer +
			"func direct(ctx context.Context, q execer) { _, _ = q.Exec(ctx, `DELETE FROM robots`) }\n\n" +
			"func (r *Fleets) M3(ctx context.Context) { direct(ctx, r.pool) }",
	}
	for name, body := range bad {
		if v, _ := poolFixtureViolations(t, poolFixturePrelude+body+"\n"); len(v) == 0 {
			t.Errorf("%s: the sensor let it through:\n%s", name, body)
		}
	}
}

// The shapes the adapter really uses must stay legal: a struct literal field, a pool-typed result, a helper whose
// parameter is the pool, the read-only registry keyed by the pool, a nil check, Close.
func TestPostgresPoolBypassSensorKeepsTheAdaptersOwnShapes(t *testing.T) {
	good := poolFixturePrelude +
		"func NewFleets(pool *pgxpool.Pool) *Fleets { return &Fleets{pool: pool} }\n\n" +
		"func NewReadOnly(pool *pgxpool.Pool) (*pgxpool.Pool, error) { readOnlyPools.Store(pool, struct{}{}); return pool, nil }\n\n" +
		"func isReadOnly(pool *pgxpool.Pool) bool { _, ok := readOnlyPools.Load(pool); return ok }\n\n" +
		"func (r *Fleets) Save(ctx context.Context) error {\n" +
		"\tif r.pool == nil || isReadOnly(r.pool) {\n\t\treturn nil\n\t}\n" +
		"\tvar same = r.pool\n\tcopied := same\n\t_ = begin(ctx, copied)\n\tr.pool.Close()\n\treturn nil\n}\n"
	v, allowed := poolFixtureViolations(t, good)
	if len(v) != 0 || allowed != 1 {
		t.Errorf("legit shapes = %s (allowed %d), want none and 1", strings.Join(v, "\n"), allowed)
	}
}

// carry (Task 20 review): poolFlowViolations refuses every pool value used where it stops being a *pgxpool.Pool: an
// interface-typed var or parameter (`var q querier = r.pool`, `direct(ctx, r.pool)`), &pool, a non-pool result. A
// pool may only be the receiver of a selector (judged by poolBypassViolations), an argument of a package func whose
// parameter at that index is declared *pgxpool.Pool (begin, inTx, exec, query, queryRow, txOptions, isReadOnly),
// the key of the read-only registry (readOnlyPools.Store/Load), the value of a pool field or variable, a pool
// result, or one side of a nil comparison.
func poolFlowViolations(fset *token.FileSet, files []*ast.File, names map[string]bool) []string {
	j := poolJudge{funcs: poolParamFuncs(files), names: names}
	var out []string
	for _, f := range files {
		j.alias = pgxpoolName(f)
		var stack []ast.Node
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			if e, isExpr := n.(ast.Expr); isExpr && len(stack) > 0 && isPoolExpr(e, names) {
				if !j.allowed(e, stack) {
					out = append(out, fset.Position(e.Pos()).String()+": a *pgxpool.Pool value flows where it stops "+
						"being a pool (an interface, a non-pool parameter or var): only begin() and the pool helpers take it")
				}
				return false
			}
			stack = append(stack, n)
			return true
		})
	}
	return out
}

type poolJudge struct {
	funcs map[string]map[int]bool // top-level func -> indexes of its *pgxpool.Pool parameters
	names map[string]bool
	alias string
}

// poolParamFuncs maps every top-level func of the package to the indexes of its *pgxpool.Pool parameters.
func poolParamFuncs(files []*ast.File) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	for _, f := range files {
		alias := pgxpoolName(f)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			out[fn.Name.Name] = poolIndexes(fn.Type.Params, alias)
		}
	}
	return out
}

// poolIndexes lists the positions of a field list (parameters or results) declared *<pgxpool>.Pool.
func poolIndexes(fl *ast.FieldList, alias string) map[int]bool {
	idx := map[int]bool{}
	if fl == nil {
		return idx
	}
	i := 0
	for _, field := range fl.List {
		for range max(len(field.Names), 1) {
			idx[i] = isPoolType(field.Type, alias)
			i++
		}
	}
	return idx
}

func (j poolJudge) allowed(e ast.Expr, stack []ast.Node) bool {
	switch p := stack[len(stack)-1].(type) {
	case *ast.SelectorExpr:
		return p.X == e
	case *ast.Field: // a declared name
		return true
	case *ast.KeyValueExpr:
		return p.Key == e || isPoolExpr(p.Key, j.names)
	case *ast.AssignStmt:
		return j.assignAllowed(e, p.Lhs, p.Rhs)
	case *ast.ValueSpec:
		if p.Type != nil && !isPoolType(p.Type, j.alias) {
			return slices.Contains(identExprs(p.Names), e)
		}
		return j.assignAllowed(e, identExprs(p.Names), p.Values)
	case *ast.CallExpr:
		return j.argAllowed(e, p)
	case *ast.ReturnStmt:
		return j.resultAllowed(e, p, stack)
	case *ast.BinaryExpr:
		return p.Op == token.EQL || p.Op == token.NEQ
	}
	return false
}

func identExprs(ids []*ast.Ident) []ast.Expr {
	out := make([]ast.Expr, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// assignAllowed: e is a target, or the value assigned to a pool name (x := r.pool made x a pool name).
func (j poolJudge) assignAllowed(e ast.Expr, lhs, rhs []ast.Expr) bool {
	if slices.Contains(lhs, e) {
		return true
	}
	i := slices.Index(rhs, e)
	return i >= 0 && len(lhs) == len(rhs) && isPoolExpr(lhs[i], j.names)
}

func (j poolJudge) argAllowed(e ast.Expr, call *ast.CallExpr) bool {
	i := slices.Index(call.Args, e)
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return i >= 0 && j.funcs[fn.Name][i]
	case *ast.SelectorExpr: // the read-only registry is keyed by the pool (readonly.go)
		recv, ok := fn.X.(*ast.Ident)
		return i == 0 && ok && recv.Name == "readOnlyPools" && (fn.Sel.Name == "Store" || fn.Sel.Name == "Load")
	}
	return false
}

// resultAllowed: a pool returned where the enclosing function declares a *pgxpool.Pool result.
func (j poolJudge) resultAllowed(e ast.Expr, ret *ast.ReturnStmt, stack []ast.Node) bool {
	i := slices.Index(ret.Results, e)
	for k := len(stack) - 1; k >= 0 && i >= 0; k-- {
		switch fn := stack[k].(type) {
		case *ast.FuncDecl:
			return poolIndexes(fn.Type.Results, j.alias)[i]
		case *ast.FuncLit:
			return poolIndexes(fn.Type.Results, j.alias)[i]
		}
	}
	return false
}
