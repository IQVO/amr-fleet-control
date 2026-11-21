//go:build integration

package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

// carry (Task 18 review): the read pool dials DATABASE_URL (the pooler in the cluster), never the direct
// MIGRATIONS_DATABASE_URL. Kills buildReads mutant NewPool(ctx, cfg.migrationsURL).
func TestTheReadPoolDialsDatabaseURLNotTheMigrationsDSN(t *testing.T) {
	url := pgtest.URL(t)
	seed(t, url)
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, io.Discard, env(map[string]string{"MCP_ADDR": addr, "AMR_MAP_FILE": exampleMap,
			"DATABASE_URL": url + sep + "application_name=mcp-reads", "MIGRATIONS_DATABASE_URL": url + sep + "application_name=mcp-migrate"}))
	}()
	defer func() { cancel(); <-done }()
	waitHealthy(t, "http://"+addr)
	exerciseEveryRead(t, "http://"+addr+"/mcp")
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && n == 0; time.Sleep(100 * time.Millisecond) {
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE application_name = 'mcp-reads'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
	if n == 0 {
		t.Fatal("no backend with application_name mcp-reads: the read pool did not dial DATABASE_URL")
	}
}
