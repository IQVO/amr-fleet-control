package architecture

// Agent surface fitness (ADR 0006): the MCP server and the A2A agent are READ-ONLY adapters.
//
// deviation (Task 18): the brief ships no read-only sensor; this is plan 04's hardened one
// (material-flow-control 622190f internal/architecture/agent_readonly_fitness_test.go and
// agent_readonly_methods_fitness_test.go, after its Task 20 and Task 21 reviews), ported to this
// module's read seam. arch-go does not constrain cmd/, so this sensor is the only guard of the
// composition roots.
//
//   - TestAgentSurfacesAreReadOnly: an ALLOWLIST sensor over each adapter and its cmd/ composition
//     root. The adapter's `Reads` seam holds exactly the listed read methods; the only
//     usecases/ports/postgres identifiers the sources may reference are the read ones (a qualifier is
//     resolved through the file's imports to its import path, so an alias cannot launder a write seam,
//     and a dot or aliased import of those three packages is itself refused); and no write-side or
//     robot-side adapter (Kafka, CloudEvents, outbox, events, MQTT, VDA 5050, paho, the memory doubles),
//     no sibling inbound adapter and no outbound A2A client is imported. Allowlist, not denylist: a new
//     write use case is refused without having to be named here first.
//   - Both surfaces are built (cmd/mcp Task 18, cmd/a2a Task 19): a missing adapter or cmd/ directory
//     fails instead of passing vacuously (carry, Task 18 review: no "not built yet" branch).
//   - TestAgentReadOnlySensorBites proves each rule fires on code fixtures.
//   - agent_readonly_methods_fitness_test.go: no write METHOD is selected on any value, and no raw
//     driver (pgx, database/sql) is imported.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// agentSurface is one read-only agent adapter and its composition root.
type agentSurface struct {
	name, adapterDir, cmdDir string
	reads                    []string // the exact method set of the adapter's Reads seam, sorted
	forbidSibling            string   // the other agent adapter, never imported
}

// agentReads is the read method set of both Reads seams (*usecases.Service built by NewReader implements it).
var agentReads = []string{"Fleet", "Leg", "OpenLegs", "Robot"}

var agentSurfaces = []agentSurface{
	{"mcp", "../adapters/inbound/mcp", "../../cmd/mcp", agentReads, "/internal/adapters/inbound/a2a"},
	{"a2a", "../adapters/inbound/a2a", "../../cmd/a2a", agentReads, "/internal/adapters/inbound/mcp"},
}

// agentAllowedSelectors maps a guarded package to the identifiers agent sources may reference through it.
// Anything else (a write use case, NewService, the unit of work, the fleet lock, the outbox) is a violation.
var agentAllowedSelectors = map[string][]string{
	"ports": {},
	"usecases": {"NewReader", "DefaultConfig", "Config", "FleetView", "LegView", "RobotView",
		"ErrFleetNotFound", "ErrLegNotFound", "ErrRobotNotFound"},
	"postgres": {"MigrationsURL", "RunMigrations", "NewPool", "NewRobots", "NewMissions"},
}

// agentForbiddenImports are import-path fragments no agent source may use, and why.
var agentForbiddenImports = map[string]string{
	"a2a-go/v2/a2aclient":                 "an outbound A2A client (the agent only answers)",
	"github.com/segmentio/kafka-go":       "Kafka: a read-only surface never dials Kafka",
	"/internal/adapters/outbound/kafka":   "Kafka: a read-only surface publishes no fact",
	"/internal/adapters/kafka":            "the CloudEvents codec: a read-only surface encodes no fact",
	"/internal/adapters/outbound/outbox":  "the outbox relay: a read-only surface never publishes",
	"/internal/adapters/outbound/events":  "the event sink: a read-only surface never appends an event",
	"/internal/adapters/mqtt":             "the fleet link: a read-only surface never dials MQTT",
	"/internal/adapters/vda5050":          "the VDA 5050 codec: a read-only surface never talks to a robot",
	"github.com/eclipse/paho.mqtt.golang": "MQTT: a read-only surface never dials the broker",
	"/internal/adapters/outbound/memory":  "the in-memory test doubles (real Postgres only at runtime)",
	"/internal/adapters/inbound/http":     "the REST adapter (the inbound adapters are siblings)",
}

func agentSourceViolations(s agentSurface, filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, 0)
	if err != nil {
		return nil, err
	}
	out := agentImportViolations(s, f)
	out = append(out, agentSelectorViolations(f)...)
	names, _ := agentQualifiers(f)
	out = append(out, agentWriteMethodViolations(f, names)...)
	return append(out, agentReadsViolations(s, f)...), nil
}

func agentImportViolations(s agentSurface, f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		for fragment, why := range agentForbiddenImports {
			if strings.Contains(path, fragment) {
				out = append(out, "imports "+path+": "+why)
			}
		}
		if strings.Contains(path, s.forbidSibling) {
			out = append(out, "imports "+path+": the agent adapters are siblings")
		}
	}
	return out
}

// agentGuardedPackages maps the import-path suffix of each guarded package to its allowlist key: the
// allowlist applies by IMPORT PATH, whatever local name a file binds the package to.
var agentGuardedPackages = map[string]string{
	"/internal/application/usecases":       "usecases",
	"/internal/application/ports":          "ports",
	"/internal/adapters/outbound/postgres": "postgres",
}

// guardedKey is the allowlist key of an import path, "" when the path is not a guarded package.
func guardedKey(path string) string {
	for suffix, key := range agentGuardedPackages {
		if strings.HasSuffix(path, suffix) {
			return key
		}
	}
	return ""
}

// agentQualifiers resolves every local package name of f to its import path (the alias, or the last path
// element), and refuses a dot or aliased import of a guarded package: a dot import hides every selector the
// allowlist reads, and an alias only obscures which package a reader sees.
func agentQualifiers(f *ast.File) (map[string]string, []string) {
	names, out := map[string]string{}, []string(nil)
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if key := guardedKey(path); key != "" && imp.Name != nil && local != "_" {
			if local == "." {
				out = append(out, "dot-imports "+path+": a guarded package must be referenced as "+key+".<Name>")
				continue
			}
			if local != key {
				out = append(out, "imports "+path+" as "+local+": a guarded package must keep its name "+key)
			}
		}
		names[local] = path
	}
	return names, out
}

func agentSelectorViolations(f *ast.File) []string {
	names, out := agentQualifiers(f)
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Obj != nil { // not a qualifier, or a variable declared in this file
			return true
		}
		key := pkg.Name // a qualifier no import binds (fixtures): judged by its name
		if path, imported := names[pkg.Name]; imported {
			key = guardedKey(path)
		}
		if allowed, guarded := agentAllowedSelectors[key]; guarded && !slices.Contains(allowed, sel.Sel.Name) {
			out = append(out, "references "+pkg.Name+"."+sel.Sel.Name+" ("+key+"), which is not on the read-only allowlist")
		}
		return true
	})
	return out
}

func agentReadsViolations(s agentSurface, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Reads" {
			return true
		}
		iface, ok := spec.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}
		var got []string
		for _, m := range iface.Methods.List {
			for _, name := range m.Names {
				got = append(got, name.Name)
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(s.reads, ",") {
			out = append(out, "Reads declares "+strings.Join(got, ",")+", want exactly "+strings.Join(s.reads, ","))
		}
		return false
	})
	return out
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// agentSurfaceProblem is why s cannot be checked: a missing adapter or cmd/ directory. carry (Task 18 review):
// both surfaces are built since Task 19, so a missing directory is a failure, never a vacuous or logged pass.
func agentSurfaceProblem(s agentSurface) string {
	adapter, cmd := dirExists(s.adapterDir), dirExists(s.cmdDir)
	if !adapter || !cmd {
		return fmt.Sprintf("the %s surface needs both %s (exists=%v) and %s (exists=%v)", s.name, s.adapterDir, adapter, s.cmdDir, cmd)
	}
	return ""
}

// agentSurfaceDirs is the directories of s to check; a surface with a missing directory fails the sensor.
func agentSurfaceDirs(t *testing.T, s agentSurface) []string {
	t.Helper()
	if p := agentSurfaceProblem(s); p != "" {
		t.Fatal(p)
	}
	return []string{s.adapterDir, s.cmdDir}
}

func TestAgentSurfacesAreReadOnly(t *testing.T) {
	for _, s := range agentSurfaces {
		for _, dir := range agentSurfaceDirs(t, s) {
			files := goFilesUnder(t, dir, false)
			if len(files) == 0 {
				t.Fatalf("no %s sources under %s; the sensor is looking in the wrong place", s.name, dir)
			}
			t.Logf("checking %d %s sources under %s", len(files), s.name, dir)
			for _, path := range files {
				for _, v := range agentFileViolations(t, s, path) {
					t.Errorf("%s %s (ADR 0006: the %s surface is read-only)", path, v, s.name)
				}
			}
		}
	}
}

func agentFileViolations(t *testing.T, s agentSurface, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := agentSourceViolations(s, path, src)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return violations
}

func TestAgentReadOnlySensorBites(t *testing.T) {
	mcpSurface := agentSurfaces[0]
	bad := map[string]string{
		"a write use case":        "package x\nvar _ = usecases.NewService\n",
		"the serial unit of work": "package x\nvar _ = usecases.NewSerialUnitOfWork\n",
		"the unit of work":        "package x\nvar _ = postgres.NewUnitOfWork\n",
		"the fleet lock":          "package x\nvar _ = postgres.NewFleets\n",
		"the outbox repo":         "package x\nvar _ = postgres.NewOutboxRepo\n",
		"the idempotency store":   "package x\nvar _ = postgres.NewIdempotencyStore\n",
		"a port":                  "package x\nvar _ = ports.UnitOfWork\n",
		"a Kafka import":          "package x\nimport _ \"" + modulePath + "/internal/adapters/outbound/kafka\"\n",
		"kafka-go":                "package x\nimport _ \"github.com/segmentio/kafka-go\"\n",
		"the CloudEvents codec":   "package x\nimport _ \"" + modulePath + "/internal/adapters/kafka/cloudevents\"\n",
		"the outbox relay":        "package x\nimport _ \"" + modulePath + "/internal/adapters/outbound/outbox\"\n",
		"the event sink":          "package x\nimport _ \"" + modulePath + "/internal/adapters/outbound/events\"\n",
		"the fleet link":          "package x\nimport _ \"" + modulePath + "/internal/adapters/mqtt\"\n",
		"the VDA 5050 codec":      "package x\nimport _ \"" + modulePath + "/internal/adapters/vda5050\"\n",
		"paho":                    "package x\nimport _ \"github.com/eclipse/paho.mqtt.golang\"\n",
		"an A2A client":           "package x\nimport _ \"github.com/a2aproject/a2a-go/v2/a2aclient\"\n",
		"a memory double":         "package x\nimport _ \"" + modulePath + "/internal/adapters/outbound/memory\"\n",
		"the REST adapter":        "package x\nimport _ \"" + modulePath + "/internal/adapters/inbound/http\"\n",
		"the sibling A2A adapter": "package x\nimport _ \"" + modulePath + "/internal/adapters/inbound/a2a\"\n",
		"a write method on Reads": "package x\ntype Reads interface{ Fleet(); Leg(); OpenLegs(); Robot(); CancelLeg() }\n",
		"a missing read method":   "package x\ntype Reads interface{ Fleet() }\n",
		// The allowlist resolves a qualifier through the file's imports, so a local alias or a dot import
		// of a guarded package cannot launder a write seam past it (plan 04 Task 20 review).
		"an aliased write use case": "package x\nimport uc \"" + modulePath + "/internal/application/usecases\"\nvar _ = uc.NewService\n",
		"an aliased unit of work":   "package x\nimport pg \"" + modulePath + "/internal/adapters/outbound/postgres\"\nvar _ = pg.NewUnitOfWork\n",
		"an aliased port":           "package x\nimport p \"" + modulePath + "/internal/application/ports\"\nvar _ = p.UnitOfWork\n",
		"an aliased guarded import": "package x\nimport uc \"" + modulePath + "/internal/application/usecases\"\nvar _ = uc.NewReader\n",
		"a dot import":              "package x\nimport . \"" + modulePath + "/internal/application/usecases\"\nvar _ = NewService\n",
	}
	for name, src := range bad {
		if got, err := agentSourceViolations(mcpSurface, "fixture.go", src); err != nil || len(got) == 0 {
			t.Errorf("the sensor does not flag %s (violations=%v, err=%v)", name, got, err)
		}
	}
	good := "package x\n// usecases.NewService and postgres.NewUnitOfWork are only named in this comment\n" +
		"import _ \"github.com/modelcontextprotocol/go-sdk/mcp\"\n" +
		"var _ = usecases.NewReader\nvar _ = postgres.NewRobots\nvar _ = postgres.NewMissions\n" +
		"type Reads interface{ Robot(); OpenLegs(); Leg(); Fleet() }\n"
	if got, err := agentSourceViolations(mcpSurface, "fixture.go", good); err != nil || len(got) != 0 {
		t.Errorf("false positive on the read wiring: %v (err=%v)", got, err)
	}
	// cmd/mcp's own aliases name packages the allowlist does not guard, and a guarded local NAME bound to
	// another import path is not that package.
	mcpWiring := "package x\nimport inboundmcp \"" + modulePath + "/internal/adapters/inbound/mcp\"\n" +
		"import mapfile \"" + modulePath + "/internal/config\"\nimport usecases \"example.com/elsewhere/usecases\"\n" +
		"var _ = inboundmcp.NewServer\nvar _ = mapfile.LoadMap\nvar _ = usecases.NewService\n"
	if got, err := agentSourceViolations(mcpSurface, "fixture.go", mcpWiring); err != nil || len(got) != 0 {
		t.Errorf("false positive on cmd/mcp's aliased wiring: %v (err=%v)", got, err)
	}
}
