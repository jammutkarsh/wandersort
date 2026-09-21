// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// issueApp returns an app whose logs live in dir/logs and whose working
// directory (where the zip lands) is dir. logs maps file name to contents.
func issueApp(t *testing.T, logs map[string]string) (*app, string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	logDir := filepath.Join(dir, "logs")
	if err := os.Mkdir(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range logs {
		if err := os.WriteFile(filepath.Join(logDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		LogDir:    logDir,
		AppDBPath: filepath.Join(dir, "lib", ".wandersort.db"),
	}}, dir
}

func TestRunIssueNoLogData(t *testing.T) {
	tests := []struct {
		name string
		logs map[string]string
	}{
		{"no logs", nil},
		{"only empty logs", map[string]string{"2026-01-01T10-00-00_1.log": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := issueApp(t, tt.logs)
			if err := a.runIssue(false, false); err == nil {
				t.Fatal("runIssue with nothing logged must fail")
			}
		})
	}
}

func TestRunIssuePackagesRecentLogs(t *testing.T) {
	logs := map[string]string{}
	for i := 1; i <= issueLogs+2; i++ {
		logs[fmt.Sprintf("2026-01-0%dT10-00-00_1.log", i)] = "some log data\n"
	}
	a, dir := issueApp(t, logs)
	a.logFile = logger.NewFile(a.Config.LogDir)
	a.logFile.Persist() // this run's own log, newest of all: must not be packaged
	if _, err := a.logFile.Write([]byte("wandersort started\n")); err != nil {
		t.Fatal(err)
	}

	if err := a.runIssue(false, false); err != nil {
		t.Fatalf("runIssue: %v", err)
	}
	entries, names := readZips(t, dir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one issue zip in the working directory, found %d", len(entries))
	}
	want := map[string]bool{"about.txt": true}
	for i := 3; i <= issueLogs+2; i++ { // the newest issueLogs, not this run's
		want[fmt.Sprintf("logs/2026-01-0%dT10-00-00_1.log", i)] = true
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("zip entries = %v, want %v", names, want)
	}
}

func TestRunIssueIncludesDBWhenRequested(t *testing.T) {
	a, dir := issueApp(t, map[string]string{"2026-01-01T10-00-00_1.log": "some log data\n"})
	if err := os.MkdirAll(filepath.Dir(a.Config.AppDBPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.Config.AppDBPath, []byte("fake db bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := a.runIssue(true, false); err != nil {
		t.Fatalf("runIssue: %v", err)
	}
	_, names := readZips(t, dir)
	if !names["wandersort.db"] {
		t.Errorf("--include-db must package the database, got entries %v", names)
	}
}

// The error report ships without --include-db, and the home directory's
// username appears nowhere in it.
func TestRunIssueShipsScrubbedErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a, dir := issueApp(t, map[string]string{"2026-01-01T10-00-00_1.log": "some log data\n"})
	if err := os.MkdirAll(filepath.Dir(a.Config.AppDBPath), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := db.New(context.Background(), a.Config.AppDBPath, db.AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, 1, filepath.Join(home, "Pictures"), "IMG_1.HEIC", 10)
	failure := db.WithStack(fmt.Errorf("open %s: %w", filepath.Join(home, "Pictures", "IMG_1.HEIC"), fs.ErrPermission))
	d.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
		return db.RecordError(ctx, tx, 1, db.StageRead, "open", failure)
	})
	d.Writer.Flush()
	d.Close() // the live database is exclusively locked while open

	if err := a.runIssue(false, false); err != nil {
		t.Fatalf("runIssue: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "wandersort-issue-*.zip"))
	zr, err := zip.OpenReader(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var got string
	for _, f := range zr.File {
		if f.Name != "errors.json" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		r.Close()
		got = string(b)
	}
	if got == "" {
		t.Fatal("no errors.json in the zip")
	}
	if strings.Contains(got, home) {
		t.Errorf("home directory %q survived in errors.json:\n%s", home, got)
	}
	if !strings.Contains(got, "$HOME/Pictures/IMG_1.HEIC") {
		t.Errorf("errors.json lost the folder structure:\n%s", got)
	}
}

func TestAddFileToZipMissingSource(t *testing.T) {
	dir := t.TempDir()
	zf, err := os.Create(filepath.Join(dir, "t.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer zf.Close()
	zw := zip.NewWriter(zf)
	defer zw.Close()

	if err := addFileToZip(zw, filepath.Join(dir, "does-not-exist"), "entry"); err == nil {
		t.Error("addFileToZip must fail when the source file doesn't exist")
	}
}

func readZips(t *testing.T, dir string) ([]string, map[string]bool) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "wandersort-issue-*.zip"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	if len(matches) == 0 {
		return matches, names
	}
	zr, err := zip.OpenReader(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		names[f.Name] = true
	}
	return matches, names
}
