// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package verify

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// seedPlaced writes content into the library at rel and rows it the way
// execute would once the file landed: library-relative dir and name, the
// scan's hash, placed = 1.
func seedPlaced(t *testing.T, d *db.DB, out string, id int64, rel, content string) string {
	t.Helper()
	abs := filepath.Join(out, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, id, filepath.Dir(rel), filepath.Base(rel), int64(len(content)))
	dbtest.SeedHash(t, d, id, hashOf(content))
	dbtest.SeedPlaced(t, d, id)
	return abs
}

func hashOf(content string) string {
	h := metadata.NewHasher()
	h.Write([]byte(content))
	return metadata.HashString(h)
}

func run(t *testing.T, d *db.DB, out string, full bool) Report {
	t.Helper()
	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Full: full})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestVerifyPassesOnAnIntactLibrary(t *testing.T) {
	d, out := dbtest.New(t), t.TempDir()
	seedPlaced(t, d, out, 1, "2024/A.jpg", "hello")
	seedPlaced(t, d, out, 2, "2024/B.jpg", "world!")

	rep := run(t, d, out, true)
	if !rep.Sound() {
		t.Fatalf("intact library reported problems: %+v", rep)
	}
	if rep.Checked != 2 || rep.Bytes != int64(len("hello")+len("world!")) {
		t.Errorf("report = %+v, want 2 files checked", rep)
	}
	if rep.Database != "ok" {
		t.Errorf("database = %q, want ok", rep.Database)
	}
}

// A file the user (or anything else) deleted out from under the library is
// the failure a quick pass exists to catch — no reading required. It is not
// kept as a problem: its rows are deleted, after a backup, so the next check
// does not list it again and a copy at a source can be planned in again.
func TestVerifyForgetsAMissingFileWithoutReadingAnything(t *testing.T) {
	d, out := dbtest.New(t), t.TempDir()
	abs := seedPlaced(t, d, out, 1, "2024/A.jpg", "hello")
	seedPlaced(t, d, out, 2, "2024/B.jpg", "world")
	dbtest.SeedEntry(t, d, 1, "2024/A.jpg", "2024/A.jpg")
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}

	rep := run(t, d, out, false)
	if len(rep.Problems) != 0 {
		t.Errorf("problems = %+v, want none — a gone file is forgotten, not kept", rep.Problems)
	}
	if len(rep.Forgotten) != 1 || rep.Forgotten[0] != filepath.Join("2024", "A.jpg") {
		t.Fatalf("forgotten = %v, want the library-relative 2024/A.jpg", rep.Forgotten)
	}
	if !rep.Sound() {
		t.Error("Sound() is false once the gone file is forgotten")
	}
	for table, want := range map[string]int{"file_registry": 1, "file_metadata": 1, "virtual_fs_entries": 0, "errors": 0} {
		var n int
		if err := d.SQL.Get(&n, `SELECT COUNT(*) FROM `+table); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s holds %d rows, want %d", table, n, want)
		}
	}
	if _, err := os.Stat(filepath.Join(out, db.BackupFileName)); err != nil {
		t.Errorf("no backup before forgetting: %v", err)
	}
	if rep := run(t, d, out, false); len(rep.Forgotten) != 0 || rep.Checked != 1 {
		t.Errorf("second check: %+v, want 1 file checked and nothing forgotten again", rep)
	}
}

// Same size, different bytes: the one failure only a full pass can see, and
// the whole reason the hash is stored.
func TestVerifyFullFindsChangedContents(t *testing.T) {
	d, out := dbtest.New(t), t.TempDir()
	abs := seedPlaced(t, d, out, 1, "2024/A.jpg", "hello")
	if err := os.WriteFile(abs, []byte("HELLO"), 0o644); err != nil {
		t.Fatal(err)
	}

	if rep := run(t, d, out, false); len(rep.Problems) != 0 {
		t.Errorf("a quick pass should not notice changed bytes: %+v", rep.Problems)
	}
	rep := run(t, d, out, true)
	if len(rep.Problems) != 1 || rep.Problems[0].Kind != db.KindChecksumMismatch {
		t.Fatalf("problems = %+v, want one checksum-mismatch", rep.Problems)
	}
}

// A file that is there but wrong is damage, not a deletion: it is reported
// and its records are kept.
func TestVerifyFindsATruncatedFile(t *testing.T) {
	d, out := dbtest.New(t), t.TempDir()
	abs := seedPlaced(t, d, out, 1, "2024/A.jpg", "hello")
	if err := os.WriteFile(abs, []byte("he"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := run(t, d, out, false)
	if len(rep.Problems) != 1 || len(rep.Forgotten) != 0 {
		t.Fatalf("problems = %+v, forgotten = %v; want one problem, nothing forgotten", rep.Problems, rep.Forgotten)
	}
	var n int
	if err := d.SQL.Get(&n, `SELECT COUNT(*) FROM file_registry`); err != nil || n != 1 {
		t.Errorf("registry rows = %d, %v; want the damaged file kept", n, err)
	}
}

// The errors table holds only live problems (spec D29), so a file that fails
// and is then put right must stop being reported — by anything reading that
// table, `wandersort admin report` included.
func TestVerifyClearsAProblemOnceItIsFixed(t *testing.T) {
	d, out := dbtest.New(t), t.TempDir()
	abs := seedPlaced(t, d, out, 1, "2024/A.jpg", "hello")
	if err := os.WriteFile(abs, []byte("HELLO"), 0o644); err != nil {
		t.Fatal(err)
	}

	run(t, d, out, true)
	if n := verifyErrors(t, d); n != 1 {
		t.Fatalf("error rows = %d, want 1", n)
	}

	if err := os.WriteFile(abs, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rep := run(t, d, out, true); !rep.Sound() {
		t.Fatalf("restored file still reported: %+v", rep.Problems)
	}
	if n := verifyErrors(t, d); n != 0 {
		t.Errorf("error rows = %d after the file was put right, want 0", n)
	}
}

// A crash mid-copy leaves a full-size temp file in the user's own folders and
// nothing else in WanderSort ever collects it.
func TestVerifyReportsLeftoverTempFiles(t *testing.T) {
	d, out := dbtest.New(t), t.TempDir()
	seedPlaced(t, d, out, 1, "2024/A.jpg", "hello")
	if err := os.WriteFile(filepath.Join(out, "2024", ".copy-123456"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := run(t, d, out, false)
	if len(rep.Problems) != 0 {
		t.Errorf("a stray is not a broken file: %+v", rep.Problems)
	}
	if len(rep.Strays) != 1 {
		t.Fatalf("strays = %v, want one", rep.Strays)
	}
	if rep.Sound() {
		t.Error("Sound() is true with a stray temp file left behind")
	}
	// Reported, never removed: deleting files is execute's job.
	if _, err := os.Stat(filepath.Join(out, "2024", ".copy-123456")); err != nil {
		t.Errorf("verify deleted the stray: %v", err)
	}
}

// An unplaced file still lives at its source, where the user may have changed
// it since — the plan is a proposal about it, not a record of it.
func TestVerifyIgnoresUnplacedFiles(t *testing.T) {
	d, out := dbtest.New(t), t.TempDir()
	dbtest.SeedFile(t, d, 1, "/somewhere/else", "A.jpg", 5)
	dbtest.SeedHash(t, d, 1, hashOf("hello"))

	rep := run(t, d, out, true)
	if rep.Checked != 0 || len(rep.Problems) != 0 {
		t.Errorf("report = %+v, want nothing checked", rep)
	}
}

func verifyErrors(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.SQL.Get(&n, `SELECT count(*) FROM errors WHERE stage = ?`, db.StageVerify); err != nil {
		t.Fatal(err)
	}
	return n
}
