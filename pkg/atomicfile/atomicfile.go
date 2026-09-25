// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package atomicfile places files without ever leaving a partial one at the
// destination, replacing an existing one, or losing one to a power cut: the
// copy and move under execute, review's preview copies, and the database's
// safety copies all go through it.
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

// Copy copies src to dest atomically: a temp file in dest's directory, then a
// no-replace Rename, so a failure partway never leaves a partial file at dest
// and an existing dest is never touched — that comes back as an error
// matching fs.ErrExist. Creates dest's parent directory if needed. Returns
// bytes written.
//
// tee, when non-nil, is sent every byte as it is copied, and check runs once
// they are all written, before dest is linked into place: an error from it
// removes the temp file and leaves dest untouched. Execute passes a hasher
// and a comparison against the scan, so the copy is verified with no second
// read. The copy keeps src's permission bits, less any execute bits, and its
// modification time.
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
	// io.Copy would hand the copy to in.WriteTo, which has no fast path for
	// a file or a MultiWriter and falls back to 32 KiB reads — 25 million
	// syscalls on a 783 GiB library. Hiding WriteTo makes the 1 MiB buffer
	// stick.
	n, err := io.CopyBuffer(w, struct{ io.Reader }{in}, make([]byte, copyBufferSize))
	if err != nil {
		return 0, fmt.Errorf("copy %s: %w", src, err)
	}
	// CreateTemp makes the file 0600, which a media server or another user
	// can't read, and a fresh mtime loses the date a file without EXIF is
	// planned by. Execute bits are dropped: FAT/exFAT cards report every
	// file as 0777, and a photo is never a program. Set before the sync
	// below, so the mode and date are as durable as the bytes.
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
	// Closing a file does not put its bytes on the platter — it only hands
	// them to the page cache. Without this, Rename below publishes a name for
	// a file whose contents a power loss can still take away, and the caller's
	// hash check verified the bytes that went through the hasher in memory,
	// not the bytes on the disk. On darwin this is F_FULLFSYNC, so it flushes
	// the drive's own write cache rather than just the OS's.
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

// MkdirAll creates dir and any missing parents, like os.MkdirAll, and makes
// each one it created durable by syncing the folder that holds it. A file
// synced into a folder whose own entry never reached the disk is lost with
// it after a power cut on a filesystem without a journal (exFAT, FAT — what
// external photo drives usually are); journaled filesystems happen to order
// it right, but nothing promises that.
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

// ErrSourceKept means Rename linked newpath but could not remove oldpath, and
// undid the link: nothing changed. Copying instead would not help — it would
// fail on the same remove — so a caller with a copy fallback checks for this.
var ErrSourceKept = errors.New("source could not be removed")

// ErrSourceLeft means RenameCommit linked newpath, committed it, and then
// could not remove oldpath. The file is at newpath and recorded there; oldpath
// is a second name for it. Unlike ErrSourceKept, nothing is undone — commit
// already happened.
var ErrSourceLeft = errors.New("moved and recorded, but the source name could not be removed")

// Rename moves oldpath to newpath without ever replacing an existing newpath,
// which os.Rename silently does on macOS and Linux. It is a hard link then an
// unlink of oldpath: the link fails with EEXIST when newpath is taken, and
// that comes back as an error matching fs.ErrExist. All or nothing — if
// oldpath can't be unlinked the link is undone (ErrSourceKept), so newpath
// never ends up as a second name for a file that also still sits at oldpath.
//
// newpath already being oldpath's own inode is not a collision: a crash
// between the link and the unlink left the move half done, and Rename
// finishes it rather than linking the file a third time under another name.
// oldpath and newpath naming one directory entry is not a move at all: the
// file is already there, and linking or unlinking would delete its only name.
func Rename(oldpath, newpath string) error {
	return renameCommit(oldpath, newpath, nil)
}

// RenameCommit is Rename with commit run at the one moment it is safe to
// record the move: once newpath durably names the file and before oldpath is
// unlinked. A crash then leaves either no record and both names, or a record
// and the file where the record says — never a moved file nothing records.
// commit failing undoes the move (the new link, or the rename) and returns
// its error. Once commit succeeded, a source that cannot be unlinked is
// ErrSourceLeft rather than an undo.
//
// ponytail: where hard links don't exist (exFAT, some network mounts) the
// move is one rename, so commit can only follow it: a crash in between
// leaves the file moved and unrecorded, which the caller has to recover.
func RenameCommit(oldpath, newpath string, commit func() error) error {
	if commit == nil {
		commit = func() error { return nil }
	}
	return renameCommit(oldpath, newpath, commit)
}

func renameCommit(oldpath, newpath string, commit func() error) error {
	if sameEntry(oldpath, newpath) {
		if commit != nil {
			return commit()
		}
		return nil
	}
	err := os.Link(oldpath, newpath)
	if err == nil || errors.Is(err, fs.ErrExist) && halfDoneMove(oldpath, newpath) {
		if commit != nil {
			// newpath has to survive a power cut before anything records it
			if err := syncDirs(newpath); err != nil {
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
		return syncDirs(newpath, oldpath)
	}
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s: %w", newpath, fs.ErrExist)
	}
	// No hard links here (exFAT, some network mounts), or oldpath is on
	// another device — os.Rename below reports the latter as it always did.
	// ponytail: check-then-rename races anything that creates newpath between
	// the Lstat and the rename; renameat2(RENAME_NOREPLACE) on Linux and
	// renamex_np(RENAME_EXCL) on darwin close it if that ever matters.
	if _, err := os.Lstat(newpath); err == nil {
		return fmt.Errorf("%s: %w", newpath, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %s: %w", newpath, err)
	}
	if err := os.Rename(oldpath, newpath); err != nil {
		return fmt.Errorf("rename to %s: %w", newpath, err)
	}
	if err := syncDirs(newpath, oldpath); err != nil {
		return err
	}
	if commit != nil {
		if err := commit(); err != nil {
			if rerr := os.Rename(newpath, oldpath); rerr != nil {
				return errors.Join(err, fmt.Errorf("the file stays at %s, unrecorded: %w", newpath, rerr))
			}
			return err
		}
	}
	return nil
}

// syncDirs makes the directory entries this package just created and removed
// durable. A rename is atomic with respect to ordering, but the *dirent* is
// not on stable storage until the filesystem's own commit interval: after a
// power loss the new name and the old one can both be gone, which for a move
// means the file is gone. Duplicate and unsyncable directories are skipped —
// the sync is best effort on filesystems that don't support it, since the
// alternative is refusing to place a file that is already correctly on disk.
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

// SyncDir makes dir's entries durable: a file created or renamed into dir is
// not on stable storage until its directory is synced too. Best effort where
// the platform or filesystem has no directory flush.
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
		// Some filesystems (and most network mounts) refuse fsync on a
		// directory. Nothing is wrong with the file itself, so don't fail a
		// transfer over it.
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

// halfDoneMove reports whether b is a second link to a's file, left by a
// crash between Rename's link and unlink. The link count is the backstop
// for a spelling sameEntry misses but the filesystem treats as one name
// (ß against SS, exFAT's own case table): one entry is one link, so a file's
// only name is never the one removed, whatever the spelling rules say.
func halfDoneMove(a, b string) bool {
	ai, err := os.Lstat(a)
	if err != nil {
		return false
	}
	bi, err := os.Lstat(b)
	return err == nil && os.SameFile(ai, bi) && sharedInode(ai)
}

// sameEntry reports whether a and b are one directory entry, however they are
// spelled: the same parent (Stat, so a symlinked prefix like /tmp against
// /private/tmp resolves), names equal once normalised to NFC and case-folded
// (APFS/exFAT treat Café in NFD and café in NFC as one name), and one file.
// The last check keeps A.jpg and a.jpg on a case-sensitive volume apart:
// there they are two entries, two files, and newpath is simply taken.
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
