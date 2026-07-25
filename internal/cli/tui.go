// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"os"

	"golang.org/x/term"
)

// tuiEnabled decides whether a command renders the full-screen TUI or falls
// back to plain line logging. Plain when --plain is set or stderr isn't a
// terminal (piped/redirected). The TUI draws to stderr, matching the review
// TUI, so stdout stays clean for piping.
func (a *App) tuiEnabled() bool {
	if v.GetBool(flagPlain) {
		return false
	}
	return term.IsTerminal(int(os.Stderr.Fd()))
}
