// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

func execCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "execute"}
	cmd.Flags().Bool(flagMove, false, "")
	cmd.Flags().Bool(flagDryRun, false, "")
	cmd.Flags().Bool(flagYes, false, "")
	return cmd
}

// TestRunExecuteAppliesDraft covers spec D18 from the command's side: the
// review's draft reaches the plan only here, the file lands under the renamed
// folder, and the draft is gone afterwards.
func TestRunExecuteAppliesDraft(t *testing.T) {
	cfg := testConfig(t)
	dir := t.TempDir()
	cfg.AppDBPath = filepath.Join(dir, ".wandersort.db")
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), []byte("photo"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := db.New(context.Background(), cfg.AppDBPath, db.AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, 1, src, "a.jpg", 5)
	day := dbtest.SeedEntry(t, d, 1, filepath.Join(src, "a.jpg"), "2024/06_June/03/a.jpg")
	hash, err := metadata.HashFile(filepath.Join(src, "a.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedHash(t, d, 1, hash)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	seedDraft(t, dir, vfs.Edit{Seq: 1, Op: vfs.OpRename, Node: day, From: "03", To: "Goa-Trip"})

	a := &app{Log: logger.NewNoopLogger(), Config: cfg}
	err = a.runExecute(execCmd(t))
	a.closeDBs()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "2024", "06_June", "Goa-Trip", "a.jpg")); err != nil {
		t.Errorf("file not under the renamed folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, vfs.DraftFileName)); !os.IsNotExist(err) {
		t.Errorf("draft still there after execute: %v", err)
	}
}
