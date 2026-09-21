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

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newAdminClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Delete the cached preview copies",
		Long: `Deletes the preview copies WanderSort makes when you peek into a folder
while organising. They are only a cache — peeking again re-creates them — so
nothing here asks, and nothing in your library is touched.

To delete the library's own data, that is 'wandersort admin db --reset'.`,
		Example: `wandersort admin clear`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runAdminClear()
		},
	}
}

func (a *app) runAdminClear() error {
	if err := review.CleanPreviews(); err != nil {
		return fmt.Errorf("could not remove the preview copies: %w", err)
	}
	fmt.Fprintln(os.Stderr, tui.OK.Render("Preview cache cleared."))
	fmt.Fprintln(os.Stderr, tui.FaintTxt.Render("To delete the library's data too, run 'wandersort admin db --reset'."))
	return nil
}
