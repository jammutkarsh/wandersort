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
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newExecuteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "execute",
		Short: "Copy or move approved files into the output folder",
		Long: `Transfers every file 'wandersort review' approved from its source into
<output>/<proposed folder>. Copy is the default and never touches a source
file; --move deletes each source only after its copy there is verified
complete. Safe to re-run: only still-approved rows are touched, so a run
interrupted partway picks up where it left off next time.`,
		Example: `# Copy every approved file (safe, default)
wandersort execute

# See what would happen without touching anything
wandersort execute --dry-run

# Move instead of copy — prompts for confirmation unless --yes
wandersort execute --move`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runExecute(cmd)
		},
	}

	cmd.Flags().Bool(flagMove, false, "Move files instead of copying, deleting each source once its copy is verified")
	cmd.Flags().Bool(flagDryRun, false, "Report what would be transferred without touching anything")
	cmd.Flags().Bool(flagYes, false, "Skip the confirmation prompt --move asks for")
	return cmd
}

func (a *app) runExecute(cmd *cobra.Command) error {
	move, _ := cmd.Flags().GetBool(flagMove)
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	yes, _ := cmd.Flags().GetBool(flagYes)

	if _, err := os.Stat(a.Config.AppDBPath); os.IsNotExist(err) {
		return fmt.Errorf("no database found — run 'wandersort scan' first")
	}

	// Copy never touches a source, so it never asks. Move is the one thing in
	// this codebase that can delete the user's files — it asks unless the
	// caller already said --yes (a dry run deletes nothing either way).
	if move && !dryRun && !yes && !a.confirmMove(cmd) {
		return fmt.Errorf("execute cancelled")
	}

	ctx := context.Background()
	if err := a.openLibrary(ctx); err != nil {
		return err
	}
	defer a.closeDBs()

	mode := execute.ModeCopy
	if move {
		mode = execute.ModeMove
	}
	rep, err := execute.Run(ctx, a.AppDB, a.Log, filepath.Dir(a.Config.AppDBPath), execute.Options{Mode: mode, DryRun: dryRun})
	if err != nil {
		return err
	}
	if rep.Done == 0 && rep.Failed == 0 {
		fmt.Fprintln(os.Stderr, "Nothing approved yet — run 'wandersort review' first.")
		return nil
	}
	if rep.Failed > 0 {
		return fmt.Errorf("%d file(s) failed to transfer — see the log for why", rep.Failed)
	}
	fmt.Fprintln(os.Stderr, tui.OK.Render(fmt.Sprintf("Done: %d files.", rep.Done)))
	return nil
}

// confirmMove asks before the one operation in this command that can delete
// the user's files: a themed dialog in the full-screen TUI, or a plain y/N
// prompt when --plain / non-interactive — same split as reset's.
func (a *app) confirmMove(cmd *cobra.Command) bool {
	if !a.isTuiEnabled(cmd) {
		fmt.Fprint(os.Stderr, tui.Attn.Render("Move files instead of copying?")+" (y/N): ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		return input == "y" || input == "yes"
	}
	ok := false
	m := tui.NewConfirmModel(
		"Move files instead of copying?",
		"Each source file is deleted once its copy at the output is verified complete — this cannot be undone.",
		&ok,
	)
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	if _, err := prog.Run(); err != nil {
		return false
	}
	return ok
}
