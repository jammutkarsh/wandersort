// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

func TestRunReviewNoDatabase(t *testing.T) {
	dir := t.TempDir()
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		AppDBPath: filepath.Join(dir, ".wandersort.db"),
	}}
	cmd := &cobra.Command{Use: "review"}
	cmd.Flags().Bool(flagYes, false, "")
	if err := a.runReview(cmd); err == nil {
		t.Fatal("runReview with no database on disk must fail")
	}
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

// TestSettingsChanged pins the three answers a re-plan hangs on: no stamp is
// never a change (a proposal from before stamping must not re-plan), a
// matching stamp is not a change, and a settings edit is.
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
