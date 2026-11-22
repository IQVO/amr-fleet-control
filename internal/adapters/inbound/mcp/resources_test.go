package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// carry (plan 04 Task 26 lesson, applied here): an unknown fleet on the resource is the protocol's
// resource-not-found (mcp.ResourceNotFoundError: -32602 per SEP-2164, its data naming the URI), never a
// generic internal error, and
// an infrastructure error leaks no DSN.
func TestTheFleetResourceRefusesAnUnknownFleetAsResourceNotFound(t *testing.T) {
	s := session(t)
	uri := "warehouse://amr-fleet-control/fleets/AMR9"
	_, err := s.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri})
	var wire *jsonrpc.Error
	if !errors.As(err, &wire) || wire.Code != jsonrpc.CodeInvalidParams || !strings.Contains(string(wire.Data), uri) {
		t.Fatalf("read %s = %v (%#v), want resource-not-found naming the URI", uri, err, wire)
	}
	_, err = sessionWith(t, brokenReads{}).ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "warehouse://amr-fleet-control/fleets/AMR1"})
	if err == nil || strings.Contains(err.Error(), "postgres://") || !strings.Contains(err.Error(), "internal-error") {
		t.Fatalf("read over a broken database = %v, want the generic internal-error", err)
	}
}
