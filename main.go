// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"

	"github.com/jammutkarsh/wandersort/internal/cli"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// Set at build time via -ldflags (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	v := fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
	if err := cli.Execute(v); err != nil {
		fmt.Fprintln(os.Stderr, tui.Bad.Render("Error:"), err)
		os.Exit(1)
	}
}
