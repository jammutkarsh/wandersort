package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/path"
)

const (
	historyFileName       = "libraries"
	sourceHistoryFileName = "sources"
	// maxHistory caps each remembered folder list
	maxHistory = 100
)

// History is the output folders this machine has opened, newest first.
func (cfg *Configuration) History() []string { return cfg.present(historyFileName) }

// ErrLibraryMissing refuses a library this machine opened before whose folder
// is gone: its drive is likely unplugged, and starting a new library there
// would scatter one collection over two.
var ErrLibraryMissing = errors.New("this library's folder is missing; plug in its drive, or pick another library")

// CheckRemembered refuses dir if it is a library this machine opened before
// and its folder is gone. A folder never opened is free to become one.
func (cfg *Configuration) CheckRemembered(dir string) error {
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		return nil
	}
	for _, d := range cfg.readHistory(historyFileName) {
		if d == dir {
			return fmt.Errorf("%s: %w", path.New().Friendly(dir), ErrLibraryMissing)
		}
	}
	return nil
}

// Remember puts dir at the front of the library history. Called once a library
// is really opened.
func (cfg *Configuration) Remember(dir string) error { return cfg.remember(historyFileName, dir) }

// SourceHistory is the folders this machine has scanned, newest first.
func (cfg *Configuration) SourceHistory() []string { return cfg.present(sourceHistoryFileName) }

// RememberSource puts dir at the front of the scanned-folder history.
func (cfg *Configuration) RememberSource(dir string) error {
	return cfg.remember(sourceHistoryFileName, dir)
}

// present is a history's folders that exist now. Missing ones (an unplugged
// drive) stay in the file and come back when the folder does.
func (cfg *Configuration) present(name string) []string {
	var out []string
	for _, dir := range cfg.readHistory(name) {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			out = append(out, dir)
		}
	}
	return out
}

// readHistory is a history file's folders as stored, newest first, once each.
func (cfg *Configuration) readHistory(name string) []string {
	data, err := os.ReadFile(filepath.Join(cfg.appDir, name))
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		dir := strings.TrimSpace(line)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
		if len(out) == maxHistory {
			break
		}
	}
	return out
}

// remember moves dir to the front of a history file, capped at maxHistory.
func (cfg *Configuration) remember(name, dir string) error {
	list := []string{dir}
	for _, d := range cfg.readHistory(name) {
		if d != dir {
			list = append(list, d)
		}
	}
	if len(list) > maxHistory {
		list = list[:maxHistory]
	}
	if err := os.MkdirAll(cfg.appDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", cfg.appDir, err)
	}
	path := filepath.Join(cfg.appDir, name)
	if err := os.WriteFile(path, []byte(strings.Join(list, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
