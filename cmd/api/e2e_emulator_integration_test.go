//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IQVO/amr-fleet-control/internal/testing/kafkatest"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

// world is the emulator's side of config/map.example.yaml: the same robot start nodes and port nodeIds.
const world = `robots:
  - {manufacturer: EMU, serialNumber: R1, startX: 0, startY: 0, mapId: S1, speed: 1.5, drainPerMeter: 0.02, chargePerSecond: 0.5, actionSeconds: 2}
  - {manufacturer: EMU, serialNumber: R2, startX: 2, startY: 0, mapId: S1, speed: 1.5, drainPerMeter: 0.02, chargePerSecond: 0.5, actionSeconds: 2}
  - {manufacturer: EMU, serialNumber: R3, startX: 4, startY: 0, mapId: S1, speed: 1.5, drainPerMeter: 0.02, chargePerSecond: 0.5, actionSeconds: 2}
asrs:
  - id: ASRS1
    aisles: 2
    levels: 1
    slots: 2
    doubleDeep: true
    baseSeconds: 1
    perLevelSeconds: 0.2
    perSlotSeconds: 0.2
    ports:
      - {id: IN1, aisle: 1, kind: INPUT, nodeId: ASRS1-IN1}
      - {id: OUT1, aisle: 1, kind: OUTPUT, nodeId: ASRS1-OUT1}
      - {id: IN2, aisle: 2, kind: INPUT, nodeId: ASRS1-IN2}
      - {id: OUT2, aisle: 2, kind: OUTPUT, nodeId: ASRS1-OUT2}
`

const eventsTopic = "warehouse.amr-fleet-control.events"

// carry (Task 17 review): every call is bounded, so a hung request fails on its own line.
var client = &http.Client{Timeout: 10 * time.Second}

type e2e struct {
	t            *testing.T
	api, emu, mq string
	n            int
}

func stack(t *testing.T) *e2e {
	t.Helper()
	ctx := context.Background()
	image := os.Getenv("EQUIPMENT_EMULATOR_IMAGE")
	if image == "" {
		image = "equipment-emulator:e2e"
	}
	nw, err := network.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nw.Remove(ctx) })
	mq, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: testcontainers.ContainerRequest{
		Image: "eclipse-mosquitto:2.0.20", ExposedPorts: []string{"1883/tcp"}, Cmd: []string{"mosquitto", "-c", "/mosquitto-no-auth.conf"},
		Networks: []string{nw.Name}, NetworkAliases: map[string][]string{nw.Name: {"mosquitto"}}, WaitingFor: wait.ForListeningPort("1883/tcp")}})
	if err != nil {
		t.Fatalf("start mosquitto: %v", err)
	}
	t.Cleanup(func() { _ = mq.Terminate(ctx) })
	emu, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: testcontainers.ContainerRequest{
		Image: image, ExposedPorts: []string{"8080/tcp"}, Networks: []string{nw.Name},
		Env:        map[string]string{"MQTT_URL": "tcp://mosquitto:1883", "SIM_SPEED": "10", "WORLD_FILE": "/tmp/world.yaml"},
		Files:      []testcontainers.ContainerFile{{Reader: strings.NewReader(world), ContainerFilePath: "/tmp/world.yaml", FileMode: 0o644}},
		WaitingFor: wait.ForHTTP("/readyz").WithPort("8080/tcp").WithStartupTimeout(60 * time.Second)}})
	if err != nil {
		t.Fatalf("start the emulator image %q: %v. Build it first: docker build -t equipment-emulator:e2e <equipment-emulator checkout at v0.1.0 (b58201e)>", image, err)
	}
	t.Cleanup(func() { _ = emu.Terminate(ctx) })
	mqURL, err := mq.PortEndpoint(ctx, "1883/tcp", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	emuURL, err := emu.PortEndpoint(ctx, "8080/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	e := &e2e{t: t, emu: strings.Replace(emuURL, "localhost", "127.0.0.1", 1), mq: strings.Replace(mqURL, "localhost", "127.0.0.1", 1)}
	e.api = startAmrFleetControl(t, e.mq)
	return e
}

func startAmrFleetControl(t *testing.T, mqURL string) string {
	t.Helper()
	db := pgtest.URL(t)
	brokers := kafkatest.Brokers(t)
	kafkatest.CreateTopic(t, eventsTopic)
	kafkatest.CreateTopic(t, "warehouse.amr-fleet-control.analytics")
	vars := map[string]string{"KAFKA_BROKERS": strings.Join(brokers, ","), "MQTT_URL": mqURL, "AMR_MAP_FILE": "../../config/map.example.yaml",
		"OUTBOX_INTERVAL_MS": "100", "ROBOT_BLOCKED_TIMEOUT_SECONDS": "5", "MQTT_CLIENT_ID": "amr-fleet-control-e2e"}
	cfg, err := loadConfig(func(k string) string { return vars[k] })
	if err == nil {
		t.Fatal("loadConfig accepted a configuration without a database")
	}
	cfg.DatabaseURL, cfg.MigrationsURL = db, db
	ctx, cancel := context.WithCancel(context.Background())
	a, err := wire(ctx, cfg, slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.run(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-done; a.close() })
	return "http://" + ln.Addr().String()
}

func (e *e2e) call(method, url, body string) (int, map[string]any) {
	e.t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if method == http.MethodPost && strings.HasPrefix(url, e.api) {
		e.n++
		req.Header.Set("Idempotency-Key", "e2e-"+strconv.Itoa(e.n))
	}
	res, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return res.StatusCode, m
}

// carry (Task 17 review): on timeout, eventually prints what the wait last observed.
func (e *e2e) eventually(what string, timeout time.Duration, ok func() bool, seen ...func() any) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	var last any
	if len(seen) > 0 {
		last = seen[0]()
	}
	e.t.Fatalf("%s did not happen within %s; last seen: %v", what, timeout, last)
}

// leg requests a leg and returns its id and the 202 answer (status ASSIGNED with the robot when one was free).
func (e *e2e) leg(container, from, to string) (string, map[string]any) {
	e.t.Helper()
	code, m := e.call(http.MethodPost, e.api+"/fleets/AMR1/legs",
		`{"container_id":"`+container+`","from_handoff_point_id":"`+from+`","to_handoff_point_id":"`+to+`","reference":"e2e"}`)
	if code != http.StatusAccepted {
		e.t.Fatalf("leg %s = %d %v", container, code, m)
	}
	// carry (Task 17 review): comma-ok, so a malformed answer fails instead of panicking.
	id, ok := m["leg_id"].(string)
	if !ok {
		e.t.Fatalf("leg %s: the 202 answer has no leg_id: %v", container, m)
	}
	return id, m
}

func (e *e2e) waitLeg(id, status string, timeout time.Duration) map[string]any {
	e.t.Helper()
	var last map[string]any
	e.eventually("leg "+id+" "+status, timeout, func() bool {
		_, last = e.call(http.MethodGet, e.api+"/fleets/AMR1/legs/"+id, "")
		return last["status"] == status
	}, func() any { return last })
	return last
}

func (e *e2e) fault(kind, serial string, active bool, value float64) {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"kind": kind, "target": serial, "active": active, "value": value})
	if code, m := e.call(http.MethodPost, e.emu+"/faults", string(body)); code != http.StatusNoContent {
		e.t.Fatalf("fault %s %s = %d %v", kind, serial, code, m)
	}
}

// retrieveToPort brings a container to an OUTPUT port the way it really gets there: seeded into
// the rack (admin API), then a RETRIEVE telegram on the AS/RS PLC topic (what asrs-control sends).
// deviation (Task 17): the brief placed it with POST /asrs/{asrsId}/ports/{portId}/container, which
// the emulator accepts on INPUT ports only (asrs.System.PlaceAtPort: 409 WRONG_PORT_KIND on OUT2).
func (e *e2e) retrieveToPort(container, port string, aisle int) {
	e.t.Helper()
	seed := `{"containerId":"` + container + `","position":{"aisle":` + strconv.Itoa(aisle) + `,"level":1,"slot":1,"depth":1}}`
	if code, m := e.call(http.MethodPost, e.emu+"/asrs/ASRS1/seeds", seed); code != http.StatusNoContent {
		e.t.Fatalf("seed %s = %d %v", container, code, m)
	}
	c := pahomqtt.NewClient(pahomqtt.NewClientOptions().AddBroker(e.mq).SetClientID("e2e-plc-" + container))
	if tok := c.Connect(); !tok.WaitTimeout(10*time.Second) || tok.Error() != nil {
		e.t.Fatalf("connect the test PLC client: %v", tok.Error())
	}
	defer c.Disconnect(250)
	telegram, _ := json.Marshal(map[string]any{"telegramId": "e2e-retrieve-" + container, "type": "RETRIEVE",
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "jobId": "e2e-retrieve-" + container,
		"containerId": container, "portId": port, "from": map[string]int{"aisle": aisle, "level": 1, "slot": 1, "depth": 1}})
	if tok := c.Publish("asrs/v1/ASRS1/down", 1, false, telegram); !tok.WaitTimeout(10*time.Second) || tok.Error() != nil {
		e.t.Fatalf("publish the RETRIEVE of %s: %v", container, tok.Error())
	}
	var last map[string]any
	e.eventually(container+" at "+port, 30*time.Second, func() bool {
		_, last = e.call(http.MethodGet, e.emu+"/asrs/ASRS1", "")
		ports, _ := last["ports"].(map[string]any)
		return ports[port] == container
	}, func() any { return last })
}

// published returns the decoded CloudEvents of the integration topic once until holds.
func published(t *testing.T, until func([]map[string]any) bool) []map[string]any {
	t.Helper()
	var out []map[string]any
	kafkatest.ReadAll(t, eventsTopic, func(ms []kafkatest.Message) bool {
		out = out[:0]
		for _, m := range ms {
			var ev map[string]any
			_ = json.Unmarshal(m.Value, &ev)
			out = append(out, ev)
		}
		return until(out)
	}, 90*time.Second)
	return out
}

func ofLeg(evs []map[string]any, legID, name string) []map[string]any {
	var out []map[string]any
	for _, ev := range evs {
		typ, _ := ev["type"].(string)
		if ev["subject"] == legID && strings.HasSuffix(typ, ".mission."+name) {
			out = append(out, ev)
		}
	}
	return out
}

func TestLegsChargingAndFaultsAgainstTheEmulatorRobots(t *testing.T) {
	e := stack(t)
	e.waitCapacity("three robots online", func(c map[string]any) bool {
		return c["robots_online"] == 3.0 && c["robots_available"] == 3.0
	})

	// 1. A station-to-port leg: the nearest robot (R1 at P1) picks at ST1 and drops at the AS/RS input port.
	legA, _ := e.leg("C-A", "ST1", "ASRS1-IN1")
	done := e.waitLeg(legA, "DONE", 60*time.Second)
	if r, ok := done["robot"].(map[string]any); !ok || r["serial_number"] != "R1" {
		t.Fatalf("leg A robot = %v (R1 at P1 is nearest to ST1); leg %v", done["robot"], done)
	}
	_, asrs := e.call(http.MethodGet, e.emu+"/asrs/ASRS1", "")
	if b, _ := json.Marshal(asrs); !strings.Contains(string(b), "C-A") {
		t.Fatalf("the emulator does not hold C-A at ASRS1 after the drop: %s", b)
	}

	// 2. A pick from an empty output port fails PICK_FAILED with the robot's LOAD_NOT_AVAILABLE ...
	legB, _ := e.leg("C-B", "ASRS1-OUT2", "ST2")
	failed := e.waitLeg(legB, "FAILED", 60*time.Second)
	if failed["reason"] != "PICK_FAILED" || failed["robot_error"] != "LOAD_NOT_AVAILABLE" {
		t.Fatalf("leg B = %v", failed)
	}
	// ... and the same pick succeeds once the container is at the port.
	e.retrieveToPort("C-C", "OUT2", 2)
	legC, _ := e.leg("C-C", "ASRS1-OUT2", "ST2")
	e.waitLeg(legC, "DONE", 90*time.Second)
	// carry (Task 17 review): the emulator's own view - the pick took C-C off OUT2.
	_, asrs = e.call(http.MethodGet, e.emu+"/asrs/ASRS1", "")
	if ports, ok := asrs["ports"].(map[string]any); !ok || ports["OUT2"] != "" {
		t.Fatalf("the emulator's ports after leg C = %v, want OUT2 empty", asrs["ports"])
	}

	// 3. A blocked robot fails its leg ROBOT_BLOCKED after ROBOT_BLOCKED_TIMEOUT (5 s here) and is cancelled.
	legD := e.blockedLeg()

	// 4. A robot below the charge threshold drives to the charger and is not available while charging.
	e.fault("ROBOT_BATTERY", "R3", true, 15)
	e.waitRobot("R3", 90*time.Second, func(r map[string]any) bool {
		return r["state"] == "SUSPENDED" && r["node_id"] == "CH1" && r["available"] == false
	})

	// 5. A disconnected robot leaves the online count.
	e.fault("ROBOT_DISCONNECT", "R2", true, 0)
	e.waitRobot("R2", 30*time.Second, func(r map[string]any) bool { return r["state"] == "STOPPED" })
	// carry (Task 17 review): ... and the fleet capacity counts it out.
	e.waitCapacity("robots_online 2 after the disconnect", func(c map[string]any) bool { return c["robots_online"] == 2.0 })

	evs := published(t, func(evs []map[string]any) bool {
		return len(ofLeg(evs, legC, "LegCompleted")) == 1 && len(ofLeg(evs, legD, "LegFailed")) == 1
	})
	assertOutcomes(t, evs, legA, legB, legC, legD)
	assertPayloads(t, evs, legA, legB, legD)
}

// carry (Task 17 review): the helpers below are split out of the test body (cyclop) so the
// added waits and payload checks keep every step's assertion; none was dropped.

// blockedLeg starts leg D, blocks its robot mid-leg and waits for the ROBOT_BLOCKED failure.
// The 202 already names the robot (one is idle), so the fault lands while the leg runs.
// ST1 -> ST2 is the map's longest station leg (the approach to ST1 plus ST1-E-F-G-H-ST2), so the
// fault lands mid-leg rather than racing the sub-second ST2 -> ST1 leg the robot of leg C would get.
func (e *e2e) blockedLeg() string {
	e.t.Helper()
	legD, accepted := e.leg("C-D", "ST1", "ST2")
	robotD, ok := accepted["robot"].(map[string]any)
	if accepted["status"] != "ASSIGNED" || !ok {
		e.t.Fatalf("leg D was not assigned at once: %v", accepted)
	}
	serial, ok := robotD["serial_number"].(string)
	if !ok {
		e.t.Fatalf("leg D robot has no serial_number: %v", robotD)
	}
	e.fault("ROBOT_BLOCKED", serial, true, 0)
	blocked := e.waitLeg(legD, "FAILED", 60*time.Second)
	if blocked["reason"] != "ROBOT_BLOCKED" || blocked["robot_error"] != "ROBOT_BLOCKED" {
		e.t.Fatalf("leg D = %v", blocked)
	}
	e.fault("ROBOT_BLOCKED", serial, false, 0)
	return legD
}

func (e *e2e) waitRobot(serial string, timeout time.Duration, ok func(map[string]any) bool) {
	e.t.Helper()
	var last map[string]any
	e.eventually(serial+" state", timeout, func() bool {
		_, last = e.call(http.MethodGet, e.api+"/fleets/AMR1/robots/EMU/"+serial, "")
		return ok(last)
	}, func() any { return last })
}

func (e *e2e) waitCapacity(what string, ok func(map[string]any) bool) {
	e.t.Helper()
	var last map[string]any
	e.eventually(what, 30*time.Second, func() bool {
		_, last = e.call(http.MethodGet, e.api+"/fleets/AMR1", "")
		c, _ := last["capacity"].(map[string]any)
		return c != nil && ok(c)
	}, func() any { return last })
}

func assertOutcomes(t *testing.T, evs []map[string]any, legA, legB, legC, legD string) {
	t.Helper()
	for _, leg := range []string{legA, legB, legC, legD} {
		if n := len(ofLeg(evs, leg, "LegCompleted")) + len(ofLeg(evs, leg, "LegFailed")); n != 1 {
			t.Fatalf("leg %s has %d outcomes, want exactly one (responsibility 6)", leg, n)
		}
		if n := len(ofLeg(evs, leg, "LegAssigned")); n != 1 {
			t.Fatalf("leg %s has %d LegAssigned", leg, n)
		}
	}
}

func assertPayloads(t *testing.T, evs []map[string]any, legA, legB, legD string) {
	t.Helper()
	for _, name := range []string{"ContainerPickedUp", "LegCompleted"} {
		// carry (Task 17 review): count before indexing, comma-ok on the data map.
		got := ofLeg(evs, legA, name)
		if len(got) != 1 {
			t.Fatalf("leg A has %d %s; events %v", len(got), name, evs)
		}
		ev := got[0]
		data, ok := ev["data"].(map[string]any)
		if !ok || data["robot_timestamp"] == nil || data["fleet_id"] != "AMR1" || ev["source"] != "/warehouse/amr-fleet-control" {
			t.Fatalf("%s of leg A = %v", name, ev)
		}
	}
	// carry (Task 17 review): the LegFailed payload plan 04 consumes carries the reason and the robot's error.
	for leg, want := range map[string][2]string{legB: {"PICK_FAILED", "LOAD_NOT_AVAILABLE"}, legD: {"ROBOT_BLOCKED", "ROBOT_BLOCKED"}} {
		got := ofLeg(evs, leg, "LegFailed")
		if len(got) != 1 {
			t.Fatalf("leg %s has %d LegFailed; events %v", leg, len(got), evs)
		}
		data, ok := got[0]["data"].(map[string]any)
		if !ok || data["reason"] != want[0] || data["robot_error"] != want[1] {
			t.Fatalf("LegFailed of %s = %v, want reason %s robot_error %s", leg, got[0], want[0], want[1])
		}
	}
	if n := len(ofLeg(evs, legB, "ContainerPickedUp")); n != 0 {
		t.Fatalf("leg B picked nothing, got %d ContainerPickedUp; events %v", n, evs)
	}
}
