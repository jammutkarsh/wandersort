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

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

func newResetTestCmd(t *testing.T, yes bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "reset"}
	cmd.Flags().Bool(flagYes, false, "")
	cmd.Flags().Bool(flagPlain, false, "")
	if yes {
		if err := cmd.Flags().Set(flagYes, "true"); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

func TestRunResetNoDatabase(t *testing.T) {
	dir := t.TempDir()
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		AppDBPath: filepath.Join(dir, ".wandersort.db"),
		LogFile:   filepath.Join(dir, "wandersort.log"),
	}}
	if err := a.runReset(newResetTestCmd(t, true)); err == nil {
		t.Fatal("runReset with no database on disk must fail")
	}
}

func TestRunResetCancelledWithoutYes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".wandersort.db")
	seedDB(t, dbPath)

	// stdin answers "n" — the non-tui confirm prompt's decline path.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	realStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = realStdin }()

	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		AppDBPath: dbPath,
		LogFile:   filepath.Join(dir, "wandersort.log"),
	}}
	if err := a.runReset(newResetTestCmd(t, false)); err == nil {
		t.Fatal("declining the confirm prompt must cancel the reset")
	}
}

func TestRunResetYesWipesDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".wandersort.db")
	seedDB(t, dbPath)

	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		AppDBPath: dbPath,
		LogFile:   filepath.Join(dir, "wandersort.log"),
	}}
	if err := a.runReset(newResetTestCmd(t, true)); err != nil {
		t.Fatalf("runReset: %v", err)
	}
}

// TestConfirmResetPlainPrompt exercises the --plain y/N stdin path directly,
// both answers, without going through the full runReset flow.
func TestConfirmResetPlainPrompt(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.WriteString(tt.input); err != nil {
				t.Fatal(err)
			}
			w.Close()
			realStdin := os.Stdin
			os.Stdin = r
			defer func() { os.Stdin = realStdin }()

			a := &app{}
			cmd := &cobra.Command{Use: "x"}
			cmd.Flags().Bool(flagPlain, true, "")
			if err := cmd.Flags().Set(flagPlain, "true"); err != nil {
				t.Fatal(err)
			}
			if got := a.confirmReset(cmd); got != tt.want {
				t.Errorf("confirmReset(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// seedDB opens and immediately closes a real app database at dbPath so
// runReset's initial os.Stat check finds a database to reset.
func seedDB(t *testing.T, dbPath string) {
	t.Helper()
	d, err := db.New(context.Background(), dbPath, db.AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}
