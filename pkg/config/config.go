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
	defaultLibrary     = "WandersortLibrary"
	defaultLogFileName = ".wandersort.log"
	defaultDBFileName  = ".wandersort.db"
	locationDBFileName = "location.db"
	defaultLogLevel    = "info"
	configFileName     = "config.yaml"
)

type TriBool int

const (
	Unset TriBool = iota
	False
	True
)

// BoolToTri converts a Go bool to TriBool (True or False).
func BoolToTri(b bool) TriBool {
	if b {
		return True
	}
	return False
}

// UnmarshalYAML lets a plain true/false in config.yaml populate a
// TriBool; a field left out of the file never calls this, so it stays Unset.
func (t *TriBool) UnmarshalYAML(value *yaml.Node) error {
	var b bool
	if err := value.Decode(&b); err != nil {
		return err
	}
	*t = BoolToTri(b)
	return nil
}

type Configuration struct {
	appConfig string `yaml:"-"`
	// The following fields are persisted in ~/.wandersort/config.yaml, and
	OutputPath            string   `yaml:"output-path,omitempty"`
	Rules                 []string `yaml:"rules,omitempty"`
	CollapseLevels        bool     `yaml:"collapse-levels"`
	SavedPlacesDateOnly   bool     `yaml:"saved-places-date-only"`
	MergeSameLocationDays bool     `yaml:"merge-same-location-days"`
	// SavedPlaces is positional: index 0 is home, 1 is work, everything else
	// is another frequently-stayed-at place — all anchored the same way.
	SavedPlaces []string `yaml:"saved-places,omitempty"`
	// SegmentMonths is how big a time slice the review works through at once.
	// 0 = pick from the span the library covers. See vfs.Segments.
	SegmentMonths int `yaml:"segment-months,omitempty"`

	// Computed at runtime, never persisted.
	//
	// Workers is not a setting. It sizes the goroutine and exiftool pools,
	// both CPU-bound, so the CPU's own number is the right one — while the
	// only disk-bound thing in the pipeline (the metadata phase's byte reads)
	// is throttled by the storage class instead, in pkg/core/metadata. A
	// hand-set count could only make one of those two worse.
	Workers        int    `yaml:"-"`
	AppDBPath      string `yaml:"-"`
	LocationDBPath string `yaml:"-"`
	LogLevel       string `yaml:"-"`
	LogConsole     bool   `yaml:"-"`
	LogFile        string `yaml:"-"`
	ExecutablePath string `yaml:"-"`
	Configured     bool   `yaml:"-"`
}

type Overrides struct {
	OutputPath            string   `yaml:"output-path,omitempty"`
	Rules                 []string `yaml:"rules,omitempty"`
	CollapseLevels        TriBool  `yaml:"collapse-levels,omitempty"`
	SavedPlacesDateOnly   TriBool  `yaml:"saved-places-date-only,omitempty"`
	MergeSameLocationDays TriBool  `yaml:"merge-same-location-days,omitempty"`
	SavedPlaces           []string `yaml:"saved-places,omitempty"`
	SegmentMonths         int      `yaml:"segment-months,omitempty"`
}

// pick returns the first non-zero-value layer, in priority order
// (typically flag, env, global-config), or def if all are unset.
func pick[T comparable](def T, layers ...T) T {
	var zero T
	for _, v := range layers {
		if v != zero {
			return v
		}
	}
	return def
}

// resolveBool is pick specialized for TriBool -> bool, since Configuration
// stores CollapseLevels etc. as plain bool, not TriBool.
func resolveBool(def bool, layers ...TriBool) bool {
	return pick(BoolToTri(def), layers...) == True
}

func envStr(name string) string { return os.Getenv(name) }

func envInt(name string) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return 0
}

func envBool(name string) TriBool {
	v, ok := os.LookupEnv(name)
	if !ok {
		return Unset
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return BoolToTri(b)
	}
	return Unset
}

// defaults returns a Configuration populated with hardcoded defaults only.
// No environment variables are read.
func defaults() (*Configuration, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}

	appDir := filepath.Join(home, ".wandersort")
	executablesDirectory := filepath.Join(appDir, "bin")
	configPath := filepath.Join(appDir, configFileName)
	outputPath := filepath.Join(home, defaultLibrary)

	return &Configuration{
		appConfig:             configPath,
		AppDBPath:             filepath.Join(outputPath, defaultDBFileName),
		LocationDBPath:        filepath.Join(appDir, locationDBFileName),
		LogLevel:              defaultLogLevel,
		LogConsole:            true,
		LogFile:               filepath.Join(outputPath, defaultLogFileName),
		Workers:               runtime.NumCPU(),
		ExecutablePath:        executablesDirectory,
		CollapseLevels:        true,
		SavedPlacesDateOnly:   true,
		MergeSameLocationDays: true,
	}, nil
}

// Resolve builds the runtime Configuration from hardcoded defaults, the
// global config file, environment variables, and CLI flags, in that
// precedence order (flag > env > file > default)
func Resolve(o Overrides) (cfg *Configuration, warning string, err error) {
	cfg, err = defaults()
	if err != nil {
		return nil, "", err
	}

	global, loadErr := cfg.Load()
	if loadErr != nil {
		warning = fmt.Sprintf("Ignoring %s — it isn't valid YAML (%v). Using defaults; fix the file or delete it to get a fresh one.", cfg.appConfig, loadErr)
		global = Overrides{}
	}

	if len(global.Rules) > 0 {
		cfg.Rules = global.Rules
	}
	if len(global.SavedPlaces) > 0 {
		cfg.SavedPlaces = global.SavedPlaces
	}

	outputPath := pick("", o.OutputPath, envStr("OUTPUT_PATH"), global.OutputPath)
	// 0 is the real default here ("auto"), not a missing value.
	cfg.SegmentMonths = pick(0, o.SegmentMonths, envInt("SEGMENT_MONTHS"), global.SegmentMonths)

	// Bools from file only apply when the file went through the wizard
	// (carries an output-path). An unconfigured file's bools are the Go
	// zero value (False), which would stomp defaults to false.
	if global.OutputPath == "" {
		global.CollapseLevels = Unset
		global.SavedPlacesDateOnly = Unset
		global.MergeSameLocationDays = Unset
	}
	cfg.CollapseLevels = resolveBool(cfg.CollapseLevels, o.CollapseLevels, envBool("COLLAPSE_LEVELS"), global.CollapseLevels)
	cfg.SavedPlacesDateOnly = resolveBool(cfg.SavedPlacesDateOnly, o.SavedPlacesDateOnly, envBool("SAVED_PLACES_DATE_ONLY"), global.SavedPlacesDateOnly)
	cfg.MergeSameLocationDays = resolveBool(cfg.MergeSameLocationDays, o.MergeSameLocationDays, envBool("MERGE_SAME_LOCATION_DAYS"), global.MergeSameLocationDays)

	if outputPath != "" {
		cfg.Configured = true
		outputPath = wspath.New().ExpandPath(outputPath)
		cfg.AppDBPath = filepath.Join(outputPath, defaultDBFileName)
		cfg.LogFile = filepath.Join(outputPath, defaultLogFileName)
	}

	return cfg, warning, nil
}

// Exists creates an empty ~/.wandersort/config.yaml if it
// doesn't exist yet, and always returns its path
func (cfg *Configuration) Exists() (string, error) {
	path := cfg.appConfig
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

// Load reads the global config file. A missing file is not an error —
// it just means nothing has been configured globally yet.
func (cfg *Configuration) Load() (Overrides, error) {
	path := cfg.appConfig
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Overrides{}, nil
	}
	if err != nil {
		return Overrides{}, fmt.Errorf("read global config: %w", err)
	}
	var o Overrides
	if err := yaml.Unmarshal(data, &o); err != nil {
		return Overrides{}, fmt.Errorf("parse global config: %w", err)
	}
	return o, nil
}

// Save writes the whole config file from cfg, replacing whatever was there.
// The config TUI always submits every setting, so there is nothing to merge.
func (cfg *Configuration) Save(saveCfg *Configuration) error {
	path := cfg.appConfig
	out, err := yaml.Marshal(saveCfg)
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
