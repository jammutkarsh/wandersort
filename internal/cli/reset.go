package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jammutkarsh/wandersort/internal/api/admin"
	"github.com/spf13/cobra"
)

var (
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
)

func (a *App) newResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Delete all wandersort scan data",
		Long:  "Clears the wandersort database — scan history, file index, and duplicate results.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReset()
		},
	}

	cmd.Flags().Bool(flagYes, false, "Skip confirmation prompt")
	return cmd
}

func (a *App) runReset() error {
	if dbMissing(a.Config.AppDBPath) {
		return fmt.Errorf("no database found — nothing to reset")
	}

	if !v.GetBool(flagYes) {
		if !confirmReset() {
			return fmt.Errorf("reset cancelled")
		}
	}

	lock, err := AcquireLock(outputDir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Unlock()

	ctx := context.Background()

	if _, err := admin.RunReset(ctx, a.Log, a.Config.AppDBPath); err != nil {
		return fmt.Errorf("reset failed: %w", err)
	}

	fmt.Fprintln(os.Stderr, successStyle.Render("All wandersort data deleted."))
	return nil
}

func confirmReset() bool {
	fmt.Fprint(os.Stderr, warnStyle.Render("Delete all wandersort data?")+" (y/N): ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}
