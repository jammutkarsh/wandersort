package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

// pinned is the hash of every migration's queries. A library that ran one
// never runs it again, so a change to it reaches new libraries only.
var pinned = map[uint]string{
	1: "3db298b62f4eaa32491cfa99838bd1d96af99821bfa138a97dd726f84aaa1d48",
	2: "b9c6747de26e587363e07b249cea74ae61b9fb51b935367504cc3e18636a724b",
	3: "b389a5f8d88dce261e3fdfe4afdc36974d73378290340ab95c98e504e24933d4",
	4: "8ffa636eb1d1f765b1b559b0944a89fc25e173004c0bcceee2089677f5a90ed4",
}

func TestMigrationsNeverChange(t *testing.T) {
	for _, m := range schemas {
		queries := strings.Join(m.SQL, "\x00")
		if m.Unless != "" {
			queries = m.Unless + "\x00" + queries
		}
		sum := sha256.Sum256([]byte(queries))
		got := hex.EncodeToString(sum[:])
		switch want, ok := pinned[m.Version]; {
		case !ok:
			t.Errorf("migration %d is not pinned: add its hash, %s", m.Version, got)
		case got != want:
			t.Errorf("migration %d changed (%s, want %s): libraries that ran it won't run it again; add a new migration instead", m.Version, got, want)
		}
	}
}

// openAtVersion is a database migrated to version, as an older release left it.
func openAtVersion(t *testing.T, version uint) *sqlx.DB {
	t.Helper()
	db := openTestDB(t)
	db.SetMaxOpenConns(1) // :memory: is per connection
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	all := schemas
	swapSchemas(t, all[:version])
	if _, err := Run(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	schemas = all
	return db
}

const seedPublished = `
INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at, volume_uuid,
	discovered_at, last_seen_at, last_seen_scan, media_type, file_extension, placed) VALUES
	(1, '/card', 'a.jpg', 10, '2024-06-03T10:00:00.000000000', 'VOL', 'd', 's', 3, 'IMAGE', '.jpg', 0),
	(2, '/card', 'b.jpg', 20, '2024-06-03T10:00:00.000000000', NULL,  'd', 's', 3, 'IMAGE', '.jpg', 0),
	(3, '2024/06_June/03', 'c.jpg', 30, '2024-06-03T10:00:00.000000000', 'VOL', 'd', 's', 3, 'IMAGE', '.jpg', 1);
INSERT INTO errors (file_id, stage, op, kind, detail, first_seen_at, last_seen_at) VALUES
	(1, 'READ', 'open', 'other', '{}', 'f', 'l'), (3, 'VERIFY', 'hash', 'other', '{}', 'f', 'l');
`

type columnInfo struct {
	Name    string  `db:"name"`
	Type    string  `db:"type"`
	NotNull bool    `db:"notnull"`
	Default *string `db:"dflt_value"`
}

// registrySchema is file_registry's columns, whatever their order, and its indexes.
func registrySchema(t *testing.T, db *sqlx.DB) (columns, indexes []string) {
	t.Helper()
	var infos []columnInfo
	if err := db.Select(&infos, `SELECT name, type, "notnull", dflt_value FROM pragma_table_info('file_registry')`); err != nil {
		t.Fatal(err)
	}
	for _, c := range infos {
		def := "-"
		if c.Default != nil {
			def = *c.Default
		}
		null := "null"
		if c.NotNull {
			null = "not null"
		}
		columns = append(columns, strings.Join([]string{c.Name, c.Type, null, def}, " "))
	}
	slices.Sort(columns)
	if err := db.Select(&indexes, `SELECT l.name || ' ' || l."unique" || ' ' || group_concat(i.name)
		FROM pragma_index_list('file_registry') l, pragma_index_info(l.name) i GROUP BY l.name ORDER BY l.name`); err != nil {
		t.Fatal(err)
	}
	return columns, indexes
}

// A library made by a published release upgrades in place: every row and its
// dependants stay, and its schema is a new library's.
func TestPublishedLibraryUpgrades(t *testing.T) {
	ctx := context.Background()
	db := openAtVersion(t, 3)
	if _, err := db.Exec(seedPublished); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, db); err != nil {
		t.Fatal(err)
	}

	var rows []struct {
		ID      int64  `db:"id"`
		Dir     string `db:"file_dir"`
		Volume  string `db:"volume_uuid"`
		MtimeNS int64  `db:"file_mtime_ns"`
		Placed  bool   `db:"placed"`
	}
	if err := db.Select(&rows, `SELECT id, file_dir, volume_uuid, file_mtime_ns, placed FROM file_registry ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	// a library path names no source volume
	for i, want := range []string{"VOL", "", ""} {
		if len(rows) != 3 || rows[i].Volume != want || rows[i].MtimeNS != 0 {
			t.Fatalf("rows after the upgrade = %+v, want volumes VOL, '' and '' and no instants", rows)
		}
	}
	var dependants int
	if err := db.Get(&dependants, `SELECT count(*) FROM errors`); err != nil || dependants != 2 {
		t.Errorf("error rows after the upgrade = %d (%v), want 2", dependants, err)
	}
	var damaged int
	if err := db.Get(&damaged, `SELECT count(*) FROM pragma_foreign_key_check`); err != nil || damaged != 0 {
		t.Errorf("foreign key check found %d (%v)", damaged, err)
	}

	fresh := openAtVersion(t, uint(len(schemas)))
	gotColumns, gotIndexes := registrySchema(t, db)
	wantColumns, wantIndexes := registrySchema(t, fresh)
	if !slices.Equal(gotColumns, wantColumns) || !slices.Equal(gotIndexes, wantIndexes) {
		t.Errorf("upgraded schema = %v %v, want a new library's %v %v", gotColumns, gotIndexes, wantColumns, wantIndexes)
	}
	// two cards mounted in turn at one path hold different files
	if _, err := db.Exec(`INSERT INTO file_registry (file_dir, file_name, file_size, file_modified_at, volume_uuid,
		discovered_at, last_seen_at, media_type, file_extension) VALUES ('/card', 'a.jpg', 1, 'm', 'OTHER', 'd', 's', 'IMAGE', '.jpg')`); err != nil {
		t.Errorf("another card's file at the same path: %v", err)
	}
}

// A library made between releases already has version 4's columns under
// version 3: it is left as it is.
func TestLibraryMadeBetweenReleasesKeepsItsColumns(t *testing.T) {
	ctx := context.Background()
	db := openAtVersion(t, uint(len(schemas)))
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 4`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO file_registry (file_dir, file_name, file_size, file_modified_at, file_mtime_ns, volume_uuid,
		discovered_at, last_seen_at, media_type, file_extension) VALUES ('/card', 'a.jpg', 1, 'm', 42, 'VOL', 'd', 's', 'IMAGE', '.jpg')`); err != nil {
		t.Fatal(err)
	}
	if n, err := Run(ctx, db); err != nil || n != 1 {
		t.Fatalf("Run = %d, %v, want version 4 recorded", n, err)
	}
	var mtime int64
	var volume string
	if err := db.QueryRow(`SELECT file_mtime_ns, volume_uuid FROM file_registry`).Scan(&mtime, &volume); err != nil || mtime != 42 || volume != "VOL" {
		t.Errorf("row = %d %q (%v), want 42 VOL", mtime, volume, err)
	}
	if n, err := Run(ctx, db); err != nil || n != 0 {
		t.Errorf("second Run = %d, %v, want nothing left", n, err)
	}
}
