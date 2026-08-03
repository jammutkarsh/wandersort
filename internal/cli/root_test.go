// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/spf13/cobra"
)

// TestFlagHelpers pins the ".Changed" gate: an unset flag must read back as the
// zero/Unset value, not whatever GetString/GetInt/GetBool default to — that
// distinction is what lets config.Resolve tell "not passed" from "passed as
// false/0" and fall through to the env/file layers underneath.
func TestFlagHelpers(t *testing.T) {
	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{Use: "x"}
		cmd.Flags().String("output-path", "", "")
		cmd.Flags().Int("workers", 0, "")
		cmd.Flags().Bool("collapse-levels", false, "")
		return cmd
	}

	t.Run("unset flags read as zero values", func(t *testing.T) {
		cmd := newCmd()
		if got := flagStr(cmd, "output-path"); got != "" {
			t.Errorf("flagStr(unset) = %q, want empty", got)
		}
		if got := flagInt(cmd, "workers"); got != 0 {
			t.Errorf("flagInt(unset) = %d, want 0", got)
		}
		if got := flagBool(cmd, "collapse-levels"); got != config.Unset {
			t.Errorf("flagBool(unset) = %v, want config.Unset", got)
		}
	})

	t.Run("changed flags read back the set value", func(t *testing.T) {
		cmd := newCmd()
		if err := cmd.Flags().Set("output-path", "/tmp/out"); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Flags().Set("workers", "4"); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Flags().Set("collapse-levels", "false"); err != nil {
			t.Fatal(err)
		}
		if got := flagStr(cmd, "output-path"); got != "/tmp/out" {
			t.Errorf("flagStr(changed) = %q, want /tmp/out", got)
		}
		if got := flagInt(cmd, "workers"); got != 4 {
			t.Errorf("flagInt(changed) = %d, want 4", got)
		}
		// explicit false must resolve to config.False, not config.Unset — an
		// explicit "--collapse-levels=false" must be able to override a
		// true default, which config.Unset can never do.
		if got := flagBool(cmd, "collapse-levels"); got != config.False {
			t.Errorf("flagBool(changed=false) = %v, want config.False", got)
		}
	})

	t.Run("flag not registered on the command", func(t *testing.T) {
		cmd := &cobra.Command{Use: "x"}
		if got := flagStr(cmd, "missing"); got != "" {
			t.Errorf("flagStr(missing) = %q, want empty", got)
		}
		if got := flagBool(cmd, "missing"); got != config.Unset {
			t.Errorf("flagBool(missing) = %v, want config.Unset", got)
		}
	})
}

// TestNewRootCmdWiresSubcommands is a smoke test that every subcommand is
// actually registered — a missed AddCommand silently drops a whole command
// from the CLI with no compiler error to catch it.
func TestNewRootCmdWiresSubcommands(t *testing.T) {
	a := &app{}
	root := a.newRootCmd()
	want := []string{"config", "scan", "review", "issue", "reset"}
	for _, name := range want {
		if _, _, err := root.Find([]string{name}); err != nil {
			t.Errorf("root command missing %q: %v", name, err)
		}
	}
}
