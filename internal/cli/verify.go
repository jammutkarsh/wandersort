// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/verify"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check that every file in the library is still what was recorded",
		Long: `Re-checks the library against its own records: every file that was copied
or moved in is still there, still the right size and — with --full — still
hashes to what it did when it was scanned. Also asks SQLite whether the
database holding your folder structure is sound, and reports any leftover
temp files a crashed transfer left behind.

Nothing is changed or deleted. A file that no longer matches is reported
here and kept in the library's error list, so 'wandersort issue' carries it.`,
		Example: `# Quick pass: is everything still there, at the right size?
wandersort verify

# Full pass: re-read every file and compare it with its scan
wandersort verify --full`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runVerify(cmd)
		},
	}

	cmd.Flags().Bool(flagFull, false, "Re-read every file and compare its contents with the hash from the scan")
	return cmd
}

func (a *app) runVerify(cmd *cobra.Command) error {
	full, _ := cmd.Flags().GetBool(flagFull)

	if !a.libraryExists() {
		return fmt.Errorf("no library found at %s — run 'wandersort scan' first", a.Config.OutputDir())
	}

	ctx := context.Background()
	if err := a.openLibrary(ctx); err != nil {
		return err
	}
	defer a.closeDBs()

	rep, err := verify.Run(ctx, a.AppDB, a.Log, a.Config.OutputDir(), verify.Options{Full: full})
	if err != nil {
		return err
	}
	return reportVerify(rep, full)
}

// reportVerify prints what the check found. Every failing file is named on
// screen, not only in the log: a list of counts is not something a person can
// act on, and these are their photos.
func reportVerify(rep verify.Report, full bool) error {
	if rep.Checked == 0 {
		fmt.Fprintln(os.Stderr, "Nothing in the library yet — run 'wandersort execute' to put files in it.")
		return nil
	}
	if rep.Sound() {
		depth := "present and the right size"
		if full {
			depth = "byte-for-byte what they were"
		}
		fmt.Fprintln(os.Stderr, tui.OK.Render(fmt.Sprintf("All %d files are %s.", rep.Checked, depth)))
		return nil
	}

	for _, p := range rep.Problems {
		fmt.Fprintf(os.Stderr, "%s %s\n    %s\n", tui.Attn.Render("✗"), p.Path, p.Detail)
	}
	if rep.Database != "ok" {
		fmt.Fprintf(os.Stderr, "%s the database holding your folder structure is damaged: %s\n",
			tui.Attn.Render("✗"), rep.Database)
		fmt.Fprintln(os.Stderr, "    'wandersort recover' restores it from the backup taken before the last transfer.")
	}
	if len(rep.Strays) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d leftover temp file(s) from an interrupted transfer, safe to delete:\n", len(rep.Strays))
		for _, s := range rep.Strays {
			fmt.Fprintf(os.Stderr, "    %s\n", s)
		}
	}
	if len(rep.Problems) == 0 {
		return nil // strays alone are untidy, not a failure
	}
	if !full {
		fmt.Fprintln(os.Stderr, "\nRun 'wandersort verify --full' to check the contents of the rest.")
	}
	return fmt.Errorf("%d of %d files do not match the library's records", len(rep.Problems), rep.Checked)
}
