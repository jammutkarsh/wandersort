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
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
	sqlite "modernc.org/sqlite"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
)

// BackupFileName is the one backup kept beside the library database (spec
// D24), overwritten by every execute run and every `reset --db`: the database
// zstd-compressed, named for what is inside and what compressed it.
const BackupFileName = ".wandersort.db.zst"

// ErrInUse means another connection — another program, or a sqlite browser —
// has the database open, so it cannot be replaced safely.
var ErrInUse = errors.New("the database is open in another program")

// Backup writes a consistent, zstd-compressed copy of the database to dest via
// VACUUM INTO. The copy is verified while still plain SQLite (a compressed file
// cannot be checked) and only then compressed beside dest and renamed over it,
// so a failure at any point leaves the previous backup intact. A compressed
// stream never holds the same bytes as the live database (zstd magic vs the
// SQLite header), and in practice not the same size.
func (d *DB) Backup(ctx context.Context, dest string) error {
	// Writes still queued in the async writer belong to the state being
	// backed up; without this the backup could miss the last edits.
	if d.Writer != nil {
		d.Writer.Flush()
	}
	plain, tmp := dest+".db.tmp", dest+".tmp"
	for _, f := range []string{plain, tmp} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove leftover backup: %w", err)
		}
		defer os.Remove(f) // no-op once renamed
	}
	if _, err := d.SQL.ExecContext(ctx, `VACUUM INTO ?`, plain); err != nil {
		return fmt.Errorf("vacuum into %s: %w", plain, err)
	}
	if err := verifyPlain(ctx, plain); err != nil {
		return err
	}
	if err := compressFile(plain, tmp); err != nil {
		return err
	}
	// compressFile synced the new file's bytes; this makes the rename itself
	// durable. Without both, a power cut could leave an empty or partial file
	// under the backup's name — replacing the good one exactly when it is
	// needed.
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("replace backup: %w", err)
	}
	if err := atomicfile.SyncDir(filepath.Dir(dest)); err != nil {
		return fmt.Errorf("replace backup: %w", err)
	}
	return nil
}

// verifyPlain opens the uncompressed copy and runs checkBackup on it.
func verifyPlain(ctx context.Context, path string) error {
	b, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer b.Close()
	// No -wal sidecar left next to the copy.
	if _, err := b.ExecContext(ctx, `PRAGMA journal_mode=DELETE`); err != nil {
		return fmt.Errorf("check backup: %w", err)
	}
	return checkBackup(ctx, b)
}

func compressFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("compress backup: %w", err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("compress backup: %w", err)
	}
	defer func() {
		if cerr := out.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("compress backup: %w", cerr)
		}
	}()
	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return fmt.Errorf("compress backup: %w", err)
	}
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		return fmt.Errorf("compress backup: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("compress backup: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("compress backup: %w", err)
	}
	return nil
}

// decompressFile writes the zstd file src out as plain bytes at dst.
func decompressFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("decompress backup: %w", err)
	}
	defer in.Close()
	zr, err := zstd.NewReader(in)
	if err != nil {
		return fmt.Errorf("decompress backup: %w", err)
	}
	defer zr.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("decompress backup: %w", err)
	}
	defer func() {
		if cerr := out.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("decompress backup: %w", cerr)
		}
	}()
	if _, err := io.Copy(out, zr); err != nil {
		return fmt.Errorf("decompress backup: %w", err)
	}
	return nil
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
	// SQLite cannot read the compressed file, so it is unpacked beside the
	// live database first (same folder, same filesystem).
	plain := live + ".restore.tmp"
	os.Remove(plain)
	defer os.Remove(plain)
	if err := decompressFile(backup, plain); err != nil {
		return fmt.Errorf("backup %s is unreadable: %w", backup, err)
	}
	src, err := sql.Open("sqlite", plain)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	err = checkBackup(ctx, src)
	src.Close()
	if err != nil {
		return fmt.Errorf("backup %s: %w", backup, err)
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
		}).NewRestore(plain)
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
	return nil
}
