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
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
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

// TestSettingsChanged pins the three answers the rebuild prompt hangs on: no
// stamp is never a change (a proposal from before stamping must not prompt),
// a matching stamp is not a change, and a settings edit is.
func TestSettingsChanged(t *testing.T) {
	cfg := testConfig(t)
	a := &app{Config: cfg, Log: logger.NewNoopLogger()}
	outputDir := t.TempDir()

	if a.settingsChanged(outputDir) {
		t.Error("no stamp file must never read as a settings change")
	}

	if err := vfs.WriteStamp(outputDir, vfs.ConfigStamp(vfs.ConfigFor(cfg))); err != nil {
		t.Fatal(err)
	}
	if a.settingsChanged(outputDir) {
		t.Error("the stamp the current settings produce must not read as a change")
	}

	a.Config.Rules = []string{vfs.RuleDevice, vfs.RuleMedia}
	if !a.settingsChanged(outputDir) {
		t.Error("changed rules must read as a settings change")
	}
}
