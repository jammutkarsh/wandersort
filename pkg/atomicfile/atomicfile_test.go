// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCopyNeverReplacesDest(t *testing.T) {
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "src"), filepath.Join(dir, "dest")
	write(t, src, "new")
	write(t, dest, "old")

	if _, err := Copy(src, dest, nil, nil); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if got := read(t, dest); got != "old" {
		t.Errorf("dest = %q, want untouched", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestCopyCreatesDest(t *testing.T) {
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "src"), filepath.Join(dir, "sub", "dest")
	write(t, src, "hello")

	n, err := Copy(src, dest, nil, nil)
	if err != nil || n != 5 {
		t.Fatalf("n, err = %d, %v", n, err)
	}
	if got := read(t, dest); got != "hello" {
		t.Errorf("dest = %q", got)
	}
}

// A failed check leaves nothing behind: no dest, no temp file. The tee saw
// every byte before the check ran.
func TestCopyCheckFailureLeavesNoDest(t *testing.T) {
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "src"), filepath.Join(dir, "sub", "dest")
	write(t, src, "hello")

	var seen strings.Builder
	errBad := errors.New("bad")
	if _, err := Copy(src, dest, &seen, func() error { return errBad }); !errors.Is(err, errBad) {
		t.Fatalf("err = %v, want the check's error", err)
	}
	if seen.String() != "hello" {
		t.Errorf("tee saw %q, want every byte", seen.String())
	}
	if entries, _ := os.ReadDir(filepath.Dir(dest)); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestCopyKeepsModeAndMtime(t *testing.T) {
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "src"), filepath.Join(dir, "dest")
	write(t, src, "hello")
	mtime := time.Date(2019, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chmod(src, 0o755); err != nil { // an exFAT card's 0777, near enough
		t.Fatal(err)
	}
	if err := os.Chtimes(src, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	if _, err := Copy(src, dest, nil, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644 (execute bits dropped)", info.Mode().Perm())
	}
	if !info.ModTime().Equal(mtime) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), mtime)
	}
}

func TestRenameNeverReplaces(t *testing.T) {
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "src"), filepath.Join(dir, "dest")
	write(t, src, "new")
	write(t, dest, "old")

	if err := Rename(src, dest); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if read(t, dest) != "old" || read(t, src) != "new" {
		t.Error("a refused rename touched a file")
	}

	free := filepath.Join(dir, "free")
	if err := Rename(src, free); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source still there: %v", err)
	}
	if read(t, free) != "new" {
		t.Error("renamed content wrong")
	}
}

// A source that can't be removed (read-only card) undoes the link: the file
// is never left under both names.
func TestRenameUndoesLinkWhenSourceKept(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("root and Windows ignore the read-only source folder this test relies on")
	}
	srcDir, destDir := t.TempDir(), t.TempDir()
	src, dest := filepath.Join(srcDir, "src"), filepath.Join(destDir, "dest")
	write(t, src, "photo")
	if err := os.Chmod(srcDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(srcDir, 0o755) })

	if err := Rename(src, dest); !errors.Is(err, ErrSourceKept) {
		t.Fatalf("err = %v, want ErrSourceKept", err)
	}
	if read(t, src) != "photo" {
		t.Error("source changed")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("link not undone: %v", err)
	}
}

// A crash between Rename's link and unlink leaves the file under both names;
// the next Rename finishes the move instead of calling newpath taken.
func TestRenameFinishesHalfDoneMove(t *testing.T) {
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "src"), filepath.Join(dir, "dest")
	write(t, src, "photo")
	if err := os.Link(src, dest); err != nil {
		t.Skipf("no hard links here: %v", err)
	}

	if err := Rename(src, dest); err != nil {
		t.Fatalf("err = %v, want the move finished", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source still there: %v", err)
	}
	if read(t, dest) != "photo" {
		t.Error("dest content wrong")
	}
}

// oldpath and newpath naming one directory entry is "already there", never a
// half-done move: finishing that move would delete the file's only name.
func TestRenameOntoItselfKeepsFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Café.jpg")
	write(t, src, "photo")

	sep := string(filepath.Separator)
	same := []string{src, dir + sep + "." + sep + "Café.jpg"} // uncleaned
	if runtime.GOOS == "darwin" {
		// APFS: case- and normalisation-insensitive, and /var is a symlink
		// to /private/var, so a resolved parent is the same entry too.
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		same = append(same,
			filepath.Join(dir, "CAFÉ.JPG"),
			filepath.Join(dir, "Cafe\u0301.jpg"), // NFD
			filepath.Join(resolved, "Café.jpg"),
		)
	}
	for _, dest := range same {
		if err := Rename(src, dest); err != nil {
			t.Errorf("Rename(src, %q) = %v, want nil", dest, err)
		}
		if read(t, src) != "photo" {
			t.Fatalf("Rename(src, %q) lost the file", dest)
		}
	}
}

// On a case-sensitive volume A.jpg and a.jpg are two files: the second name
// is taken, not the first one respelled.
func TestRenameCaseVariantOnCaseSensitiveVolume(t *testing.T) {
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "A.jpg"), filepath.Join(dir, "a.jpg")
	write(t, src, "new")
	write(t, dest, "old")
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Skip("case-insensitive volume")
	}
	if err := Rename(src, dest); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if read(t, src) != "new" || read(t, dest) != "old" {
		t.Error("a refused rename touched a file")
	}
}

// The backstop under sameEntry: a file with one name is never a half-done
// move, even when both paths reach it.
func TestHalfDoneMoveNeedsTwoLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no link count on Windows")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "a.jpg")
	write(t, src, "photo")
	if halfDoneMove(src, src) {
		t.Error("one name reported as a half-done move")
	}
	link := filepath.Join(dir, "b.jpg")
	if err := os.Link(src, link); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	if !halfDoneMove(src, link) {
		t.Error("two links not reported as a half-done move")
	}
}

func TestSyncDirsDedupesAndTolerates(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	write(t, a, "a")
	write(t, b, "b")

	// Two paths in one directory is one fsync, and it must succeed on an
	// ordinary local filesystem — a Copy that syncs nothing is the whole bug
	// this guards.
	if err := syncDirs(a, b); err != nil {
		t.Fatalf("syncDirs on a real directory: %v", err)
	}

	// A directory that isn't there is a real error, not something to swallow:
	// it means the caller published a name somewhere unexpected.
	if err := syncDirs(filepath.Join(dir, "gone", "x")); err == nil && runtime.GOOS != "windows" {
		t.Error("syncDirs should report a missing directory")
	}
}

func TestSyncDir(t *testing.T) {
	if err := SyncDir(t.TempDir()); err != nil {
		t.Errorf("SyncDir(existing) = %v, want nil", err)
	}
	if runtime.GOOS != "windows" {
		if err := SyncDir(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Error("SyncDir(missing) = nil, want an error")
		}
	}
}

func TestMkdirAll(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "2024", "08_August", "Goa")
	if err := MkdirAll(deep); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(deep); err != nil || !info.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
	if err := MkdirAll(deep); err != nil {
		t.Errorf("existing folder: %v", err)
	}
	blocker := filepath.Join(base, "file")
	write(t, blocker, "x")
	if err := MkdirAll(filepath.Join(blocker, "sub")); err == nil {
		t.Error("MkdirAll under a file = nil, want an error")
	}
}

func TestRenameCommit(t *testing.T) {
	t.Run("commit sees both names, then the source goes", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "a.jpg"), filepath.Join(dir, "b.jpg")
		write(t, src, "photo")
		var both bool
		err := RenameCommit(src, dst, func() error {
			_, e1 := os.Stat(src)
			_, e2 := os.Stat(dst)
			both = e1 == nil && e2 == nil
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !both {
			t.Error("commit ran without both names in place")
		}
		if _, err := os.Stat(src); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("source left behind: %v", err)
		}
		if read(t, dst) != "photo" {
			t.Error("destination lost the bytes")
		}
	})
	t.Run("a failed commit undoes the move", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "a.jpg"), filepath.Join(dir, "b.jpg")
		write(t, src, "photo")
		boom := errors.New("boom")
		if err := RenameCommit(src, dst, func() error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the commit's", err)
		}
		if read(t, src) != "photo" {
			t.Error("source lost")
		}
		if _, err := os.Stat(dst); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("destination left behind: %v", err)
		}
	})
	t.Run("a file already in place is still committed", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "a.jpg")
		write(t, p, "photo")
		called := false
		if err := RenameCommit(p, p, func() error { called = true; return nil }); err != nil {
			t.Fatal(err)
		}
		if !called || read(t, p) != "photo" {
			t.Errorf("called = %v; file must stay", called)
		}
	})
	t.Run("a taken name is refused without committing", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "a.jpg"), filepath.Join(dir, "b.jpg")
		write(t, src, "photo")
		write(t, dst, "other")
		called := false
		if err := RenameCommit(src, dst, func() error { called = true; return nil }); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("err = %v, want fs.ErrExist", err)
		}
		if called {
			t.Error("committed a move that did not happen")
		}
	})
}
