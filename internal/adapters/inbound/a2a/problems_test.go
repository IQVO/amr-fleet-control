package a2a

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

// carry (plan 04 Task 21 review lesson, ruling P3-R13): every typed read error, wrapped the way the use cases wrap
// it, maps to its exact problem: the generic type the envelope contract names (not-found), the REST catalogue's
// slug as the code (internal/adapters/inbound/http/errors.go), its title and status, and its own message as the
// detail. No problem states a replay class or finality: fleet-not-found and robot-not-found are RETRYABLE (a map
// deploy or a robot's first report clears them), so the envelope carries no field a client could read as final.
func TestEveryTypedReadErrorMapsToItsExactProblem(t *testing.T) {
	cases := []struct {
		err       error
		typ, code string
		title     string
		status    int
	}{
		{usecases.ErrFleetNotFound, "not-found", "fleet-not-found", "Fleet not found", 404},
		{usecases.ErrLegNotFound, "not-found", "leg-not-found", "Leg not found", 404},
		{usecases.ErrRobotNotFound, "not-found", "robot-not-found", "Robot not found", 404},
	}
	if len(cases) != len(useCaseProblems) {
		t.Fatalf("useCaseProblems has %d entries, the table pins %d: pin the new one here", len(useCaseProblems), len(cases))
	}
	for _, c := range cases {
		wrapped := fmt.Errorf("read X-1: %w", c.err)
		p := problemFromUseCase(context.Background(), wrapped)
		if p.Type != c.typ || p.Code != c.code || p.Title != c.title || p.Status != c.status || p.Detail != wrapped.Error() {
			t.Errorf("%v -> %+v, want type %s code %s title %q status %d", c.err, *p, c.typ, c.code, c.title, c.status)
		}
	}
}

func TestAnUnexpectedReadErrorIsAnInternalErrorWithoutItsCause(t *testing.T) {
	p := problemFromUseCase(context.Background(), errors.New("dial tcp 10.0.0.7:5432: password authentication failed"))
	if p.Type != "internal-error" || p.Code != "internal-error" || p.Status != 500 || strings.Contains(p.Detail, "5432") {
		t.Fatalf("problem = %+v, want internal-error 500 with an adapter-owned detail", *p)
	}
}

// Every adapter-owned problem carries its type as its code, so a client switches on one field.
func TestAdapterProblemsCarryTheirTypeAsTheCode(t *testing.T) {
	for _, p := range []*problem{problemMalformed("x"), problemUnknownSkill("x"), problemInvalidArgument("x")} {
		if p.Code == "" || p.Code != p.Type || p.Status != 400 {
			t.Errorf("problem = %+v, want code == type and status 400", *p)
		}
	}
}

// The not-found answers on the wire carry the exact slug, and no card description calls an unknown id final.
func TestNotFoundAnswersNameTheSlugAndNoDescriptionClaimsFinality(t *testing.T) {
	client := agent(t, fakeReads{})
	for code, req := range map[string]map[string]any{
		"fleet-not-found": {"skill": "get_fleet", "arguments": map[string]any{"fleetId": "AMR9"}},
		"leg-not-found":   {"skill": "get_leg", "arguments": map[string]any{"fleetId": "AMR1", "legId": "leg-9"}},
		"robot-not-found": {"skill": "get_robot", "arguments": map[string]any{"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R9"}},
	} {
		env, _ := send(t, client, a2a.NewDataPart(req))
		p, _ := env["problem"].(map[string]any)
		if p["type"] != "not-found" || p["code"] != code || p["status"] != float64(404) {
			t.Errorf("%s: problem %v", code, p)
		}
		for _, k := range []string{"retryable", "final", "replay"} {
			if _, ok := p[k]; ok {
				t.Errorf("%s: the problem carries %q", code, k)
			}
		}
	}
	for _, s := range Card(Config{AgentURL: "http://x/a2a"}).Skills {
		d := strings.ToLower(s.Description)
		for _, w := range []string{"final", "permanent", "never exist", "will never"} {
			if strings.Contains(d, w) {
				t.Errorf("skill %s description claims finality (%q): %s", s.ID, w, s.Description)
			}
		}
		if !strings.Contains(s.Description, "fleet-not-found") {
			t.Errorf("skill %s description does not name fleet-not-found: %s", s.ID, s.Description)
		}
	}
}
