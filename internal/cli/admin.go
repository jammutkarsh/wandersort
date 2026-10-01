package cli

import "github.com/spf13/cobra"

// newAdminCmd groups commands about the library itself rather than its photos.
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
