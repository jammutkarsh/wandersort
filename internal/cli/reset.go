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

	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/style"
	"github.com/spf13/cobra"
)

func (a *App) newResetCmd() *cobra.Command {
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
			return a.runReset()
		},
	}

	cmd.Flags().Bool(flagYes, false, "Skip confirmation prompt")
	return cmd
}

func (a *App) runReset() error {
	if _, err := os.Stat(a.Config.AppDBPath); os.IsNotExist(err) {
		return fmt.Errorf("no database found — nothing to reset")
	}

	if !v.GetBool(flagYes) {
		if !confirmReset() {
			return fmt.Errorf("reset cancelled")
		}
	}

	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()

	ctx := context.Background()

	if err := a.InitAppDB(ctx); err != nil {
		return fmt.Errorf("app db: %w", err)
	}
	defer a.Close()

	if _, err := a.AdminService().Reset(ctx); err != nil {
		return fmt.Errorf("reset failed: %w", err)
	}

	if err := a.AppDB.Optimize(ctx); err != nil {
		a.Log.Warn("database optimization after reset failed", "error", err)
	}

	fmt.Fprintln(os.Stderr, style.Success.Render("All wandersort data deleted."))
	return nil
}

func confirmReset() bool {
	fmt.Fprint(os.Stderr, style.Warn.Render("Delete all wandersort data?")+" (y/N): ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}
