//go:build !windows

package exiftool

import "testing"

// processTree is pid alone: exiftool is one process on these systems.
func processTree(_ *testing.T, pid int) []int { return []int{pid} }
