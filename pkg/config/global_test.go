// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

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

func TestSaveHomeWorkPreservesRestOfFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := SaveHomeWork("Delhi", ""); err != nil {
		t.Fatalf("SaveHomeWork: %v", err)
	}
	g, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if g.HomeWork.Home != "Delhi" || g.HomeWork.Work != "" {
		t.Fatalf("g.HomeWork = %+v, want {Delhi, \"\"}", g.HomeWork)
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
		t.Errorf("SaveHomeWork dropped the template's comments:\n%s", data)
	}

	// setting work later must not disturb the home value already saved
	if err := SaveHomeWork("", "Gurugram"); err != nil {
		t.Fatalf("SaveHomeWork (work only): %v", err)
	}
	g, err = LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if g.HomeWork.Home != "Delhi" || g.HomeWork.Work != "Gurugram" {
		t.Fatalf("g.HomeWork = %+v, want {Delhi, Gurugram}", g.HomeWork)
	}
}
