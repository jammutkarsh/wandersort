// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	sqlite "modernc.org/sqlite"
)

// BackupFileName is the one backup kept beside the library database (spec
// D24), overwritten by every execute run and every `reset --db`.
const BackupFileName = ".wandersort.db.bak"

// backupTable is the stamp Backup writes into the copy and Restore drops.
const backupTable = "wandersort_backup"

// ErrInUse means another connection — another program, or a sqlite browser —
// has the database open, so it cannot be replaced safely.
var ErrInUse = errors.New("the database is open in another program")

// Backup writes a consistent copy of the database to dest via VACUUM INTO.
// The copy is built beside dest and only renamed over it once it has been
// verified, so a failure at any point leaves the previous backup intact.
//
// The copy is stamped with a wandersort_backup row, so it never holds the
// same bytes as the live database: a duplicate finder or cleanup tool would
// otherwise pair the two and offer to delete one — possibly the live one. If
// the sizes match when it is taken, it is padded by a page, since most such
// tools group by size before hashing. That part is best effort: the live file
// keeps changing afterwards. The differing hash is the guarantee.
func (d *DB) Backup(ctx context.Context, dest string) error {
	// Writes still queued in the async writer belong to the state being
	// backed up; without this the backup could miss the last edits.
	if d.Writer != nil {
		d.Writer.Flush()
	}
	tmp := dest + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove leftover backup: %w", err)
	}
	defer os.Remove(tmp) // no-op once renamed
	if _, err := d.SQL.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return fmt.Errorf("vacuum into %s: %w", tmp, err)
	}

	var live string
	if err := d.SQL.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&live); err != nil {
		return fmt.Errorf("locate live database: %w", err)
	}
	if err := stampBackup(ctx, tmp, live); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("replace backup: %w", err)
	}
	return nil
}

func stampBackup(ctx context.Context, path, live string) error {
	b, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer b.Close()
	for _, q := range []string{
		`PRAGMA journal_mode=DELETE`, // no -wal sidecar left next to the backup
		// A database restored from an older backup may still carry one.
		`DROP TABLE IF EXISTS ` + backupTable,
		`CREATE TABLE ` + backupTable + ` (taken_at TEXT NOT NULL, pad BLOB)`,
	} {
		if _, err := b.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("stamp backup: %w", err)
		}
	}
	if _, err := b.ExecContext(ctx, `INSERT INTO `+backupTable+` (taken_at) VALUES (?)`, FormatTime(time.Now())); err != nil {
		return fmt.Errorf("stamp backup: %w", err)
	}

	liveInfo, err := os.Stat(live)
	if err != nil {
		return fmt.Errorf("stat live database: %w", err)
	}
	bakInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat backup: %w", err)
	}
	if liveInfo.Size() == bakInfo.Size() {
		if _, err := b.ExecContext(ctx,
			`UPDATE `+backupTable+` SET pad = zeroblob((SELECT page_size FROM pragma_page_size))`); err != nil {
			return fmt.Errorf("pad backup: %w", err)
		}
	}
	if err := checkBackup(ctx, b); err != nil {
		return err
	}
	return b.Close()
}

// checkBackup proves a backup is worth trusting: ours, and intact.
func checkBackup(ctx context.Context, b *sql.DB) error {
	var id int32
	if err := b.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&id); err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	if id != appIDFromTag() {
		return fmt.Errorf("backup is not a wandersort database (application_id %d)", id)
	}
	var check string
	if err := b.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check); err != nil {
		return fmt.Errorf("check backup: %w", err)
	}
	if check != "ok" {
		return fmt.Errorf("backup is damaged: %s", check)
	}
	return nil
}

// Restore replaces the database at live with the backup at backup, which is
// left in place so a second restore is possible. Nothing is touched unless
// the backup checks out and no other connection has the live database open
// (ErrInUse otherwise); the caller holds the output lock, which keeps other
// wandersort processes out, and this keeps everything else out.
//
// It never swaps files: SQLite's online backup writes the pages through its
// own rollback journal, so a crash mid-restore rolls back to the old database
// on the next open, and there is no -wal file to get wrong.
func Restore(ctx context.Context, backup, live string) error {
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("no backup: %w", err)
	}
	src, err := sql.Open("sqlite", backup)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	err = checkBackup(ctx, src)
	src.Close()
	if err != nil {
		return err
	}

	dbh, err := sql.Open("sqlite", live)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer dbh.Close()
	c, err := dbh.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer c.Close()

	// Leaving WAL needs every other connection gone, even an idle one, and
	// EXCLUSIVE keeps the lock that took until this connection closes, so no
	// one can open the database mid-restore either.
	if _, err := c.ExecContext(ctx, `PRAGMA locking_mode=EXCLUSIVE`); err != nil {
		return fmt.Errorf("lock database: %w", err)
	}
	var mode string
	if err := c.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		var se *sqlite.Error
		if errors.As(err, &se) && se.Code()&0xff == 5 { // SQLITE_BUSY
			return ErrInUse
		}
		return fmt.Errorf("database %s is unreadable (%w); rename it aside and restore again", live, err)
	}
	if mode != "delete" {
		return ErrInUse
	}

	err = c.Raw(func(dc any) error {
		r, err := dc.(interface {
			NewRestore(string) (*sqlite.Backup, error)
		}).NewRestore(backup)
		if err != nil {
			return err
		}
		if _, err := r.Step(-1); err != nil {
			r.Finish()
			return err
		}
		return r.Finish()
	})
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	if _, err := c.ExecContext(ctx, `DROP TABLE IF EXISTS `+backupTable); err != nil {
		return fmt.Errorf("unstamp restored database: %w", err)
	}
	return nil
}
