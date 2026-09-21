// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestFlagHelpers pins the ".Changed" gate: an unset flag must read back as
// the zero value, not whatever GetString defaults to — that distinction is
// what lets --output-path tell "not passed" from "passed as empty" and fall
// through to the most recently used library underneath.
func TestFlagHelpers(t *testing.T) {
	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{Use: "x"}
		cmd.Flags().String("output-path", "", "")
		return cmd
	}

	t.Run("unset flags read as zero values", func(t *testing.T) {
		if got := flagStr(newCmd(), "output-path"); got != "" {
			t.Errorf("flagStr(unset) = %q, want empty", got)
		}
	})

	t.Run("changed flags read back the set value", func(t *testing.T) {
		cmd := newCmd()
		if err := cmd.Flags().Set("output-path", "/tmp/out"); err != nil {
			t.Fatal(err)
		}
		if got := flagStr(cmd, "output-path"); got != "/tmp/out" {
			t.Errorf("flagStr(changed) = %q, want /tmp/out", got)
		}
	})

	t.Run("flag not registered on the command", func(t *testing.T) {
		if got := flagStr(&cobra.Command{Use: "x"}, "missing"); got != "" {
			t.Errorf("flagStr(missing) = %q, want empty", got)
		}
	})
}

// TestNewRootCmdWiresSubcommands is a smoke test that every subcommand is
// actually registered — a missed AddCommand silently drops a whole command
// from the CLI with no compiler error to catch it.
func TestNewRootCmdWiresSubcommands(t *testing.T) {
	a := &app{}
	root := a.newRootCmd()
	// Subcommands too: a missed AddCommand on the admin parent drops a whole
	// group with no compiler error either.
	want := [][]string{
		{"add"},
		{"review"},
		{"execute"},
		{"check"},
		{"admin"},
		{"admin", "clear"},
		{"admin", "db"},
		{"admin", "report"},
	}
	for _, path := range want {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Errorf("command missing %q: %v", path, err)
			continue
		}
		if cmd.Name() != path[len(path)-1] {
			t.Errorf("Find(%q) landed on %q", path, cmd.CommandPath())
		}
	}
}
