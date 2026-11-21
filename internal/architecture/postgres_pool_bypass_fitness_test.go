package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"
)

// carry (Task 19 review): the read-only agent pools (postgres.NewReadOnlyPool) stay read-only only because every
// statement of the postgres adapter goes through begin(), which opens the transaction READ ONLY on such a pool
// (txOptions). A repository method calling the pool directly (r.pool.Exec, pool.QueryRow, ...) would run in
// autocommit on a read-write session and write through the agents' pool. This sensor makes the rule structural:
// in the non-test sources of internal/adapters/outbound/postgres no pool method that runs SQL or hands out a
// connection is selected on a *pgxpool.Pool value, except pool.BeginTx inside begin() itself.
//
// A parse-only sensor (no type check): a value counts as a pool when it is a field, parameter, result or variable
// declared as *<pgxpool>.Pool anywhere in the package, or a local assigned from one; a selector on such a name is
// judged by its method name.
var pgPoolRefusedMethods = []string{"Exec", "Query", "QueryRow", "SendBatch", "CopyFrom", "Acquire", "AcquireFunc",
	"AcquireAllIdle", "Begin", "BeginTx"}

const pgxpoolImport = "github.com/jackc/pgx/v5/pgxpool"

// pgxpoolName is the local name f imports pgxpool under ("" when it does not).
func pgxpoolName(f *ast.File) string {
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == pgxpoolImport {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return "pgxpool"
		}
	}
	return ""
}

func isPoolType(e ast.Expr, alias string) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok || alias == "" {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Pool" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == alias
}

// poolNames collects every name the package declares with the pool type, plus locals assigned from one.
func poolNames(files []*ast.File) map[string]bool {
	names := map[string]bool{}
	for _, f := range files {
		declaredPoolNames(f, names)
	}
	for propagatePoolLocals(files, names) { // x := <pool>, x = <pool>, until nothing new
	}
	return names
}

// declaredPoolNames adds the fields, parameters, results and variables f declares as *<pgxpool>.Pool.
func declaredPoolNames(f *ast.File, names map[string]bool) {
	alias := pgxpoolName(f)
	ast.Inspect(f, func(n ast.Node) bool {
		var ids []*ast.Ident
		switch d := n.(type) {
		case *ast.Field:
			if isPoolType(d.Type, alias) {
				ids = d.Names
			}
		case *ast.ValueSpec:
			if d.Type != nil && isPoolType(d.Type, alias) {
				ids = d.Names
			}
		}
		for _, id := range ids {
			names[id.Name] = true
		}
		return true
	})
}

// propagatePoolLocals adds every identifier assigned from a pool expression; it reports whether it added one.
func propagatePoolLocals(files []*ast.File, names map[string]bool) bool {
	changed := false
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != len(as.Rhs) {
				return true
			}
			for i, rhs := range as.Rhs {
				if id, isIdent := as.Lhs[i].(*ast.Ident); isIdent && !names[id.Name] && isPoolExpr(rhs, names) {
					names[id.Name], changed = true, true
				}
			}
			return true
		})
	}
	return changed
}

// isPoolExpr: an identifier or a field selector (r.pool) whose name is a pool name.
func isPoolExpr(e ast.Expr, names map[string]bool) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return names[x.Name]
	case *ast.SelectorExpr:
		return names[x.Sel.Name]
	case *ast.ParenExpr:
		return isPoolExpr(x.X, names)
	}
	return false
}

// poolBypassViolations returns every refused pool selector of the package files, and how many times the one
// allowed selector (pool.BeginTx inside the top-level func begin) was seen.
func poolBypassViolations(fset *token.FileSet, files []*ast.File) (violations []string, allowed int) {
	names := poolNames(files)
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			inBegin := isFunc && fn.Recv == nil && fn.Name.Name == "begin"
			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || !slices.Contains(pgPoolRefusedMethods, sel.Sel.Name) || !isPoolExpr(sel.X, names) {
					return true
				}
				if inBegin && sel.Sel.Name == "BeginTx" {
					allowed++
					return true
				}
				violations = append(violations, fset.Position(sel.Pos()).String()+": selects "+exprString(sel)+
					" on a *pgxpool.Pool: every statement goes through begin() (READ ONLY on a NewReadOnlyPool pool)")
				return true
			})
		}
	}
	return violations, allowed
}

func exprString(sel *ast.SelectorExpr) string {
	switch x := sel.X.(type) {
	case *ast.Ident:
		return x.Name + "." + sel.Sel.Name
	case *ast.SelectorExpr:
		return exprString(x) + "." + sel.Sel.Name
	}
	return "?." + sel.Sel.Name
}

func TestPostgresAdapterNeverBypassesBegin(t *testing.T) {
	paths := goFiles(t, "internal/adapters/outbound/postgres")
	if len(paths) < 8 {
		t.Fatalf("scanned %d postgres sources (%v): the sensor must RUN over the adapter", len(paths), paths)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		files = append(files, f)
	}
	violations, allowed := poolBypassViolations(fset, files)
	for _, v := range violations {
		t.Error(v)
	}
	if allowed != 1 {
		t.Errorf("pool.BeginTx inside begin() seen %d times, want exactly 1 (the sensor no longer sees the one legit call)", allowed)
	}
}

const poolFixturePrelude = "package postgres\n\nimport (\n\t\"context\"\n\n\t\"github.com/jackc/pgx/v5/pgxpool\"\n)\n\n" +
	"type Fleets struct{ pool *pgxpool.Pool }\n\n" +
	"func begin(ctx context.Context, pool *pgxpool.Pool) error { _, err := pool.BeginTx(ctx, txOptions(pool)); return err }\n\n"

func poolFixtureViolations(t *testing.T, src string) ([]string, int) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v\n%s", err, src)
	}
	return poolBypassViolations(fset, []*ast.File{f})
}

func TestPostgresPoolBypassSensorBites(t *testing.T) {
	bad := map[string]string{
		"a field Exec (overlay o2_fleets_bare)": "func (r *Fleets) Save(ctx context.Context) error { _, err := r.pool.Exec(ctx, `INSERT`); return err }",
		"a parameter QueryRow":                  "func read(ctx context.Context, pool *pgxpool.Pool) { _ = pool.QueryRow(ctx, `SELECT 1`) }",
		"a renamed parameter Query":             "func read(ctx context.Context, p *pgxpool.Pool) { _, _ = p.Query(ctx, `SELECT 1`) }",
		"a local copy of the field":             "func (r *Fleets) X(ctx context.Context) { db := r.pool; _, _ = db.Begin(ctx) }",
		"Acquire":                               "func (r *Fleets) X(ctx context.Context) { _, _ = r.pool.Acquire(ctx) }",
		"SendBatch":                             "func (r *Fleets) X(ctx context.Context) { _ = r.pool.SendBatch(ctx, nil) }",
		"CopyFrom":                              "func (r *Fleets) X(ctx context.Context) { _, _ = r.pool.CopyFrom(ctx, nil, nil, nil) }",
		"BeginTx outside begin":                 "func (r *Fleets) X(ctx context.Context) { _, _ = r.pool.BeginTx(ctx, txOptions(r.pool)) }",
		"a var of the pool type":                "var shared *pgxpool.Pool\n\nfunc X(ctx context.Context) { _, _ = shared.Exec(ctx, `DELETE`) }",
	}
	for name, body := range bad {
		if v, _ := poolFixtureViolations(t, poolFixturePrelude+body+"\n"); len(v) == 0 {
			t.Errorf("%s: the sensor let it through:\n%s", name, body)
		}
	}
	aliased := "package postgres\n\nimport (\n\t\"context\"\n\n\tpp \"github.com/jackc/pgx/v5/pgxpool\"\n)\n\n" +
		"type Missions struct{ db *pp.Pool }\n\nfunc (r *Missions) X(ctx context.Context) { _, _ = r.db.Exec(ctx, `DELETE`) }\n"
	if v, _ := poolFixtureViolations(t, aliased); len(v) != 1 {
		t.Errorf("an aliased pgxpool import = %v, want one violation", v)
	}
	// The legit shapes: begin's BeginTx, a transaction's Exec, the helpers taking the pool, Close.
	good := poolFixturePrelude + "func (r *Fleets) Save(ctx context.Context, tx interface{ Exec(context.Context, string) error }) error {\n" +
		"\t_ = begin(ctx, r.pool)\n\tr.pool.Close()\n\treturn tx.Exec(ctx, `INSERT`)\n}\n"
	if v, allowed := poolFixtureViolations(t, good); len(v) != 0 || allowed != 1 {
		t.Errorf("legit shapes = %v (allowed %d), want none and 1", v, allowed)
	}
}
