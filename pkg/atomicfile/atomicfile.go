// Package atomicfile places files without ever leaving a partial one,
// replacing an existing one, or losing one to a power cut.
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/text/unicode/norm"
)

// copyBufferSize is how much is read per syscall; same reasoning as the
// metadata phase's hash buffer.
const copyBufferSize = 1 << 20

// Copy copies src to dest atomically (temp file in dest's directory, then a
// no-replace Rename); a taken dest is fs.ErrExist and never touched. tee sees
// every byte and check runs before linking: its error leaves dest untouched.
// Keeps src's permission bits (minus execute) and mtime. Returns bytes written.
func Copy(src, dest string, tee io.Writer, check func() error) (int64, error) {
	if err := MkdirAll(filepath.Dir(dest)); err != nil {
		return 0, fmt.Errorf("create dest dir %s: %w", filepath.Dir(dest), err)
	}

	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".copy-*")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op once Rename moved it
	}()

	var w io.Writer = tmp
	if tee != nil {
		w = io.MultiWriter(tmp, tee)
	}
	// hide WriteTo: io.CopyBuffer would otherwise ignore the 1 MiB buffer and
	// fall back to 32 KiB reads
	n, err := io.CopyBuffer(w, struct{ io.Reader }{in}, make([]byte, copyBufferSize))
	if err != nil {
		return 0, fmt.Errorf("copy %s: %w", src, err)
	}
	// CreateTemp's 0600 hides the file from other readers, and a fresh mtime
	// loses the date EXIF-less files are planned by. Execute bits dropped
	// (FAT/exFAT report 0777). Set before the sync, so they are durable too.
	info, err := in.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", src, err)
	}
	if err := tmp.Chmod(info.Mode().Perm() &^ 0o111); err != nil {
		return 0, fmt.Errorf("set mode on temp file: %w", err)
	}
	if err := os.Chtimes(tmpName, time.Time{}, info.ModTime()); err != nil {
		return 0, fmt.Errorf("set mtime on temp file: %w", err)
	}
	// Close only reaches the page cache: sync before Rename publishes the name,
	// or a power loss can empty a file whose hash already "verified". darwin
	// uses F_FULLFSYNC, flushing the drive cache too.
	if err := tmp.Sync(); err != nil {
		return 0, fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temp file: %w", err)
	}
	if check != nil {
		if err := check(); err != nil {
			return 0, err
		}
	}
	if err := Rename(tmpName, dest); err != nil {
		return 0, err
	}
	return n, nil
}

// MkdirAll is os.MkdirAll that syncs each created folder into its parent: on
// exFAT/FAT (no journal) a power cut can otherwise drop the folder and the
// synced file in it.
func MkdirAll(dir string) error {
	dir = filepath.Clean(dir)
	var created []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		created = append(created, d)
		if parent := filepath.Dir(d); parent == d {
			break
		}
	}
	if len(created) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// topmost first: each new folder's entry lives in the folder above it
	for i := len(created) - 1; i >= 0; i-- {
		if err := syncDir(filepath.Dir(created[i])); err != nil {
			return fmt.Errorf("sync dir %s: %w", filepath.Dir(created[i]), err)
		}
	}
	return nil
}

// ErrSourceKept means Rename linked newpath but couldn't remove oldpath and
// undid the link. A copy fallback would fail the same way.
var ErrSourceKept = errors.New("source could not be removed")

// ErrSourceLeft means RenameCommit linked and committed newpath but couldn't
// remove oldpath; nothing is undone.
var ErrSourceLeft = errors.New("moved and recorded, but the source name could not be removed")

// Rename moves oldpath to newpath without replacing an existing newpath (which
// os.Rename does): hard link, then unlink. A taken newpath is fs.ErrExist; an
// unremovable oldpath undoes the link (ErrSourceKept).
//
// Order matters: oldpath and newpath being one directory entry (however
// spelled) is checked first and is a no-op, since unlinking would delete the
// only name. Only then does newpath being oldpath's inode mean a crash-
// interrupted move, which Rename finishes.
func Rename(oldpath, newpath string) error {
	return renameCommit(oldpath, newpath, nil, osOps())
}

// RenameCommit is Rename with commit run after newpath durably names the file
// and before oldpath is unlinked, so no crash leaves a moved file unrecorded.
// A commit error undoes the move; an unremovable source after commit is
// ErrSourceLeft; any other error after a successful commit means moved and
// recorded, but not cleanly.
//
// ponytail: without hard links (exFAT, some network mounts) the move is one
// rename and commit can only follow it; the caller recovers that gap.
func RenameCommit(oldpath, newpath string, commit func() error) error {
	if commit == nil {
		commit = func() error { return nil }
	}
	return renameCommit(oldpath, newpath, commit, osOps())
}

// fsOps is what renameCommit does to the disk that a test may need to fail.
type fsOps struct {
	link     func(oldpath, newpath string) error
	syncDirs func(paths ...string) error
}

func osOps() fsOps { return fsOps{link: os.Link, syncDirs: syncDirs} }

func renameCommit(oldpath, newpath string, commit func() error, ops fsOps) error {
	if sameEntry(oldpath, newpath) {
		if commit != nil {
			return commit()
		}
		return nil
	}
	err := ops.link(oldpath, newpath)
	if err == nil || errors.Is(err, fs.ErrExist) && halfDoneMove(oldpath, newpath) {
		if commit != nil {
			// newpath has to survive a power cut before anything records it
			if err := ops.syncDirs(newpath); err != nil {
				os.Remove(newpath)
				return err
			}
			if err := commit(); err != nil {
				os.Remove(newpath) // oldpath still names the file
				return err
			}
		}
		// Already gone (a sync client, the user) is a finished move: newpath
		// is now the file's only name, and undoing the link would delete it.
		if err := os.Remove(oldpath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if commit != nil {
				return fmt.Errorf("%w: remove %s: %w", ErrSourceLeft, oldpath, err)
			}
			os.Remove(newpath)
			return fmt.Errorf("%w: remove %s after linking it to %s: %w", ErrSourceKept, oldpath, newpath, err)
		}
		return ops.syncDirs(newpath, oldpath)
	}
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s: %w", newpath, fs.ErrExist)
	}
	// no hard links here, or another device (os.Rename reports that)
	// ponytail: check-then-rename races a newpath created in between;
	// renameat2(RENAME_NOREPLACE)/renamex_np(RENAME_EXCL) would close it
	if _, err := os.Lstat(newpath); err == nil {
		return fmt.Errorf("%s: %w", newpath, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %s: %w", newpath, err)
	}
	if err := os.Rename(oldpath, newpath); err != nil {
		return fmt.Errorf("rename to %s: %w", newpath, err)
	}
	// the file has moved: record it even if the folders won't sync, or it sits
	// in the library unrecorded
	syncErr := ops.syncDirs(newpath, oldpath)
	if commit != nil {
		if err := commit(); err != nil {
			if rerr := os.Rename(newpath, oldpath); rerr != nil {
				return errors.Join(err, fmt.Errorf("the file stays at %s, unrecorded: %w", newpath, rerr))
			}
			return err
		}
	}
	return syncErr
}

// syncDirs makes the directory entries just created or removed durable: until
// synced, a power loss can lose both a move's new and old names. Best effort
// where directories can't be synced.
func syncDirs(paths ...string) error {
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		dir := filepath.Dir(p)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if err := syncDir(dir); err != nil {
			return fmt.Errorf("sync dir %s: %w", dir, err)
		}
	}
	return nil
}

// SyncDir makes dir's entries durable. Best effort where unsupported.
func SyncDir(dir string) error { return syncDir(dir) }

// syncDir fsyncs one directory. Windows has no directory-handle flush, so
// there it is a no-op; NTFS orders its own metadata through its log.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		// many filesystems and network mounts refuse directory fsync
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EPERM) {
			return nil
		}
		return err
	}
	return nil
}

// sameFile reports whether a and b are two names for one file. Any Lstat
// failure is "no": the caller then treats b as taken, the safe answer.
func sameFile(a, b string) bool {
	ai, err := os.Lstat(a)
	if err != nil {
		return false
	}
	bi, err := os.Lstat(b)
	return err == nil && os.SameFile(ai, bi)
}

// halfDoneMove reports whether b is a second link to a's file, left by a crash
// between link and unlink. The link count (>= 2) backstops spellings sameEntry
// misses, so a file's only name is never removed.
func halfDoneMove(a, b string) bool {
	ai, err := os.Lstat(a)
	if err != nil {
		return false
	}
	bi, err := os.Lstat(b)
	return err == nil && os.SameFile(ai, bi) && sharedInode(ai)
}

// sameEntry reports whether a and b are one directory entry however spelled:
// same parent (Stat resolves symlinked prefixes), names equal after NFC and
// case-folding, and the same file (keeps A.jpg/a.jpg apart on case-sensitive
// volumes).
func sameEntry(a, b string) bool {
	if !strings.EqualFold(norm.NFC.String(filepath.Base(a)), norm.NFC.String(filepath.Base(b))) {
		return false
	}
	ad, err := os.Stat(filepath.Dir(a))
	if err != nil {
		return false
	}
	bd, err := os.Stat(filepath.Dir(b))
	return err == nil && os.SameFile(ad, bd) && sameFile(a, b)
}
