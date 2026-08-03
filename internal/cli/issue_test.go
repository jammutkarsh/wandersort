// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func TestRunIssueNoLogFile(t *testing.T) {
	dir := t.TempDir()
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		LogFile:   filepath.Join(dir, "wandersort.log"),
		AppDBPath: filepath.Join(dir, ".wandersort.db"),
	}}
	if err := a.runIssue(false); err == nil {
		t.Fatal("runIssue with no log file must fail")
	}
}

func TestRunIssueEmptyLogFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "wandersort.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		LogFile:   logPath,
		AppDBPath: filepath.Join(dir, ".wandersort.db"),
	}}
	if err := a.runIssue(false); err == nil {
		t.Fatal("runIssue with an empty (just-created) log file must fail — logger always creates one")
	}
}

func TestRunIssueCreatesZipWithoutDB(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "wandersort.log")
	if err := os.WriteFile(logPath, []byte("some log data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, ".wandersort.db")
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{LogFile: logPath, AppDBPath: dbPath}}

	if err := a.runIssue(false); err != nil {
		t.Fatalf("runIssue: %v", err)
	}
	entries, names := readZips(t, dir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one issue zip, found %d", len(entries))
	}
	wantNames := map[string]bool{"wandersort.log": true, "about.txt": true}
	if len(names) != len(wantNames) {
		t.Fatalf("zip entries = %v, want exactly %v", names, wantNames)
	}
	for n := range wantNames {
		if !names[n] {
			t.Errorf("zip missing entry %q, got %v", n, names)
		}
	}
}

func TestRunIssueIncludesDBWhenRequested(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "wandersort.log")
	if err := os.WriteFile(logPath, []byte("some log data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, ".wandersort.db")
	if err := os.WriteFile(dbPath, []byte("fake db bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{LogFile: logPath, AppDBPath: dbPath}}

	if err := a.runIssue(true); err != nil {
		t.Fatalf("runIssue: %v", err)
	}
	_, names := readZips(t, dir)
	if !names["wandersort.db"] {
		t.Errorf("--include-db must package the database, got entries %v", names)
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
