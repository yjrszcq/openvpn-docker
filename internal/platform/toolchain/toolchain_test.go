// Package toolchain contains build probes for required native dependencies.
package toolchain_test

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteCGO(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close in-memory SQLite database: %v", err)
		}
	})

	var version string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatalf("query SQLite version: %v", err)
	}
	if version == "" {
		t.Fatal("SQLite returned an empty version")
	}
}
