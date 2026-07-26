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

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, tui.Bad.Render("Error:"), err)
		os.Exit(1)
	}
}
