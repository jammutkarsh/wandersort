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
// the failure a quick pass exists to catch — no reading required.
func TestVerifyFindsAMissingFileWithoutReadingAnything(t *testing.T) {
	d, out := dbtest.New(t), t.TempDir()
	abs := seedPlaced(t, d, out, 1, "2024/A.jpg", "hello")
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}

	rep := run(t, d, out, false)
	if len(rep.Problems) != 1 || rep.Problems[0].Kind != db.KindNotFound {
		t.Fatalf("problems = %+v, want one not-found", rep.Problems)
	}
	if rep.Problems[0].Path != filepath.Join("2024", "A.jpg") {
		t.Errorf("path = %q, want the library-relative path", rep.Problems[0].Path)
	}
	if rep.Sound() {
		t.Error("Sound() is true with a missing file")
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

func TestVerifyFindsATruncatedFile(t *testing.T) {
	d, out := dbtest.New(t), t.TempDir()
	abs := seedPlaced(t, d, out, 1, "2024/A.jpg", "hello")
	if err := os.WriteFile(abs, []byte("he"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := run(t, d, out, false)
	if len(rep.Problems) != 1 {
		t.Fatalf("problems = %+v, want one", rep.Problems)
	}
}

// The errors table holds only live problems (spec D29), so a file that fails
// and is then put right must stop being reported — by anything reading that
// table, `wandersort issue` included.
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
