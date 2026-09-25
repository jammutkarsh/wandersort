// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// TestReferencedCommandsExist fails when a message or help text sends the user
// to a command that doesn't exist — 'wandersort organise', 'wandersort
// recover' and 'wandersort scan' all shipped that way after renames. It reads
// every quoted 'wandersort …' and every help-example line starting with
// wandersort in the repo's Go source, and resolves it against the real tree.
func TestReferencedCommandsExist(t *testing.T) {
	root := (&app{}).newRootCmd()
	quoted := regexp.MustCompile(`'wandersort((?: [a-z][a-z-]*)+)`)
	example := regexp.MustCompile(`(?m)^wandersort((?: [a-z][a-z-]*)+)`)

	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, re := range []*regexp.Regexp{quoted, example} {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				words := strings.Fields(m[1])
				cmd, rest, err := root.Find(words)
				if err != nil || cmd == root || len(rest) > 0 {
					t.Errorf("%s: 'wandersort %s' is not a command", p, strings.Join(words, " "))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
