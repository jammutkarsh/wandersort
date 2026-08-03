// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestIndent(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"single line", "hello", "  hello"},
		{"multi line", "a\nb", "  a\n  b"},
		// a blank line must stay blank, not become two trailing spaces —
		// that would be invisible in a terminal but show up as a diff noise
		// magnet in any snapshot test or line-based diff.
		{"blank line untouched", "a\n\nb", "  a\n\n  b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := indent(tt.in); got != tt.want {
				t.Errorf("indent(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSection(t *testing.T) {
	var b strings.Builder
	section(&b, "TITLE", "body")
	got := b.String()
	if !strings.Contains(got, "TITLE") || !strings.Contains(got, "body") {
		t.Errorf("section output = %q, want it to contain title and body", got)
	}
	if !strings.HasPrefix(got, "\n") {
		t.Errorf("section must lead with a blank line to separate it from the previous section, got %q", got)
	}
}

func TestRenderFlags(t *testing.T) {
	usage := "  -o, --output-path string   Output directory   \n  --workers int          Worker count\n"
	got := renderFlags(usage)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("renderFlags produced %d lines, want 2", len(lines))
	}
	for _, l := range lines {
		if strings.HasSuffix(l, " ") {
			t.Errorf("renderFlags left trailing whitespace on %q", l)
		}
	}
}

// TestSetCustomHelpRuns is a smoke test over the real help output for a
// runnable leaf command and a command with subcommands — the two branches
// setCustomHelp switches USAGE rendering on.
func TestSetCustomHelpRuns(t *testing.T) {
	child := &cobra.Command{
		Use:   "leaf",
		Short: "a leaf command",
		Long:  "A longer description of the leaf command.",
		Example: `wandersort leaf
wandersort leaf --flag`,
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
		Annotations: map[string]string{
			"env": "SOME_ENV_VAR affects this command.",
		},
	}
	child.Flags().String("flag", "", "a flag")

	root := &cobra.Command{Use: "root", Short: "root command"}
	root.AddCommand(child)
	setCustomHelp(root)
	setCustomHelp(child)

	// cobra's HelpFunc here prints via fmt.Print (stdout), not cmd.OutOrStdout —
	// redirect real stdout so a passing test run stays quiet.
	realStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = realStdout }()

	for _, cmd := range []*cobra.Command{root, child} {
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.Help() // must not panic on either the runnable-leaf or has-subcommands branch
	}
	w.Close()
	os.Stdout = realStdout
	io.Copy(io.Discard, r)
}
