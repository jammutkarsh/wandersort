package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/lock"
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

// ErrLibraryMissing refuses a library this machine opened before that is no
// longer in its folder: its drive is likely unplugged, and starting a new
// library there would scatter one collection over two.
var ErrLibraryMissing = errors.New("this library is missing; plug in its drive, or pick another library")

// CheckRemembered refuses dir if it is a library this machine opened before
// and neither its database nor its backup is there. The folder alone proves
// nothing: an unplugged drive's mount point can stay behind, empty. A folder
// never opened is free to become one.
func (cfg *Configuration) CheckRemembered(dir string) error {
	for _, name := range []string{defaultDBFileName, db.BackupFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			return nil
		}
	}
	for _, d := range cfg.readHistory(historyFileName) {
		if sameSpelling(d, dir) {
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

// sameSpelling reports whether a and b name one folder on a system whose
// disks usually ignore letter case and Unicode form (macOS, Windows). It
// errs toward a match: a missing library typed another way is still refused.
func sameSpelling(a, b string) bool {
	if a == b {
		return true
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return false
	}
	return strings.EqualFold(norm.NFC.String(a), norm.NFC.String(b))
}

// readHistory is a history file's folders as stored, newest first, once each.
// It waits for a history being replaced; with no lock to be had (an app
// folder it can't write), it reads anyway: a history unread would let a
// missing library start afresh.
func (cfg *Configuration) readHistory(name string) []string {
	if l, err := lock.AcquireHistory(cfg.appDir); err == nil {
		defer l.Unlock()
	}
	return cfg.readHistoryHeld(name)
}

// readHistoryHeld is readHistory for a caller holding the history lock.
func (cfg *Configuration) readHistoryHeld(name string) []string {
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

// remember moves dir to the front of a history file, capped at maxHistory. It
// holds the history lock from read to replace, so two processes remembering
// at once never lose an entry, and on Windows no reader holds the file open
// while it is renamed over.
func (cfg *Configuration) remember(name, dir string) error {
	l, err := lock.AcquireHistory(cfg.appDir) // creates the app folder
	if err != nil {
		return fmt.Errorf("lock the history: %w", err)
	}
	defer l.Unlock()
	list := []string{dir}
	for _, d := range cfg.readHistoryHeld(name) {
		if d != dir {
			list = append(list, d)
		}
	}
	if len(list) > maxHistory {
		list = list[:maxHistory]
	}
	path := filepath.Join(cfg.appDir, name)
	// replaced whole: an emptied history would let a missing library start afresh
	if err := atomicfile.WriteFile(path, []byte(strings.Join(list, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
