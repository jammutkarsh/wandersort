package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const pickPrompt = "Choose a folder with photos or videos"

// pickerCommand is the OS folder dialog's command line, or false when this
// system has none installed.
func pickerCommand() (argv []string, ok bool) {
	switch runtime.GOOS {
	case "darwin":
		return []string{"osascript", "-e", `POSIX path of (choose folder with prompt "` + pickPrompt + `")`}, true
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; ` +
			`$d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = '` + pickPrompt + `'; ` +
			`if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath }`
		return []string{"powershell", "-NoProfile", "-STA", "-Command", script}, true
	}
	for _, argv := range [][]string{
		{"zenity", "--file-selection", "--directory", "--title=" + pickPrompt},
		{"kdialog", "--getexistingdirectory", ".", "--title", pickPrompt},
	} {
		if _, err := exec.LookPath(argv[0]); err == nil {
			return argv, true
		}
	}
	return nil, false
}

// CanPickFolder reports whether this system has a folder dialog to open.
func CanPickFolder() bool {
	_, ok := pickerCommand()
	return ok
}

// PickFolder opens the OS folder dialog and returns the folder chosen, or ""
// when the dialog was closed without one.
func PickFolder(ctx context.Context) (string, error) {
	argv, ok := pickerCommand()
	if !ok {
		return "", fmt.Errorf("no folder dialog on this system")
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// every dialog tool exits non-zero when the user cancels it
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil {
			return "", nil
		}
		return "", fmt.Errorf("folder dialog: %w", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", nil
	}
	return filepath.Clean(dir), nil
}
