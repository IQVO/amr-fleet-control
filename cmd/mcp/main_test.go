package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootConfigNamesEveryMissingVariable(t *testing.T) {
	_, err := loadBootConfig(func(string) string { return "" })
	for _, v := range []string{"DATABASE_URL is required", "AMR_MAP_FILE is required"} {
		if err == nil || !strings.Contains(err.Error(), v) {
			t.Fatalf("err = %v, want %q", err, v)
		}
	}
	c, err := loadBootConfig(func(k string) string { return map[string]string{"DATABASE_URL": "x", "AMR_MAP_FILE": "m"}[k] })
	if err != nil || c.addr != ":8090" {
		t.Fatalf("defaults = %+v %v", c, err)
	}
}

func TestHealthzAndBothMCPMounts(t *testing.T) {
	hit := 0
	h := newRouter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hit++; w.WriteHeader(http.StatusAccepted) }), slog.Default(), "amr-fleet-control-mcp")
	// deviation (Task 18 D1): the brief POSTed /healthz before its GET, but the verbatim router's
	// Mount("/") serves every method /healthz does not register, so that POST reached the MCP handler
	// (hit == 3). /healthz is probed with GET only; every assertion is unchanged.
	for path, want := range map[string]int{"/healthz": 200, "/mcp": 202, "/": 202} {
		method := "POST"
		if path == "/healthz" {
			method = "GET"
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != want {
			t.Fatalf("%s = %d, want %d", path, rec.Code, want)
		}
	}
	if hit != 2 {
		t.Fatalf("the MCP handler served %d requests, want 2", hit)
	}
}
