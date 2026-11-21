package main

import (
	"context"
	"strings"
	"testing"
)

// carry (Task 18 review): the boot migrations run on MIGRATIONS_DATABASE_URL, never on DATABASE_URL (the pooler).
// Kills buildReads mutant RunMigrations(cfg.databaseURL).
func TestBuildReadsMigratesOnTheMigrationsDSN(t *testing.T) {
	_, err := buildReads(context.Background(), bootConfig{mapFile: exampleMap, databaseURL: deadDB, migrationsURL: "bogus://direct"})
	if err == nil || !strings.Contains(err.Error(), "migrations") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("buildReads = %v, want the migrations error of MIGRATIONS_DATABASE_URL (bogus://direct)", err)
	}
}
