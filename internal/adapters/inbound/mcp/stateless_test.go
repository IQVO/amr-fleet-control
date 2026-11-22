package mcp_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inboundmcp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/mcp"
)

// Stateless (Agentic Standard v2, AS-4; ported from material-flow-control cafaee6): no Mcp-Session-Id
// is issued, and a legacy initialize handshake and a sessionless tools/list both work over plain HTTP;
// an Authorization header changes nothing (no auth, fleet rule).

func post(t *testing.T, body, auth string) (int, http.Header, string) {
	t.Helper()
	srv := httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: fakeReads{}, FleetID: "AMR1"})))
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(raw)
}

func TestInitializeIssuesNoSessionIDAndNeedsNoAuth(t *testing.T) {
	for _, auth := range []string{"", "Bearer nonsense"} {
		status, header, body := post(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`, auth)
		if status != http.StatusOK || header.Get("Mcp-Session-Id") != "" || !strings.Contains(body, "amr-fleet-control-mcp") {
			t.Fatalf("initialize (Authorization %q) = %d session %q body %s", auth, status, header.Get("Mcp-Session-Id"), body)
		}
	}
}

func TestToolsListOverHTTPWithoutASession(t *testing.T) {
	status, _, body := post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "")
	if status != http.StatusOK || !strings.Contains(body, "get_fleet") || !strings.Contains(body, "list_open_legs") {
		t.Fatalf("tools/list = %d %s", status, body)
	}
}
