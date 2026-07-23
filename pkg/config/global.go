package config

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// GlobalConfigFileName lives under ~/.wandersort — settings that make sense
// across every library (not tied to one --output-path), like home/work
// anchors and a default output path so it doesn't have to be typed every run.
const GlobalConfigFileName = "config.yaml"

// Anchors are the confirmed home/work town names, set once via `wandersort
// setup` and reused by every scan (see cli.syncAnchorsFromConfig) instead of
// being tied to one library's database.
type Anchors struct {
	Home string `yaml:"home,omitempty"`
	Work string `yaml:"work,omitempty"`
}

// Global is the on-disk shape of ~/.wandersort/config.yaml, for reading.
// output-path/workers/debug/group-by aren't modeled here — they're plain
// viper keys the CLI layer reads directly (see internal/cli/root.go's
// applyOverrides), never written back by this package. Only Anchors is ever
// written by our own code (SaveAnchors), which is why it's the only field
// with a matching write path below.
type Global struct {
	OutputPath string  `yaml:"output-path,omitempty"`
	Anchors    Anchors `yaml:"anchors,omitempty"`
}

// configTemplate seeds a fresh config.yaml with every supported key
// documented, most of them commented out (they already have a sensible
// built-in default — see config.Defaults). anchors.home/work are the
// exception: left as real, empty, top-level keys because SaveAnchors edits
// them in place, so the on-disk comments explaining them survive that edit
// (only the two scalar values under anchors: are ever touched).
const configTemplate = `# WanderSort global config.
# Applies to every scan/serve unless overridden by a command-line flag or an
# environment variable. Precedence: flag > env > this file > built-in default.
# Run 'wandersort config' any time to reopen this file (in $EDITOR, or prints
# it if $EDITOR isn't set).

# Default output directory (DB + logs). Same as --output-path / -o.
# output-path: ~/WanderSortLibrary

# Concurrent worker count. Same as --workers / -w.
# workers: 4

# Verbose logging. Same as --debug.
# debug: false

# Folder levels below Year/Month for new proposals, i.e. group by: location,
# orientation, device, media, or "none" for flat Year/Month. Same as
# scan/serve's --group-by, or change it per-session from the review TUI's
# [L] key.
# group-by: [location, orientation, media]

# Confirmed home/work towns (set interactively via 'wandersort setup').
# GPS within ~50km of either folds into that town's folder instead of
# splitting into separate per-neighbourhood folders.
anchors:
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

// SaveAnchors sets anchors.home/anchors.work in the global config file,
// creating it from configTemplate first if it doesn't exist. It edits the
// YAML node tree in place rather than re-marshaling the whole struct, so
// every other key — output-path, workers, comments, anything the user typed
// by hand — survives untouched. An empty name leaves that key alone (use
// LoadGlobal first to know what's already set).
func SaveAnchors(home, work string) error {
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

	anchors := mapGetOrCreateMapping(root, "anchors")
	if home != "" {
		mapSetString(anchors, "home", home)
	}
	if work != "" {
		mapSetString(anchors, "work", work)
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
