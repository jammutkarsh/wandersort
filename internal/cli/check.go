// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/verify"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check that every file in the library is still what was recorded",
		Long: `Re-checks the library against its own records: every file that was copied
or moved in is still there, still the right size and — with --full — still
hashes to what it did when it was scanned. Also asks SQLite whether the
database holding your folder structure is sound, and reports any leftover
temp files a crashed transfer left behind.

No file is changed or deleted. A file that is gone from the library is
listed and forgotten, so the next check doesn't list it again and 'wandersort
add' can bring back a copy still at a source; the database is backed up
first. A file that is there but wrong is listed and kept in the library's
error list, so 'wandersort admin report' carries it.`,
		Example: `# Quick pass: is everything still there, at the right size?
wandersort check

# Full pass: re-read every file and compare it with its scan
wandersort check --full`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runCheck(cmd)
		},
	}

	cmd.Flags().Bool(flagFull, false, "Re-read every file and compare its contents with the hash from the scan")
	return cmd
}

func (a *app) runCheck(cmd *cobra.Command) error {
	full, _ := cmd.Flags().GetBool(flagFull)

	if !a.libraryExists() {
		return fmt.Errorf("no library found at %s — run 'wandersort add' first", a.Config.OutputDir())
	}

	ctx, cancel := interruptible()
	defer cancel()
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
// act on, and these are their photos. Files are grouped by what is wrong with
// them, one heading per kind and one line per file.
func reportVerify(rep verify.Report, full bool) error {
	if rep.Checked == 0 {
		fmt.Fprintln(os.Stderr, "Nothing in the library yet — run 'wandersort execute' to put files in it.")
		return nil
	}
	if len(rep.Forgotten) > 0 {
		fmt.Fprintf(os.Stderr, "%s %d files are not in the library any more, so the library no longer records them — 'wandersort add' brings back any copy still at a source:\n",
			tui.Attn.Render("✗"), len(rep.Forgotten))
		for _, p := range rep.Forgotten {
			fmt.Fprintf(os.Stderr, "    %s\n", p)
		}
	}
	if rep.Sound() {
		if len(rep.Forgotten) > 0 {
			return nil
		}
		depth := "present and the right size"
		if full {
			depth = "byte-for-byte what they were"
		}
		fmt.Fprintln(os.Stderr, tui.OK.Render(fmt.Sprintf("All %d files are %s.", rep.Checked, depth)))
		return nil
	}

	for _, g := range groupProblems(rep.Problems) {
		fmt.Fprintf(os.Stderr, "%s %d %s:\n", tui.Attn.Render("✗"), len(g.problems), g.heading)
		for _, p := range g.problems {
			if g.detail {
				fmt.Fprintf(os.Stderr, "    %s — %s\n", p.Path, p.Detail)
			} else {
				fmt.Fprintf(os.Stderr, "    %s\n", p.Path)
			}
		}
	}
	if rep.Database != "ok" {
		fmt.Fprintf(os.Stderr, "%s the database holding your folder structure is damaged: %s\n",
			tui.Attn.Render("✗"), rep.Database)
		fmt.Fprintln(os.Stderr, "    'wandersort admin db --restore' restores it from the backup taken before the last transfer.")
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
		fmt.Fprintln(os.Stderr, "\nRun 'wandersort check --full' to check the contents of the rest.")
	}
	return fmt.Errorf("%d of %d files do not match the library's records", len(rep.Problems), rep.Checked)
}

// problemGroup is every problem of one kind, under the heading that names it.
// detail says whether each file's own detail is worth a line: a size differs
// per file, while "contents changed" would only repeat the heading (and two
// long hashes).
type problemGroup struct {
	heading  string
	detail   bool
	problems []verify.Problem
}

// groupProblems sorts problems into groups by kind, in the order kinds first
// appear, keeping each group's files in check order.
func groupProblems(problems []verify.Problem) []problemGroup {
	var groups []problemGroup
	index := map[string]int{}
	for _, p := range problems {
		i, ok := index[p.Kind]
		if !ok {
			heading, detail := problemHeading(p.Kind)
			i = len(groups)
			index[p.Kind] = i
			groups = append(groups, problemGroup{heading: heading, detail: detail})
		}
		groups[i].problems = append(groups[i].problems, p)
	}
	return groups
}

func problemHeading(kind string) (heading string, detail bool) {
	switch kind {
	case db.KindChecksumMismatch:
		return "files changed since they were copied in", false
	case db.KindIO, db.KindPermissionDenied:
		return "files could not be read", true
	}
	return "files are not what the library recorded", true
}
