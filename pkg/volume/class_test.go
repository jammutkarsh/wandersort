//go:build darwin || linux

package volume

import (
	"path/filepath"
	"testing"
)

func TestClassForPath(t *testing.T) {
	// The class is advisory: whatever this machine is, resolving it must not
	// panic and must not report an error to the caller
	if got := ClassForPath(t.TempDir()); got.String() == "" {
		t.Fatalf("ClassForPath returned a class with no name: %d", got)
	}

	if got := ClassForPath("/definitely/not/a/real/path/xyzzy"); got != ClassUnknown {
		t.Errorf("ClassForPath(bogus) = %v, want %v", got, ClassUnknown)
	}
}

func TestClassString(t *testing.T) {
	tests := []struct {
		class Class
		want  string
	}{
		{ClassUnknown, "unknown"},
		{ClassRotational, "rotational"},
		{ClassSolidState, "solid-state"},
		{ClassRemovable, "removable"},
		{ClassNetwork, "network"},
		{Class(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.class.String(); got != tt.want {
			t.Errorf("Class(%d).String() = %q, want %q", tt.class, got, tt.want)
		}
	}
}

// A library folder that doesn't exist yet is judged by its nearest existing
// ancestor; a local temp folder is never a network mount.
func TestIsNetworkClimbsToAnExistingAncestor(t *testing.T) {
	for _, p := range []string{t.TempDir(), filepath.Join(t.TempDir(), "not", "made", "yet")} {
		if IsNetwork(p) {
			t.Errorf("IsNetwork(%q) = true, want false for a local folder", p)
		}
	}
}
