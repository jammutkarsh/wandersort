package cli

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	commandStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	flagStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	descStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
)

func setCustomHelp(cmd *cobra.Command) {
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if c.Long != "" {
			fmt.Println(c.Long)
		} else {
			fmt.Println(c.Short)
		}

		if c.Runnable() {
			fmt.Printf("\n%s\n  %s\n", headerStyle.Render("USAGE"), c.UseLine())
		} else if c.HasAvailableSubCommands() {
			fmt.Printf("\n%s\n  %s [command]\n", headerStyle.Render("USAGE"), c.CommandPath())
		}

		if c.HasAvailableSubCommands() {
			fmt.Printf("\n%s\n", headerStyle.Render("COMMANDS"))
			for _, sub := range c.Commands() {
				if sub.IsAvailableCommand() || sub.Name() == "help" {
					fmt.Printf("  %s %s\n", commandStyle.Render(fmt.Sprintf("%-12s", sub.Name())), descStyle.Render(sub.Short))
				}
			}
		}

		if c.HasAvailableLocalFlags() {
			fmt.Printf("\n%s\n%s", headerStyle.Render("FLAGS"), flagStyle.Render(c.LocalFlags().FlagUsages()))
		}

		if c.HasAvailableInheritedFlags() {
			fmt.Printf("\n%s\n%s", headerStyle.Render("GLOBAL FLAGS"), flagStyle.Render(c.InheritedFlags().FlagUsages()))
		}

		if c.HasExample() {
			fmt.Printf("\n%s\n%s\n", headerStyle.Render("EXAMPLES"), c.Example)
		}

		fmt.Println()
	})
}
