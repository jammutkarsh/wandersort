package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	commandStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	flagStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	descStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// setCustomHelp installs a help renderer modelled on git/gh/docker: a short
// description, then USAGE, EXAMPLES, COMMANDS, FLAGS, ENVIRONMENT sections, and
// a footer pointing to per-command help.
func setCustomHelp(cmd *cobra.Command) {
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		var b strings.Builder

		if c.Long != "" {
			fmt.Fprintln(&b, strings.TrimSpace(c.Long))
		} else if c.Short != "" {
			fmt.Fprintln(&b, c.Short)
		}

		if c.Runnable() {
			section(&b, "USAGE", "  "+c.UseLine())
		} else if c.HasAvailableSubCommands() {
			section(&b, "USAGE", "  "+c.CommandPath()+" [command]")
		}

		if c.HasExample() {
			section(&b, "EXAMPLES", indent(strings.TrimSpace(c.Example)))
		}

		if c.HasAvailableSubCommands() {
			var rows strings.Builder
			for _, sub := range c.Commands() {
				if sub.IsAvailableCommand() || sub.Name() == "help" {
					fmt.Fprintf(&rows, "  %s %s\n",
						commandStyle.Render(fmt.Sprintf("%-12s", sub.Name())),
						descStyle.Render(sub.Short))
				}
			}
			section(&b, "COMMANDS", strings.TrimRight(rows.String(), "\n"))
		}

		if c.HasAvailableLocalFlags() {
			section(&b, "FLAGS", renderFlags(c.LocalFlags().FlagUsages()))
		}

		if c.HasAvailableInheritedFlags() {
			section(&b, "GLOBAL FLAGS", renderFlags(c.InheritedFlags().FlagUsages()))
		}

		if env := c.Annotations["env"]; env != "" {
			section(&b, "ENVIRONMENT", indent(strings.TrimSpace(env)))
		}

		if c.HasAvailableSubCommands() {
			fmt.Fprintf(&b, "\n%s\n", dimStyle.Render(
				fmt.Sprintf("Use \"%s [command] --help\" for more information about a command.", c.CommandPath()),
			))
		}

		fmt.Print(b.String())
	})
}

// section writes a blank line, a styled header, then body.
func section(b *strings.Builder, title, body string) {
	fmt.Fprintf(b, "\n%s\n%s\n", headerStyle.Render(title), body)
}

// indent prefixes every line with two spaces.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "  " + line
		}
	}
	return strings.Join(lines, "\n")
}

// renderFlags styles pflag's usage block line-by-line, stripping the trailing
// whitespace pflag pads each column with (rendering per line avoids lipgloss
// re-padding the block back to a rectangle).
func renderFlags(usage string) string {
	lines := strings.Split(strings.TrimRight(usage, "\n"), "\n")
	for i, line := range lines {
		lines[i] = flagStyle.Render(strings.TrimRight(line, " "))
	}
	return strings.Join(lines, "\n")
}
