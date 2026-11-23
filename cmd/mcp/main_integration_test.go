//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/idempotency"
	"github.com/IQVO/amr-fleet-control/internal/application/outbox"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

var seedAt = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

// seed writes one row into EVERY table cmd/api owns, through the write repositories (test-only wiring:
// the MCP binary has no write seam), so a write by the MCP binary to any table changes a fingerprint.
func seed(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rec := fleetrow.Record{MapVersion: "2026-11-20.1", Reservations: fleetmap.State{Nodes: map[string]string{"P1": "EMU/R1"}},
		Capacity: capacity.Snapshot{RobotsTotal: 1, RobotsOnline: 1, At: seedAt}, HasCapacity: true}
	r, _ := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, seedAt)
	r.ApplyConnection(robot.ConnectionOnline, seedAt, seedAt)
	r.ApplyReport(robot.Report{At: seedAt, LastNodeID: "P1", MapID: "S1", Battery: 80}, "P1", seedAt)
	leg, err := mission.NewLeg(mission.LegSpec{ID: "leg-1", MapVersion: "2026-11-20.1", ContainerID: "C1", From: "ST1",
		To: "ASRS1-IN1", PickupNode: "ST1", DropNode: "ASRS1-IN1", At: seedAt})
	if err != nil {
		t.Fatal(err)
	}
	msg := outbox.Message{EventID: "ev-1", Topic: "warehouse.amr-fleet-control.events", EventType: "t", Subject: "leg-1",
		Key: []byte("leg-1"), DataSchema: "urn", Value: []byte(`{}`), Headers: []outbox.Header{{Key: "content-type", Value: "application/json"}}}
	idem := idempotency.IdempotencyRecord{Key: "k-1", Fingerprint: "f", Status: 202, ContentType: "application/json", Body: []byte(`{}`), CreatedAt: seedAt}
	for name, write := range map[string]func() error{
		"fleet":       func() error { return postgres.NewFleets(pool).Save(ctx, "AMR1", rec) },
		"robot":       func() error { return postgres.NewRobots(pool).Save(ctx, r) },
		"leg":         func() error { return postgres.NewMissions(pool).Insert(ctx, leg) },
		"outbox":      func() error { return postgres.NewOutboxRepo(pool).Insert(ctx, msg) },
		"idempotency": func() error { return postgres.NewIdempotencyStore(pool).Save(ctx, idem, seedAt.Add(-time.Hour)) },
	} {
		if err := write(); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
}

// fingerprints is every public table's row count and md5 over its rows in a stable order: any INSERT,
// UPDATE or DELETE changes it (a row count alone misses an UPDATE).
func fingerprints(t *testing.T, url string) map[string]string {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	out := map[string]string{}
	for _, table := range tables {
		var n int
		var sum string
		q := fmt.Sprintf(`SELECT count(*), coalesce(md5(string_agg(t::text, '|' ORDER BY t::text)), '') FROM %q t`, table)
		if err := pool.QueryRow(ctx, q).Scan(&n, &sum); err != nil {
			t.Fatalf("fingerprint %s: %v", table, err)
		}
		out[table] = fmt.Sprintf("%d:%s", n, sum)
	}
	return out
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// waitHealthy polls /healthz until it answers 200 or the deadline passes, reporting what it last saw.
func waitHealthy(t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	last := "no attempt"
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		resp, err := client.Get(base + "/healthz")
		if err != nil {
			last = err.Error()
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return
		}
		last = resp.Status
	}
	t.Fatalf("cmd/mcp never became healthy at %s; last seen: %s", base, last)
}

func callTool(t *testing.T, s *sdk.ClientSession, name, args string) (map[string]any, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out map[string]any
	if res.IsError {
		return map[string]any{"error": res.Content[0].(*sdk.TextContent).Text}, true
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, false
}

// The REAL composition root end to end: run() boots on Postgres (migrations, pool, Reader, adapter,
// router), the SDK client drives every tool and the resource over Streamable HTTP, and afterwards every
// public table is byte-identical to before: the MCP binary writes nothing.
func TestTheMCPBinaryServesPostgresReadsAndWritesNothing(t *testing.T) {
	url := pgtest.URL(t)
	seed(t, url)
	before := fingerprints(t, url)
	for _, table := range []string{"fleets", "robots", "missions", "outbox_events", "idempotency_keys", "schema_migrations"} {
		if v, ok := before[table]; !ok || strings.HasPrefix(v, "0:") {
			t.Fatalf("the seed left %s empty or absent: %v", table, before)
		}
	}
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, io.Discard, env(map[string]string{"MCP_ADDR": addr, "DATABASE_URL": url, "AMR_MAP_FILE": exampleMap}))
	}()
	waitHealthy(t, "http://"+addr)
	exerciseEveryRead(t, "http://"+addr+"/mcp")
	if after := fingerprints(t, url); !maps(before, after) {
		t.Fatalf("the MCP binary changed the database:\nbefore %v\nafter  %v", before, after)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run after SIGTERM-equivalent = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return within 15 s of the context ending")
	}
}

func maps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func exerciseEveryRead(t *testing.T, endpoint string) {
	t.Helper()
	s, err := sdk.NewClient(&sdk.Implementation{Name: "it", Version: "0"}, nil).
		Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	assertTheToolSet(t, s)
	assertTheReads(t, s)
	assertTheRefusals(t, s)
	res, err := s.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "warehouse://amr-fleet-control/fleets/AMR1"})
	if err != nil || !strings.Contains(res.Contents[0].Text, `"fleetId":"AMR1"`) {
		t.Fatalf("read resource = %+v %v", res, err)
	}
}

func assertTheToolSet(t *testing.T, s *sdk.ClientSession) {
	t.Helper()
	tools, err := s.ListTools(context.Background(), nil)
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if err != nil || strings.Join(names, ",") != "get_fleet,get_leg,get_robot,list_open_legs" {
		t.Fatalf("tools = %v (%v)", names, err)
	}
}

func assertTheReads(t *testing.T, s *sdk.ClientSession) {
	t.Helper()
	if f, isErr := callTool(t, s, "get_fleet", `{"fleetId":"AMR1"}`); isErr || f["mapVersion"] != "2026-11-22.1" || len(f["robots"].([]any)) != 1 {
		t.Fatalf("get_fleet = %v", f)
	}
	if o, isErr := callTool(t, s, "list_open_legs", `{"fleetId":"AMR1"}`); isErr || len(o["legs"].([]any)) != 1 {
		t.Fatalf("list_open_legs = %v", o)
	}
	if l, isErr := callTool(t, s, "get_leg", `{"fleetId":"AMR1","legId":"leg-1"}`); isErr || l["status"] != "QUEUED" || l["containerId"] != "C1" {
		t.Fatalf("get_leg = %v", l)
	}
	if r, isErr := callTool(t, s, "get_robot", `{"fleetId":"AMR1","manufacturer":"EMU","serialNumber":"R1"}`); isErr || r["connection"] != "ONLINE" || r["nodeId"] != "P1" {
		t.Fatalf("get_robot = %v", r)
	}
}

func assertTheRefusals(t *testing.T, s *sdk.ClientSession) {
	t.Helper()
	for args, slug := range map[string]string{
		`{"fleetId":"AMR1","legId":"leg-9"}`:                          "leg-not-found",
		`{"fleetId":"AMR1","manufacturer":"","serialNumber":"R1"}`:    "validation-failed",
		`{"fleetId":"AMR1","manufacturer":"EMU","serialNumber":"R9"}`: "robot-not-found",
		`{"fleetId":"AMR9","manufacturer":"EMU","serialNumber":"R1"}`: "fleet-not-found",
	} {
		tool := "get_robot"
		if strings.Contains(args, "legId") {
			tool = "get_leg"
		}
		if e, isErr := callTool(t, s, tool, args); !isErr || !strings.HasPrefix(e["error"].(string), slug+":") {
			t.Errorf("%s %s = %v, want %s", tool, args, e, slug)
		}
	}
}
