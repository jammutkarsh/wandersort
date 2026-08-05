// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

func seedVFSEntry(t *testing.T, d *db.DB, fileID int64, status string) {
	t.Helper()
	name := fmt.Sprintf("IMG_%d.jpg", fileID)
	dbtest.SeedFile(t, d, fileID, "/src", name, 100)
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO virtual_fs_entries (file_id, source_path, target_path, status) VALUES (?, ?, ?, ?)`,
		fileID, "/src/"+name, "2024/08/"+name, status); err != nil {
		t.Fatal(err)
	}
}

func TestApprovedCount(t *testing.T) {
	d := dbtest.New(t)
	seedVFSEntry(t, d, 1, db.StatusProposed)
	seedVFSEntry(t, d, 2, db.StatusApproved)
	seedVFSEntry(t, d, 3, db.StatusApproved)

	n, err := approvedCount(context.Background(), d)
	if err != nil {
		t.Fatalf("approvedCount: %v", err)
	}
	if n != 2 {
		t.Errorf("approvedCount = %d, want 2", n)
	}
}

func TestApprovedCountEmpty(t *testing.T) {
	d := dbtest.New(t)
	n, err := approvedCount(context.Background(), d)
	if err != nil {
		t.Fatalf("approvedCount: %v", err)
	}
	if n != 0 {
		t.Errorf("approvedCount(empty) = %d, want 0", n)
	}
}

func TestRunReviewNoDatabase(t *testing.T) {
	dir := t.TempDir()
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		AppDBPath: filepath.Join(dir, ".wandersort.db"),
		LogFile:   filepath.Join(dir, "wandersort.log"),
	}}
	cmd := &cobra.Command{Use: "review"}
	cmd.Flags().Bool(flagYes, false, "")
	cmd.Flags().Bool(flagRebuild, false, "")
	if err := a.runReview(cmd); err == nil {
		t.Fatal("runReview with no database on disk must fail")
	}
}

// TestRunReviewRebuildBlocksApprovedPlan pins the safety rail: --rebuild
// throws away every proposal, including an already-approved one, so it must
// refuse rather than silently discard confirmed work unless --yes overrides it.
func TestRunReviewRebuildBlocksApprovedPlan(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".wandersort.db")
	seedDB(t, dbPath)

	d, err := db.New(context.Background(), dbPath, db.AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	seedVFSEntry(t, d, 1, db.StatusApproved)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		AppDBPath: dbPath,
		LogFile:   filepath.Join(dir, "wandersort.log"),
	}}
	cmd := &cobra.Command{Use: "review"}
	cmd.Flags().Bool(flagYes, false, "")
	cmd.Flags().Bool(flagRebuild, false, "")
	if err := cmd.Flags().Set(flagRebuild, "true"); err != nil {
		t.Fatal(err)
	}

	err = a.runReview(cmd)
	if err == nil {
		t.Fatal("--rebuild without --yes must refuse to discard an approved plan")
	}
	a.closeDBs()
}

func TestReportReviewOutcome(t *testing.T) {
	a := &app{Log: logger.NewNoopLogger()}

	if _, err := a.reportReviewOutcome(true, errors.New("boom")); err == nil {
		t.Error("a save error must always surface, even when confirmed=true")
	}
	note, err := a.reportReviewOutcome(false, nil)
	if err != nil {
		t.Errorf("a cancelled review with no error must not itself error, got %v", err)
	}
	if !strings.Contains(note, "cancelled") {
		t.Errorf("cancelled note = %q, want it to say so", note)
	}
	note, err = a.reportReviewOutcome(true, nil)
	if err != nil {
		t.Errorf("a confirmed review with no error must not error, got %v", err)
	}
	if !strings.Contains(note, "approved") {
		t.Errorf("confirmed note = %q, want it to say so", note)
	}
}
