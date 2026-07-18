package store

import "testing"

func TestOpenAndMigrate(t *testing.T) {
	s, err := OpenAt(":memory:")
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	defer s.Close()

	v, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != len(migrations) {
		t.Errorf("schema version = %d, want %d", v, len(migrations))
	}
}

func TestMigrateIdempotent(t *testing.T) {
	// A file-backed DB reopened twice must not re-run migrations or error.
	path := t.TempDir() + "/test.db"
	s1, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenAt(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	v, _ := s2.SchemaVersion()
	if v != len(migrations) {
		t.Errorf("reopened schema version = %d, want %d", v, len(migrations))
	}
}

func TestTablesExist(t *testing.T) {
	s, err := OpenAt(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"tx_history", "contracts", "selectors"} {
		var name string
		err := s.DB().QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}
