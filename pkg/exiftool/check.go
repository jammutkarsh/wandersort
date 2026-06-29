package exiftool

import (
	"fmt"
	"os/exec"
	"strings"
)

// minimumVersion is the oldest exiftool release that supports all metadata
const minimumVersion = "12.00"

// Check verifies exiftool is installed and meets the minimum version requirement.
// Returns an error with platform-specific install instructions if the check fails.
func Check() error {
	path, err := exec.LookPath("exiftool")
	if err != nil {
		return fmt.Errorf("exiftool not found in PATH: %w\n%s", err, installInstructions())
	}

	out, err := exec.Command(path, "-ver").Output()
	if err != nil {
		return fmt.Errorf("failed to run exiftool: %w", err)
	}

	version := strings.TrimSpace(string(out))
	if err := checkVersion(version); err != nil {
		return fmt.Errorf("%w\n%s", err, installInstructions())
	}

	return nil
}

// checkVersion returns error if current is below minimumVersion.
func checkVersion(current string) error {
	if current < minimumVersion {
		return fmt.Errorf("exiftool version %s is below minimum required %s", current, minimumVersion)
	}
	return nil
}