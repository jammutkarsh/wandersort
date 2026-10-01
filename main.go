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
