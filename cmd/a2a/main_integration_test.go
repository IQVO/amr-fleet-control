//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

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

// carry (Task 18 review; plan 04 Task 21 lesson): the seed, fingerprint and health helpers are cmd/mcp's
// (cmd/mcp/main_integration_test.go), copied: the two composition roots are separate packages.

var seedAt = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

// seed writes one row into EVERY table cmd/api owns, through the write repositories (test-only wiring:
// the A2A binary has no write seam), so a write by the A2A binary to any table changes a fingerprint.
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
	t.Fatalf("cmd/a2a never became healthy at %s; last seen: %s", base, last)
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

// The REAL composition root end to end: run() boots on Postgres (migrations, pool, Reader, adapter, router), the
// SDK client built from the SERVED card (it advertises A2A_AGENT_URL) drives every skill over JSON-RPC, and
// afterwards every public table is byte-identical to before: the A2A binary writes nothing.
func TestTheA2ABinaryServesPostgresReadsAndWritesNothing(t *testing.T) {
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
		done <- run(ctx, io.Discard, env(map[string]string{"A2A_ADDR": addr, "DATABASE_URL": url, "AMR_MAP_FILE": exampleMap,
			"A2A_AGENT_URL": "http://" + addr + "/a2a", "SERVICE_VERSION": "it-1"}))
	}()
	waitHealthy(t, "http://"+addr)
	if v := servedCard(t, "http://"+addr).Version; v != "it-1" {
		t.Fatalf("the card version = %q, want SERVICE_VERSION it-1", v)
	}
	exerciseEverySkill(t, "http://"+addr)
	if after := fingerprints(t, url); !maps(before, after) {
		t.Fatalf("the A2A binary changed the database:\nbefore %v\nafter  %v", before, after)
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

// servedCard fetches the Agent Card the binary serves at the well-known path.
func servedCard(t *testing.T, base string) *a2a.AgentCard {
	t.Helper()
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(base + a2asrv.WellKnownAgentCardPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("card = %d %v", resp.StatusCode, err)
	}
	return &card
}

func exerciseEverySkill(t *testing.T, base string) {
	t.Helper()
	card := servedCard(t, base)
	if len(card.SupportedInterfaces) != 1 || card.SupportedInterfaces[0].URL != base+"/a2a" || len(card.Skills) != 4 {
		t.Fatalf("served card = %+v", card)
	}
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatal(err)
	}
	assertTheSkills(t, client)
	assertTheProblems(t, client)
}

// askSkill sends one skill request and returns the envelope.
func askSkill(t *testing.T, client *a2aclient.Client, skill string, args map[string]any) map[string]any {
	t.Helper()
	part := a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args})
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) != 2 {
		t.Fatalf("%s: reply = %T %+v", skill, resp, resp)
	}
	raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%s: envelope %s (%v)", skill, raw, err)
	}
	return env
}

func data(t *testing.T, env map[string]any, key string) map[string]any {
	t.Helper()
	d, _ := env["data"].(map[string]any)
	v, ok := d[key].(map[string]any)
	if !ok || env["problem"] != nil {
		t.Fatalf("envelope %v has no data.%s", env, key)
	}
	return v
}

func assertTheSkills(t *testing.T, client *a2aclient.Client) {
	t.Helper()
	if f := data(t, askSkill(t, client, "get_fleet", map[string]any{"fleetId": "AMR1"}), "fleet"); f["mapVersion"] != "2026-11-20.1" ||
		len(f["robots"].([]any)) != 1 {
		t.Fatalf("get_fleet = %v", f)
	}
	env := askSkill(t, client, "list_open_legs", map[string]any{"fleetId": "AMR1"})
	if d, _ := env["data"].(map[string]any); len(d["legs"].([]any)) != 1 {
		t.Fatalf("list_open_legs = %v", env)
	}
	if l := data(t, askSkill(t, client, "get_leg", map[string]any{"fleetId": "AMR1", "legId": "leg-1"}), "leg"); l["status"] != "QUEUED" ||
		l["containerId"] != "C1" {
		t.Fatalf("get_leg = %v", l)
	}
	if r := data(t, askSkill(t, client, "get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R1"}),
		"robot"); r["connection"] != "ONLINE" || r["nodeId"] != "P1" {
		t.Fatalf("get_robot = %v", r)
	}
}

func assertTheProblems(t *testing.T, client *a2aclient.Client) {
	t.Helper()
	for _, c := range []struct {
		skill string
		args  map[string]any
		code  string
	}{
		{"get_leg", map[string]any{"fleetId": "AMR1", "legId": "leg-9"}, "leg-not-found"},
		{"get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R9"}, "robot-not-found"},
		{"get_fleet", map[string]any{"fleetId": "AMR9"}, "fleet-not-found"},
		{"list_open_legs", map[string]any{"fleetId": "AMR9"}, "fleet-not-found"},
		{"get_robot", map[string]any{"fleetId": "AMR1"}, "invalid-argument"},
		{"request_leg", map[string]any{"fleetId": "AMR1"}, "unknown-skill"},
	} {
		p, _ := askSkill(t, client, c.skill, c.args)["problem"].(map[string]any)
		if p["code"] != c.code {
			t.Errorf("%s %v = %v, want %s", c.skill, c.args, p, c.code)
		}
	}
}
