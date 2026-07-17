package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func TestNewRefusesForeignDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "foreign.db")

	// A sqlite file with schema but without our application_id stamp
	foreign, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Exec(`CREATE TABLE somebody_elses_data (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := New(context.Background(), dbPath, AppDB, logger.NewNoopLogger()); err == nil {
		t.Fatal("New should refuse a non-wandersort database")
	} else if !strings.Contains(err.Error(), "not a wandersort database") {
		t.Fatalf("unexpected error: %v", err)
	}

	// The refused file must be left untouched — no stamp, no migrations
	foreign, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	var appID int32
	if err := foreign.QueryRow("PRAGMA application_id").Scan(&appID); err != nil {
		t.Fatal(err)
	}
	if appID != 0 {
		t.Errorf("refused database was stamped with application_id %d", appID)
	}
}

func TestNewFreshAndReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	d, err := New(context.Background(), dbPath, AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatalf("fresh open: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	// A database we stamped and migrated must reopen cleanly
	d, err = New(context.Background(), dbPath, AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d.Close()

	var appID int32
	if err := d.SQL.QueryRow("PRAGMA application_id").Scan(&appID); err != nil {
		t.Fatal(err)
	}
	if appID != appIDFromTag() {
		t.Errorf("application_id = %d, want %d", appID, appIDFromTag())
	}
}
