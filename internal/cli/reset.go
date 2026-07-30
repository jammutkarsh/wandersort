// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
)

func (a *app) newResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Delete all wandersort scan data",
		Long: `Clears the wandersort database — scan history, file index, and duplicate
results. This is irreversible; you are prompted for confirmation unless --yes
is given.`,
		Example: `# Delete all scan data (prompts for confirmation)
wandersort reset

# Skip the confirmation prompt
wandersort reset --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReset(cmd)
		},
	}

	cmd.Flags().Bool(flagYes, false, "Skip confirmation prompt")
	return cmd
}

func (a *app) runReset(cmd *cobra.Command) error {
	if _, err := os.Stat(a.Config.AppDBPath); os.IsNotExist(err) {
		return fmt.Errorf("no database found — nothing to reset")
	}

	yes, _ := cmd.Flags().GetBool(flagYes)
	if !yes {
		if !a.confirmReset(cmd) {
			return fmt.Errorf("reset cancelled")
		}
	}

	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()

	ctx := context.Background()

	if err := a.initAppDB(ctx); err != nil {
		return fmt.Errorf("app db: %w", err)
	}
	defer a.closeDBs()

	if _, err := a.AppDB.ResetAll(ctx); err != nil {
		return fmt.Errorf("reset failed: %w", err)
	}

	if err := a.AppDB.Optimize(ctx); err != nil {
		a.Log.Warn("database optimization after reset failed", "error", err)
	}

	fmt.Fprintln(os.Stderr, tui.OK.Render("All wandersort data deleted."))
	return nil
}

// confirmReset asks before the irreversible wipe: a themed dialog in the
// full-screen TUI, or a plain y/N prompt when --plain / non-interactive.
func (a *app) confirmReset(cmd *cobra.Command) bool {
	if !a.isTuiEnabled(cmd) {
		fmt.Fprint(os.Stderr, tui.Attn.Render("Delete all wandersort data?")+" (y/N): ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		return input == "y" || input == "yes"
	}
	ok := false
	m := tui.NewConfirmModel(
		"Delete all wandersort data?",
		"Scan history, file index, and duplicate results — this cannot be undone.",
		&ok,
	)
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	if _, err := prog.Run(); err != nil {
		return false
	}
	return ok
}
