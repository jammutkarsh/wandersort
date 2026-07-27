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
	"runtime"
	"strconv"

	wspath "github.com/jammutkarsh/wandersort/pkg/path"
	"go.yaml.in/yaml/v3"
)

const (
	DefaultLibraryDir  = "WanderSortLibrary"
	DefaultLogFileName = ".wandersort.log"
	DefaultDBFileName  = ".wandersort.db"
	locationDBFileName = "location.db"
	defaultLogLevel    = "info"

	// SavedPlaceHome is the user_labels.kind for a confirmed home location.
	SavedPlaceHome = "SAVED_PLACE_HOME"
	// SavedPlaceWork is the user_labels.kind for a confirmed work location.
	SavedPlaceWork = "SAVED_PLACE_WORK"
)

type Configuration struct {
	AppDBPath      string
	LocationDBPath string
	LogLevel       string
	LogConsole     bool
	LogFile        string
	Workers        int
	ExecutablePath string
	// Rules overrides vfs.DefaultConfig's Rules when non-empty; "none"
	// means no levels at all (flat Year/Month). Validated against
	// vfs.Rules* in cli.
	Rules []string
	// CollapseLevels drops a device/orientation/media folder level that has
	// only one value across the whole library — see vfs.Config.CollapseLevels.
	CollapseLevels bool
	// HomeWorkDateOnly groups photos taken at a home/work place by date only,
	// not location — see vfs.Config.HomeWorkDateOnly.
	HomeWorkDateOnly bool
	// MergeSameLocationDays collapses consecutive same-location day folders into
	// a dated range folder — see vfs.Config.MergeSameLocationDays.
	MergeSameLocationDays bool
	// Configured is true once output-path has been set by flag, env, or the
	// config file — the marker `wandersort config` always writes and nothing
	// else does. False means every field above is still a hardcoded default,
	// and the pipeline commands (requireConfigured) refuse to run.
	Configured bool
}

// Defaults returns a Configuration populated with hardcoded defaults only.
// No environment variables are read. Use for CLI where ENV > CLI > defaults predecence applies.
func Defaults() (*Configuration, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}

	appDir := filepath.Join(home, ".wandersort")
	executablesDirectory := filepath.Join(appDir, "bin")
	outputPath := filepath.Join(home, DefaultLibraryDir)

	return &Configuration{
		AppDBPath:             filepath.Join(outputPath, DefaultDBFileName),
		LocationDBPath:        filepath.Join(appDir, locationDBFileName),
		LogLevel:              defaultLogLevel,
		LogConsole:            true,
		LogFile:               filepath.Join(outputPath, DefaultLogFileName),
		Workers:               runtime.NumCPU(),
		ExecutablePath:        executablesDirectory,
		CollapseLevels:        true,
		HomeWorkDateOnly:      true,
		MergeSameLocationDays: true,
	}, nil
}

// FlagOverrides carries user-set CLI flag values into Resolve, layered over
// env vars and the config file. A nil field means the flag was not passed —
// mirrors cobra's Flag.Changed, since Resolve can't tell "false" from
// "unset" any other way.
type FlagOverrides struct {
	OutputPath            *string
	Workers               *int
	CollapseLevels        *bool
	HomeWorkDateOnly      *bool
	MergeSameLocationDays *bool
}

// Resolve builds the runtime Configuration from hardcoded defaults, the
// global config file, environment variables, and CLI flags, in that
// precedence order (flag > env > file > default) — the single place all
// four layers meet. See internal/cli/root.go for how FlagOverrides is built
// from cobra flags.
//
// An unparseable global config file is not fatal: the file is hand-editable
// and every setting in it is optional, so a stray typo must not brick every
// command — including `wandersort config`, the one that would fix it. The
// warning is returned for the caller to log once a logger exists, rather
// than failing outright.
func Resolve(overrides FlagOverrides) (cfg *Configuration, warning string, err error) {
	cfg, err = Defaults()
	if err != nil {
		return nil, "", err
	}

	var outputPath string

	global, loadErr := LoadGlobal()
	if loadErr != nil {
		warning = fmt.Sprintf("Ignoring %s — it isn't valid YAML (%v). Using defaults; fix the file or delete it to get a fresh one.", mustGlobalConfigPath(), loadErr)
	} else {
		if global.OutputPath != "" {
			outputPath = global.OutputPath
		}
		if global.Workers > 0 {
			cfg.Workers = global.Workers
		}
		if len(global.Rules) > 0 {
			cfg.Rules = global.Rules
		}
		// The three bools below are the only fields where an absent YAML key
		// and an explicit "false" are the same Go zero value — output-path is
		// the marker that this file has been through the wizard (SaveGlobal
		// always writes every key together), so gating on it avoids reading
		// an empty/hand-edited-partial file's zero-valued bools as "false".
		if global.OutputPath != "" {
			cfg.CollapseLevels = global.CollapseLevels
			cfg.HomeWorkDateOnly = global.HomeWorkDateOnly
			cfg.MergeSameLocationDays = global.MergeSameLocationDays
		}
	}

	if v := os.Getenv("OUTPUT_PATH"); v != "" {
		outputPath = v
	}
	if v := os.Getenv("WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Workers = n
		}
	}
	if v := os.Getenv("COLLAPSE_LEVELS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.CollapseLevels = b
		}
	}
	if v := os.Getenv("HOME_WORK_DATE_ONLY"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.HomeWorkDateOnly = b
		}
	}
	if v := os.Getenv("MERGE_SAME_LOCATION_DAYS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.MergeSameLocationDays = b
		}
	}

	if overrides.OutputPath != nil && *overrides.OutputPath != "" {
		outputPath = *overrides.OutputPath
	}
	if overrides.Workers != nil && *overrides.Workers > 0 {
		cfg.Workers = *overrides.Workers
	}
	if overrides.CollapseLevels != nil {
		cfg.CollapseLevels = *overrides.CollapseLevels
	}
	if overrides.HomeWorkDateOnly != nil {
		cfg.HomeWorkDateOnly = *overrides.HomeWorkDateOnly
	}
	if overrides.MergeSameLocationDays != nil {
		cfg.MergeSameLocationDays = *overrides.MergeSameLocationDays
	}

	if outputPath != "" {
		cfg.Configured = true
		outputPath = wspath.New().ExpandPath(outputPath)
		cfg.AppDBPath = filepath.Join(outputPath, DefaultDBFileName)
		cfg.LogFile = filepath.Join(outputPath, DefaultLogFileName)
	}

	return cfg, warning, nil
}

// mustGlobalConfigPath is GlobalConfigPath without the error return, for the
// warning message above — os.UserHomeDir() already succeeded once in
// Defaults() by the time Resolve reaches this point, so a second failure
// here would be a hardware-fault-tier surprise, not something to plumb an
// error return through a warning string for.
func mustGlobalConfigPath() string {
	p, err := GlobalConfigPath()
	if err != nil {
		return GlobalConfigFileName
	}
	return p
}

/* ---------- ~/.wandersort/config.yaml ---------- */

// GlobalConfigFileName lives under ~/.wandersort — settings that make sense
// across every library (not tied to one --output-path), like the home/work
// places and a default output path so it doesn't have to be typed every run.
const GlobalConfigFileName = "config.yaml"

// HomeWork are the confirmed home/work town names, set via `wandersort config`
// and reused by every scan (see vfs.SyncAnchors) instead of being
// tied to one library's database. Photos taken at these everyday places are
// grouped by date only, not location (there's no point sorting your home
// photos into a "home town" folder).
type HomeWork struct {
	Home string `yaml:"home,omitempty"`
	Work string `yaml:"work,omitempty"`
}

// Global is the on-disk shape of ~/.wandersort/config.yaml. `wandersort
// config` is the only thing that writes it (whole-file marshal — no comments
// to preserve), and Resolve reads the same keys so flags and env vars still
// layer over them.
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
// invocation (see Resolve) so there's always a file for LoadGlobal to read.
// It starts empty on purpose: every setting has a built-in default, and
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
