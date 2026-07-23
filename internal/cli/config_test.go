package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBadYAMLWarnsAndFallsBackToDefaults covers the escape hatch: a config
// file that doesn't parse must not stop the command. Every setting in it is
// optional, and failing hard would mean a stray tab locks the user out of
// every command — including the one that opens the file to fix it.
func TestBadYAMLWarnsAndFallsBackToDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	if err := os.MkdirAll(filepath.Join(home, ".wandersort"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".wandersort", "config.yaml")
	if err := os.WriteFile(path, []byte("workers: 4\n\tbad: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	warning, err := loadGlobalConfigFile()
	if err != nil {
		t.Fatalf("bad YAML must not be a fatal error, got %v", err)
	}
	if warning == "" {
		t.Error("expected a warning naming the unparseable file")
	}
}

// TestValidYAMLIsStillApplied guards the other direction: the warning path
// must not have broken normal config loading.
func TestValidYAMLIsStillApplied(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".wandersort"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".wandersort", "config.yaml"),
		[]byte("workers: 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	warning, err := loadGlobalConfigFile()
	if err != nil || warning != "" {
		t.Fatalf("valid config: err=%v warning=%q", err, warning)
	}
	if got := v.GetInt(flagWorkers); got != 7 {
		t.Errorf("workers = %d, want 7 from the config file", got)
	}
}
