package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

// Forget takes more ids than SQLite binds in one statement.
func TestForgetBeyondTheVariableLimit(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	const n = 40_000
	if _, err := d.SQL.ExecContext(ctx, `
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

	if err := d.Forget(ctx, ids); err != nil {
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

// A file is changed by its size, or by its modification time moving both as an
// instant and as wall clock. A time zone change moves only one: the wall clock
// on APFS/NTFS, the instant on FAT/exFAT (which store wall clock).
func TestForgetChanged(t *testing.T) {
	home := time.FixedZone("home", 5*3600)
	away := time.FixedZone("away", -3*3600)
	stored := time.Date(2024, 6, 1, 10, 0, 0, 0, home)
	tests := []struct {
		name    string
		size    int64
		mod     time.Time
		force   bool
		forgets bool
	}{
		{"unchanged", 5, stored, false, false},
		{"zone change, instant kept (APFS)", 5, stored.In(away), false, false},
		{"zone change, wall clock kept (FAT)", 5, time.Date(2024, 6, 1, 10, 0, 0, 0, away), false, false},
		{"edited", 5, stored.Add(time.Hour), false, true},
		{"resized", 6, stored, false, true},
		{"forced", 5, stored, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			d := dbtest.New(t)
			if _, err := d.SQL.ExecContext(ctx, `
				INSERT INTO file_registry (file_dir, file_name, file_size, file_modified_at, file_mtime_ns,
					file_extension, discovered_at, last_seen_at)
				VALUES ('/src', 'a.jpg', 5, ?, ?, '.jpg', '', '')`,
				db.FormatTime(stored), stored.UnixNano()); err != nil {
				t.Fatal(err)
			}
			if err := d.Writer.WriteSync(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
				return db.ForgetChanged(ctx, tx, "/src", "a.jpg", tt.size, tt.mod, tt.force)
			}); err != nil {
				t.Fatal(err)
			}
			var left int
			if err := d.SQL.GetContext(ctx, &left, `SELECT count(*) FROM file_registry`); err != nil {
				t.Fatal(err)
			}
			if forgot := left == 0; forgot != tt.forgets {
				t.Errorf("forgot = %v, want %v", forgot, tt.forgets)
			}
		})
	}
}
