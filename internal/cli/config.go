package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/config"
)

func (a *App) newConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Open the global config file",
		Long: `Opens ~/.wandersort/config.yaml in $EDITOR — output-path, workers, debug,
group-by, and home/work anchors all live there, applying to every scan/serve
unless overridden by a flag or environment variable. Creates the file with
explanatory comments first if it doesn't exist yet. If $EDITOR isn't set,
prints the file instead.`,
		Example: "wandersort config",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runConfig()
		},
	}
}

func (a *App) runConfig() error {
	path, err := config.EnsureGlobalConfigFile()
	if err != nil {
		return fmt.Errorf("global config: %w", err)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "$EDITOR not set — printing %s:\n\n", path)
		fmt.Print(string(data))
		return nil
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", editor, err)
	}
	return nil
}
