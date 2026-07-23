package config

import (
	"os"
	"strings"
	"testing"
)

func TestEnsureGlobalConfigFileWritesTemplateOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := EnsureGlobalConfigFile()
	if err != nil {
		t.Fatalf("EnsureGlobalConfigFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != configTemplate {
		t.Fatalf("wrote unexpected content:\n%s", data)
	}

	// a second call must not clobber a file that's since been hand-edited
	if err := os.WriteFile(path, []byte(configTemplate+"\nworkers: 8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureGlobalConfigFile(); err != nil {
		t.Fatalf("EnsureGlobalConfigFile (existing): %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "workers: 8") {
		t.Error("EnsureGlobalConfigFile overwrote an existing file")
	}
}

func TestLoadGlobalOnMissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	g, err := LoadGlobal()
	if err != nil || g != (Global{}) {
		t.Fatalf("LoadGlobal on missing file = (%+v, %v), want (zero value, nil)", g, err)
	}
}

func TestSaveAnchorsPreservesRestOfFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := SaveAnchors("Delhi", ""); err != nil {
		t.Fatalf("SaveAnchors: %v", err)
	}
	g, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if g.Anchors.Home != "Delhi" || g.Anchors.Work != "" {
		t.Fatalf("g.Anchors = %+v, want {Delhi, \"\"}", g.Anchors)
	}

	// the template's explanatory comments and commented-out keys must survive
	path, err := GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# output-path:") {
		t.Errorf("SaveAnchors dropped the template's comments:\n%s", data)
	}

	// setting work later must not disturb the home value already saved
	if err := SaveAnchors("", "Gurugram"); err != nil {
		t.Fatalf("SaveAnchors (work only): %v", err)
	}
	g, err = LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if g.Anchors.Home != "Delhi" || g.Anchors.Work != "Gurugram" {
		t.Fatalf("g.Anchors = %+v, want {Delhi, Gurugram}", g.Anchors)
	}
}
