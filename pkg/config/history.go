package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	historyFileName = "libraries"
	// maxHistory caps the remembered output folders
	maxHistory = 100
)

// History is the output folders this machine has opened, newest first; ones
// that no longer exist are dropped as it is read.
func (cfg *Configuration) History() []string {
	data, err := os.ReadFile(filepath.Join(cfg.appDir, historyFileName))
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
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
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

// Remember puts dir at the front of the history (capped at maxHistory). Called
// once a library is really opened.
func (cfg *Configuration) Remember(dir string) error {
	list := append([]string{dir}, cfg.History()...)
	for i := 1; i < len(list); i++ {
		if list[i] == dir {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) > maxHistory {
		list = list[:maxHistory]
	}
	if err := os.MkdirAll(cfg.appDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", cfg.appDir, err)
	}
	path := filepath.Join(cfg.appDir, historyFileName)
	if err := os.WriteFile(path, []byte(strings.Join(list, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
