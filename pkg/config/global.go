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

// HomeWork are the confirmed home/work town names, set once via `wandersort
// setup` and reused by every scan (see cli.syncHomeWorkFromConfig) instead of
// being tied to one library's database. Photos taken at these everyday places
// are grouped by date only, not location (there's no point sorting your home
// photos into a "home town" folder).
type HomeWork struct {
	Home string `yaml:"home,omitempty"`
	Work string `yaml:"work,omitempty"`
}

// Global is the on-disk shape of ~/.wandersort/config.yaml, for reading.
// output-path/workers/debug/rules aren't modeled here — they're plain
// viper keys the CLI layer reads directly (see internal/cli/root.go's
// applyOverrides), never written back by this package. Only HomeWork is ever
// written by our own code (SaveHomeWork), which is why it's the only field
// with a matching write path below.
type Global struct {
	OutputPath string   `yaml:"output-path,omitempty"`
	HomeWork   HomeWork `yaml:"home-work,omitempty"`
}

// configTemplate seeds a fresh config.yaml with every supported key
// documented, most of them commented out (they already have a sensible
// built-in default — see config.Defaults). home-work.home/work are the
// exception: left as real, empty, top-level keys because SaveHomeWork edits
// them in place, so the on-disk comments explaining them survive that edit
// (only the two scalar values under home-work: are ever touched).
const configTemplate = `# WanderSort global config.
# Applies to every scan/serve unless overridden by a command-line flag or an
# environment variable. Precedence: flag > env > this file > built-in default.
# Run 'wandersort config' any time to reopen this file in $EDITOR, or
# 'wandersort config --print' to print it instead.

# Default output directory (DB + logs). Same as --output-path / -o.
# output-path: ~/WanderSortLibrary

# Concurrent worker count. Same as --workers / -w.
# workers: 4

# Verbose logging. Same as --debug.
# debug: false

# Folder levels below Year/Month for new proposals, in the order they nest:
# location, date, device, orientation, media — or "none" for flat Year/Month.
# Same as scan/serve's --rules, or change it per-session from the review
# TUI's [L] key. A full Year/Month/Day/Location/Device/Orientation/Media
# hierarchy is: [date, location, device, orientation, media]
# rules: [date, location]

# Drop a device/orientation/media folder level that turns out to have only one
# value in your whole library — no point clicking through "iPhone/Vertical" when
# every photo is one. Day and location folders are always kept. Set to false to
# always get the full nesting.
# collapse-levels: true

# Group photos taken at your home/work places by date only, not location — no
# point sorting your everyday home photos into a town folder. Set to false to
# instead fold nearby suburbs into that town's own folder.
# home-work-date-only: true

# Collapse consecutive days at the same location into one dated range folder
# (Aug/02/Goa + 03/Goa + 04/Goa -> Aug/02_04/Goa). Only applies when a date
# level sits above location. Set to false to keep every day separate.
# merge-same-location-days: true

# Your everyday places (set interactively via 'wandersort setup'). Photos taken
# at home or work are grouped by date only, not location — no point sorting your
# home photos into a "home town" folder. Set interactively; a blank value means
# "not set".
home-work:
  home: ""
  work: ""
`

// GlobalConfigPath returns ~/.wandersort/config.yaml, regardless of whether it exists yet.
func GlobalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, ".wandersort", GlobalConfigFileName), nil
}

// EnsureGlobalConfigFile creates ~/.wandersort/config.yaml from configTemplate
// if it doesn't exist yet, and always returns its path. Called on every CLI
// invocation (see cli.loadGlobalConfigFile) so the file — and its
// explanatory comments — is there for a user to find and edit from the start,
// not something they have to know to create first.
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
	if err := os.WriteFile(path, []byte(configTemplate), 0o644); err != nil {
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

// SaveHomeWork sets home-work.home/home-work.work in the global config file,
// creating it from configTemplate first if it doesn't exist. It edits the
// YAML node tree in place rather than re-marshaling the whole struct, so
// every other key — output-path, workers, comments, anything the user typed
// by hand — survives untouched. An empty name leaves that key alone (use
// LoadGlobal first to know what's already set).
func SaveHomeWork(home, work string) error {
	path, err := EnsureGlobalConfigFile()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read global config: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse global config: %w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("global config: expected a YAML mapping at the top level")
	}

	hw := mapGetOrCreateMapping(root, "home-work")
	if home != "" {
		mapSetString(hw, "home", home)
	}
	if work != "" {
		mapSetString(hw, "work", work)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write global config: %w", err)
	}
	return nil
}

// Settings are the global config values the setup wizard writes back. A nil/
// empty field is left untouched in the file (skip), so the wizard only records
// what the user actually chose. Bools are pointers so a deliberate `false` is
// distinguishable from "unset".
type Settings struct {
	OutputPath       string
	Workers          int // 0 = skip
	Rules            []string
	Collapse         *bool
	HomeWorkDateOnly *bool
	MergeDays        *bool
	Debug            *bool
	Home, Work       string
}

// SaveSettings writes the wizard's choices into the global config file using
// the same in-place YAML-node surgery as SaveHomeWork — comments and any keys
// the wizard doesn't touch survive. Keys that don't exist yet (the template
// ships them commented out, which YAML treats as absent) are appended as real
// entries below the documentation comments.
func SaveSettings(s Settings) error {
	path, err := EnsureGlobalConfigFile()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read global config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse global config: %w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("global config: expected a YAML mapping at the top level")
	}

	if s.OutputPath != "" {
		mapSetString(root, "output-path", s.OutputPath)
	}
	if s.Workers > 0 {
		mapSetString(root, "workers", fmt.Sprintf("%d", s.Workers))
	}
	if s.Rules != nil {
		mapSetStringSlice(root, "rules", s.Rules)
	}
	if s.Collapse != nil {
		mapSetString(root, "collapse-levels", fmt.Sprintf("%t", *s.Collapse))
	}
	if s.HomeWorkDateOnly != nil {
		mapSetString(root, "home-work-date-only", fmt.Sprintf("%t", *s.HomeWorkDateOnly))
	}
	if s.MergeDays != nil {
		mapSetString(root, "merge-same-location-days", fmt.Sprintf("%t", *s.MergeDays))
	}
	if s.Debug != nil {
		mapSetString(root, "debug", fmt.Sprintf("%t", *s.Debug))
	}
	if s.Home != "" || s.Work != "" {
		anchors := mapGetOrCreateMapping(root, "home-work")
		if s.Home != "" {
			mapSetString(anchors, "home", s.Home)
		}
		if s.Work != "" {
			mapSetString(anchors, "work", s.Work)
		}
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write global config: %w", err)
	}
	return nil
}

// mapSetStringSlice sets key to a flow sequence of strings, replacing the value
// in place if key exists or appending it otherwise.
func mapSetStringSlice(m *yaml.Node, key string, values []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = seq
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, seq)
}

// mapGetOrCreateMapping returns the mapping node for key in m, creating an
// empty one if key isn't present yet.
func mapGetOrCreateMapping(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, keyNode, valNode)
	return valNode
}

// mapSetString sets key's scalar value in mapping node m, updating it in
// place if key already exists (preserving any comment on that line) or
// appending a new key: value pair otherwise.
func mapSetString(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].SetString(value)
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.ScalarNode}
	valNode.SetString(value)
	m.Content = append(m.Content, keyNode, valNode)
}
