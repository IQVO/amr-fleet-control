//go:build integration

// Package mqtttest starts one Mosquitto broker per call (integration tests only).
package mqtttest

import (
	"context"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Broker returns tcp://127.0.0.1:<port> of a fresh eclipse-mosquitto:2.0.20 (an IP literal:
// an undeadlined localhost lookup is how plan 02's relay once hung).
func Broker(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true,
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "eclipse-mosquitto:2.0.20", ExposedPorts: []string{"1883/tcp"},
			Cmd:        []string{"mosquitto", "-c", "/mosquitto-no-auth.conf"},
			WaitingFor: wait.ForListeningPort("1883/tcp"),
		}})
	if err != nil {
		t.Fatalf("start mosquitto: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	ep, err := c.PortEndpoint(ctx, "1883/tcp", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(ep, "localhost", "127.0.0.1", 1)
}
