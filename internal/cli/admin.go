// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import "github.com/spf13/cobra"

// newAdminCmd groups the commands that are about the library itself rather
// than about the photos in it. Nothing here is part of organising a library,
// which is why none of it sits at the top level next to add/organise/execute/check:
// a user who never has a problem never types `admin`.
func (a *app) newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Look after the library itself: its database, caches and reports",
		Long: `Commands for the library rather than the photos in it — restoring or
resetting its database, clearing cached previews, and packaging logs for a bug
report. Running a library never requires any of them.`,
		Example: `wandersort admin clear
wandersort admin db --restore
wandersort admin report`,
	}
	cmd.AddCommand(a.newAdminClearCmd())
	cmd.AddCommand(a.newAdminDBCmd())
	cmd.AddCommand(a.newAdminReportCmd())
	return cmd
}
