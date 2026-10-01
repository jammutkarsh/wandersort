package db_test

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

// Forget takes more ids than SQLite binds in one statement.
func TestForgetBeyondTheVariableLimit(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	const n = 40_000
	if _, err := d.ExecContext(ctx, `
		WITH RECURSIVE ids(id) AS (SELECT 1 UNION ALL SELECT id + 1 FROM ids WHERE id < ?)
		INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at,
			file_extension, media_type, discovered_at, last_seen_at)
		SELECT id, '/src', 'f' || id || '.jpg', 1, '2024-01-01T00:00:00.000000000', '.jpg', 'IMAGE',
			'2024-01-01T00:00:00.000000000', '2024-01-01T00:00:00.000000000' FROM ids`, n); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = int64(i + 1)
	}

	tx, err := d.SQL.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Forget(ctx, tx, ids); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var left int
	if err := d.SQL.GetContext(ctx, &left, `SELECT count(*) FROM file_registry`); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d rows left, want 0", left)
	}
}
