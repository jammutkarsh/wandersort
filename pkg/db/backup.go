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
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
)

// BackupFileName is the one zstd-compressed backup kept beside the library
// database, overwritten by every execute run and `admin db --reset`.
const BackupFileName = ".wandersort.db.zst"

// PreMigrationBackupFileName is the backup taken before a schema upgrade, kept
// apart so the next execute doesn't overwrite it.
const PreMigrationBackupFileName = ".wandersort.db.pre-upgrade.zst"

// BeforeRestoreFileName is the database a restore replaced; rename it back to
// undo a restore. One is kept.
const BeforeRestoreFileName = ".wandersort.db.before-restore"

// ErrInUse means another connection — another program, or a sqlite browser —
// has the database open, so it cannot be replaced safely.
var ErrInUse = errors.New("the database is open in another program")

// Backup writes a consistent compressed copy to dest via VACUUM INTO. The copy
// is verified while still plain SQLite, then compressed and renamed over dest,
// so a failure leaves the previous backup intact.
func (d *DB) Backup(ctx context.Context, dest string) error {
	// Writes still queued in the async writer belong to the state being
	// backed up; without this the backup could miss the last edits.
	if d.Writer != nil {
		d.Writer.drain()
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
	// with the file synced, this makes the rename durable; otherwise a power
	// cut could leave an empty file under the backup's name
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

// Restore replaces the database at live with backup (kept in place). Nothing
// is touched unless the backup checks out and no other connection has live
// open (ErrInUse). Pages go through SQLite's online backup and rollback
// journal, never a file swap, so a crash rolls back to the old database.
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

	// leaving WAL needs every other connection gone, and EXCLUSIVE keeps
	// that lock until this connection closes
	if _, err := c.ExecContext(ctx, `PRAGMA locking_mode=EXCLUSIVE`); err != nil {
		return fmt.Errorf("lock database: %w", err)
	}
	var mode string
	if err := c.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		var se *sqlite.Error
		if errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_BUSY { // primary code, extended bits masked
			return ErrInUse
		}
		return fmt.Errorf("database %s is unreadable (%w); rename it aside and restore again", live, err)
	}
	if mode != "delete" {
		return ErrInUse
	}

	// Only now, with every other connection shut out and the WAL folded into
	// the main file, is the file on disk the whole database: keep it.
	if err := keepBeforeRestore(ctx, c, live); err != nil {
		return fmt.Errorf("keep a copy of the database being replaced (nothing was changed): %w", err)
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

// keepBeforeRestore copies the live database aside before a restore: a byte
// copy (works on a database too damaged for SQLite), falling back to VACUUM
// INTO where Windows forbids reading SQLite's locked byte range (past 1 GiB).
func keepBeforeRestore(ctx context.Context, c *sql.Conn, live string) error {
	dest := filepath.Join(filepath.Dir(live), BeforeRestoreFileName)
	if err := os.Remove(dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_, copyErr := atomicfile.Copy(live, dest, nil, nil)
	if copyErr == nil {
		return nil
	}
	os.Remove(dest)
	if _, err := c.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		os.Remove(dest)
		return errors.Join(copyErr, err)
	}
	f, err := os.Open(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return err
	}
	return atomicfile.SyncDir(filepath.Dir(dest))
}
