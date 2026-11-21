package architecture

import (
	"path/filepath"
	"strings"
	"testing"
)

// carry (Task 18 review): cmd/a2a exists since Task 19, so no agent surface may be "not built yet": a surface with
// a missing adapter or cmd/ directory is a sensor failure, never a logged pass. Every rule of the read-only sensor
// (the allowlist, the forbidden imports, the write verbs and the pgx raw-SQL entry points) bites on BOTH surfaces.
func TestEveryAgentSurfaceMustBeBuilt(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent")
	for _, s := range agentSurfaces {
		for name, c := range map[string]agentSurface{
			"both dirs absent": withDirs(s, absent, absent),
			"no cmd/ dir":      withDirs(s, s.adapterDir, absent),
			"no adapter dir":   withDirs(s, absent, s.cmdDir),
		} {
			if p := agentSurfaceProblem(c); p == "" || !strings.Contains(p, s.name) {
				t.Errorf("%s surface with %s: problem %q, want a failure naming the surface", s.name, name, p)
			}
		}
		if p := agentSurfaceProblem(s); p != "" {
			t.Errorf("the built %s surface: %s", s.name, p)
		}
	}
}

// withDirs is s (every other field kept) looking at adapterDir and cmdDir.
func withDirs(s agentSurface, adapterDir, cmdDir string) agentSurface {
	s.adapterDir, s.cmdDir = adapterDir, cmdDir
	return s
}

func TestTheSensorRulesBiteOnEverySurface(t *testing.T) {
	pg := "import \"" + modulePath + "/internal/adapters/outbound/postgres\"\n"
	bad := map[string]string{
		"a write use case":           "package x\nvar _ = usecases.NewService\n",
		"kafka-go":                   "package x\nimport _ \"github.com/segmentio/kafka-go\"\n",
		"paho":                       "package x\nimport _ \"github.com/eclipse/paho.mqtt.golang\"\n",
		"a write method on a repo":   "package x\n" + pg + "func f() { _ = postgres.NewRobots(nil).Save(ctx, nil) }\n",
		"raw SQL through pool.Query": "package x\n" + pg + "func f() { p, _ := postgres.NewPool(ctx, dsn); _, _ = p.Query(ctx, \"DELETE FROM missions\") }\n",
		"raw SQL through QueryRow":   "package x\nfunc f(p pool) { _ = p.QueryRow(ctx, \"DELETE FROM missions RETURNING 1\") }\n",
		"a pooled connection":        "package x\nfunc f(p pool) { c, _ := p.Acquire(ctx); _ = c.Conn().PgConn() }\n",
		"a pgx import":               "package x\nimport _ \"github.com/jackc/pgx/v5/pgxpool\"\n",
		"an aliased write use case":  "package x\nimport uc \"" + modulePath + "/internal/application/usecases\"\nvar _ = uc.NewService\n",
		"a write method on Reads":    "package x\ntype Reads interface{ Fleet(); Leg(); OpenLegs(); Robot(); CancelLeg() }\n",
	}
	for _, s := range agentSurfaces {
		for name, src := range bad {
			if got, err := agentSourceViolations(s, "fixture.go", src); err != nil || len(got) == 0 {
				t.Errorf("%s surface: the sensor does not flag %s (violations=%v, err=%v)", s.name, name, got, err)
			}
		}
	}
	// Each surface refuses the OTHER agent adapter.
	for _, c := range []struct {
		surface int
		sibling string
	}{{0, "/internal/adapters/inbound/a2a"}, {1, "/internal/adapters/inbound/mcp"}} {
		src := "package x\nimport _ \"" + modulePath + c.sibling + "\"\n"
		if got, _ := agentSourceViolations(agentSurfaces[c.surface], "fixture.go", src); len(got) == 0 {
			t.Errorf("the %s surface may import its sibling %s", agentSurfaces[c.surface].name, c.sibling)
		}
	}
}
