// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// GlobalConfigFileName lives under ~/.wandersort — settings that make sense
// across every library (not tied to one --output-path), like the home/work
// places and a default output path so it doesn't have to be typed every run.
const GlobalConfigFileName = "config.yaml"

// HomeWork are the confirmed home/work town names, set via `wandersort config`
// and reused by every scan (see cli.syncHomeWorkFromConfig) instead of being
// tied to one library's database. Photos taken at these everyday places are
// grouped by date only, not location (there's no point sorting your home
// photos into a "home town" folder).
type HomeWork struct {
	Home string `yaml:"home,omitempty"`
	Work string `yaml:"work,omitempty"`
}

// Global is the on-disk shape of ~/.wandersort/config.yaml. `wandersort
// config` is the only thing that writes it (whole-file marshal — no comments
// to preserve), and the CLI reads the same keys through viper so flags and env
// vars still layer over them (see internal/cli/root.go's applyOverrides).
type Global struct {
	OutputPath            string   `yaml:"output-path,omitempty"`
	Workers               int      `yaml:"workers,omitempty"`
	Debug                 bool     `yaml:"debug"`
	Rules                 []string `yaml:"rules,omitempty"`
	CollapseLevels        bool     `yaml:"collapse-levels"`
	HomeWorkDateOnly      bool     `yaml:"home-work-date-only"`
	MergeSameLocationDays bool     `yaml:"merge-same-location-days"`
	HomeWork              HomeWork `yaml:"home-work,omitempty"`
}

// GlobalConfigPath returns ~/.wandersort/config.yaml, regardless of whether it exists yet.
func GlobalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, ".wandersort", GlobalConfigFileName), nil
}

// EnsureGlobalConfigFile creates an empty ~/.wandersort/config.yaml if it
// doesn't exist yet, and always returns its path. Called on every CLI
// invocation (see cli.loadGlobalConfigFile) so viper always has a file to
// read. It starts empty on purpose: every setting has a built-in default, and
// `wandersort config` is what fills the file in.
func EnsureGlobalConfigFile() (string, error) {
	path, err := GlobalConfigPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// LoadGlobal reads the global config file. A missing file is not an error —
// it just means nothing has been configured globally yet.
func LoadGlobal() (Global, error) {
	path, err := GlobalConfigPath()
	if err != nil {
		return Global{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Global{}, nil
	}
	if err != nil {
		return Global{}, fmt.Errorf("read global config: %w", err)
	}
	var g Global
	if err := yaml.Unmarshal(data, &g); err != nil {
		return Global{}, fmt.Errorf("parse global config: %w", err)
	}
	return g, nil
}

// SaveGlobal writes the whole config file from g, replacing whatever was there.
// The config TUI always submits every setting, so there is nothing to merge.
func SaveGlobal(g Global) error {
	path, err := GlobalConfigPath()
	if err != nil {
		return err
	}
	out, err := yaml.Marshal(g)
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write global config: %w", err)
	}
	return nil
}
