package coordinator

import (
	"strings"
	"testing"

	"github.com/ambient/platform/components/boss/internal/coordinator/db"
)

// TestPostgresNoDSN verifies that DB_TYPE=postgres without DB_DSN returns
// a descriptive error immediately (no network call is made).
func TestPostgresNoDSN(t *testing.T) {
	t.Setenv("DB_TYPE", "postgres")
	t.Setenv("DB_DSN", "") // explicitly unset

	dir := t.TempDir()
	s := NewServer(":0", dir)
	err := s.Start()
	if err == nil {
		s.Stop()
		t.Fatal("expected error for DB_TYPE=postgres without DB_DSN, got nil")
	}
	if !strings.Contains(err.Error(), "DB_DSN") {
		t.Errorf("expected error to mention DB_DSN, got: %v", err)
	}
}

// TestPostgresInvalidDSN verifies that an invalid/unreachable postgres DSN
// returns an error (either during open or during AutoMigrate).
func TestPostgresInvalidDSN(t *testing.T) {
	t.Setenv("DB_TYPE", "postgres")
	t.Setenv("DB_DSN", "host=127.0.0.1 port=5 user=nobody dbname=nodb sslmode=disable connect_timeout=1")

	dir := t.TempDir()
	s := NewServer(":0", dir)
	err := s.Start()
	if err == nil {
		s.Stop()
		t.Fatal("expected error for invalid postgres DSN, got nil")
	}
	// The error should come from the db package (open or migrate).
	t.Logf("postgres error (expected): %v", err)
}

// TestPostgresOpenDirectly tests db.Open directly with DB_TYPE=postgres and no DSN.
// This isolates the db package from the server-level error handling.
func TestPostgresOpenDirectly(t *testing.T) {
	t.Setenv("DB_TYPE", "postgres")
	t.Setenv("DB_DSN", "")

	_, err := db.Open(t.TempDir())
	if err == nil {
		t.Fatal("db.Open: expected error for postgres without DSN, got nil")
	}
	if !strings.Contains(err.Error(), "DB_DSN") {
		t.Errorf("expected error mentioning DB_DSN, got: %v", err)
	}
}

// TestPostgresUnsupportedDBType verifies that an unknown DB_TYPE returns
// a clear error (not specific to postgres, but validates the same code path).
func TestPostgresUnsupportedDBType(t *testing.T) {
	t.Setenv("DB_TYPE", "cockroachdb")

	_, err := db.Open(t.TempDir())
	if err == nil {
		t.Fatal("db.Open: expected error for unsupported DB_TYPE, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected error mentioning 'unsupported', got: %v", err)
	}
}
