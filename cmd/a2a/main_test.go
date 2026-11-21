package main

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	inbounda2a "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/a2a"
)

func TestBootConfigNamesEveryMissingVariable(t *testing.T) {
	_, err := loadBootConfig(func(string) string { return "" })
	for _, v := range []string{"DATABASE_URL is required", "AMR_MAP_FILE is required", "A2A_AGENT_URL is required"} {
		if err == nil || !strings.Contains(err.Error(), v) {
			t.Fatalf("err = %v, want %q", err, v)
		}
	}
	c, err := loadBootConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "x", "AMR_MAP_FILE": "m", "A2A_AGENT_URL": "http://a/a2a"}[k]
	})
	if err != nil || c.addr != ":8093" {
		t.Fatalf("defaults = %+v %v", c, err)
	}
}

func TestTheCardAndHealthzAreServed(t *testing.T) {
	h := newAgent(inbounda2a.Deps{}, "http://amr-fleet-control-a2a:8093/a2a", "0.1.0", slog.Default(), "amr-fleet-control-a2a")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/.well-known/agent-card.json", nil))
	var card map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil || rec.Code != 200 || card["name"] != "amr-fleet-control" {
		t.Fatalf("card = %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("healthz = %d", rec.Code)
	}
}
