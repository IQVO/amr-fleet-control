package architecture

import (
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// TestA2AAdapterDependencyRule is the A2A twin of TestMCPAdapterDependencyRule (fleet rule .claude/rules/fleet/a2a.md;
// ported from material-flow-control 622190f agent_readonly_fitness_test.go): the A2A inbound adapter may depend only
// on the application and domain layers, and nothing else in internal/ may depend on it (cmd/a2a wires it).
func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)
	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		assertArchGoPasses(t, archgo.CheckArchitecture(moduleInfo, configuration.Config{DependenciesRules: []*configuration.DependenciesRule{{
			Package: "**.internal.adapters.inbound.a2a.**",
			ShouldOnlyDependsOn: &configuration.Dependencies{Internal: []string{"**.internal.adapters.inbound.a2a.**",
				"**.internal.application.**", "**.internal.domain.**"}},
		}}}))
	})
	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		assertArchGoPasses(t, archgo.CheckArchitecture(moduleInfo, configuration.Config{DependenciesRules: []*configuration.DependenciesRule{{
			Package:            "**.internal.**",
			ShouldNotDependsOn: &configuration.Dependencies{Internal: []string{"**.internal.adapters.inbound.a2a.**"}},
		}}}))
	})
}
