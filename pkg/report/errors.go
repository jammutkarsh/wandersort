// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package report turns the errors table into what `wandersort admin report` ships: the
// rows as named fields with their paths replaced, and a grouped summary — enough
// to find the bug, and nothing about the person's photos. The database stores
// everything; only this export is scrubbed.
package report

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/db"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// HomePlaceholder stands for the home directory in an export. The username is
// the identifier; the folder structure below it is what makes a report
// debuggable.
const HomePlaceholder = "$HOME"

// Placeholders --redact-paths puts where a path was.
const (
	placeholderSource  = "<source>"
	placeholderName    = "<name>"
	placeholderTarget  = "<target>"
	placeholderLibrary = "<library>"
	placeholderPath    = "<path>"
)

// Options says whose paths to replace and how hard.
type Options struct {
	// Home is the home directory the process knows. Replaced exactly with
	// HomePlaceholder — no guessing, no pattern.
	Home string
	// Library is the output folder, named <library> when Redact is set.
	Library string
	// Redact replaces every path with <source>/<name>/<target>/<library>, for
	// someone whose folder names are personal. Off by default: a report nobody
	// can read helps nobody.
	Redact bool
}

// File is what an error row says about its file, without its path.
type File struct {
	MediaType   string `json:"media_type"`
	Extension   string `json:"extension"`
	Size        int64  `json:"size"`
	VolumeClass string `json:"volume_class"`
}

// Row is one exported error: the columns as named fields, the detail nested
// under its own key, every path replaced.
type Row struct {
	Stage       string         `json:"stage"`
	Op          string         `json:"op"`
	Kind        string         `json:"kind"`
	Attempts    int            `json:"attempts"`
	FirstSeenAt string         `json:"first_seen_at"`
	LastSeenAt  string         `json:"last_seen_at"`
	File        File           `json:"file"`
	Detail      jsontext.Value `json:"detail"`
}

// Querier is the read side of a database handle.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Errors reads every live error row and returns the scrubbed rows plus one
// summary line per group, most frequent first: `31 x READ/open/permission-denied
// at metadata.go:412, .HEIC, removable`.
func Errors(ctx context.Context, q Querier, o Options) ([]Row, []string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT e.stage, e.op, e.kind, e.attempts, e.first_seen_at, e.last_seen_at, e.detail,
		       COALESCE(f.media_type, ''), f.file_extension, f.file_size,
		       COALESCE(f.volume_uuid, ''), f.file_dir, f.file_name, COALESCE(v.target_path, '')
		FROM errors e
		JOIN file_registry f ON f.id = e.file_id
		LEFT JOIN virtual_fs_entries v ON v.file_id = e.file_id
		ORDER BY e.id`)
	if err != nil {
		return nil, nil, fmt.Errorf("read errors: %w", err)
	}
	defer rows.Close()

	classes := map[string]string{} // by volume uuid: one lookup per volume, not per row
	var out []Row
	groups := map[string]int{}
	for rows.Next() {
		var r Row
		var detail, uuid, dir, name, target string
		if err := rows.Scan(&r.Stage, &r.Op, &r.Kind, &r.Attempts, &r.FirstSeenAt, &r.LastSeenAt, &detail,
			&r.File.MediaType, &r.File.Extension, &r.File.Size, &uuid, &dir, &name, &target); err != nil {
			return nil, nil, fmt.Errorf("read errors: %w", err)
		}
		dir = wspath.FromSourcePath(dir)
		r.File.VolumeClass = volumeClass(classes, uuid, dir)

		if r.Detail, err = scrubDetail(detail, knownPaths{o, filepath.Join(dir, name), name, target}); err != nil {
			return nil, nil, err
		}
		out = append(out, r)
		groups[groupKey(r)]++
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read errors: %w", err)
	}
	return out, summarize(groups), nil
}

// volumeClass resolves and caches how the file's volume behaves under
// concurrent reads, keyed by its uuid. Two rows answer Unknown without a
// lookup rather than guessing: a file with no uuid (the same platform
// machinery produces both, so if the uuid failed the class would too), and a
// placed file, whose directory is library-relative from the moment it landed
// and so names nothing on this machine.
func volumeClass(cache map[string]string, uuid, dir string) string {
	if uuid == "" || !filepath.IsAbs(dir) {
		return volume.ClassUnknown.String()
	}
	if class, ok := cache[uuid]; ok {
		return class
	}
	class := volume.ClassForPath(dir).String()
	cache[uuid] = class
	return class
}

// groupKey names what rows have in common: stage/op/kind at the recording
// call site, then the file's extension and volume class.
func groupKey(r Row) string {
	var detail db.ErrorDetail
	site := "unknown"
	if json.Unmarshal(r.Detail, &detail) == nil && len(detail.Frames) > 0 {
		f := detail.Frames[0]
		site = fmt.Sprintf("%s:%d", path.Base(filepath.ToSlash(f.File)), f.Line)
	}
	return fmt.Sprintf("%s/%s/%s at %s, %s, %s", r.Stage, r.Op, r.Kind, site, r.File.Extension, r.File.VolumeClass)
}

func summarize(groups map[string]int) []string {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if groups[keys[i]] != groups[keys[j]] {
			return groups[keys[i]] > groups[keys[j]]
		}
		return keys[i] < keys[j]
	})
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = fmt.Sprintf("%d x %s", groups[k], k)
	}
	return lines
}

// knownPaths is what a row's own strings are matched against.
type knownPaths struct {
	Options
	source string // the file's absolute source path
	name   string
	target string // the planned library-relative path, "" when not planned
}

// scrubDetail replaces the paths in every string of the stored detail object
// and returns it as JSON again.
func scrubDetail(detail string, k knownPaths) (jsontext.Value, error) {
	var v any
	if err := json.Unmarshal([]byte(detail), &v); err != nil {
		return nil, fmt.Errorf("read error detail: %w", err)
	}
	out, err := json.Marshal(walkStrings(v, k.scrub), json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("write error detail: %w", err)
	}
	return out, nil
}

// walkStrings applies fn to every string in a decoded JSON value.
func walkStrings(v any, fn func(string) string) any {
	switch v := v.(type) {
	case string:
		return fn(v)
	case []any:
		for i := range v {
			v[i] = walkStrings(v[i], fn)
		}
	case map[string]any:
		for key := range v {
			v[key] = walkStrings(v[key], fn)
		}
	}
	return v
}

// pathToken is any absolute path left after the exact replacements, for
// --redact-paths only: a directory nobody recorded (a failed mkdir's) has no
// entry to match against. A path runs to the end of the string, a quote, a
// newline or a colon that isn't part of it (Go's "op path: reason"), and
// takes spaces with it: folder names with spaces are the personal ones, and
// stopping at the first space left "Trip 2024/IMG_1.jpg" behind. Where that
// guesses wrong it swallows too much, which is the side to be wrong on.
var pathToken = regexp.MustCompile(`(^|[^A-Za-z0-9_.$>-])((?:[A-Za-z]:)?[\\/](?:[^"\n:]|:[^\s"\n])*)`)

// ScrubHome replaces the home directory with $HOME everywhere in text, as a
// whole path segment, including where JSON escaping doubled its backslashes
// (a Windows home inside a JSON log line).
func ScrubHome(text, home string) string {
	text = replaceHome(text, home)
	if escaped := strings.ReplaceAll(home, `\`, `\\`); escaped != home {
		text = replacePath(text, escaped, HomePlaceholder)
	}
	return text
}

// scrub replaces the paths in s.
func (k knownPaths) scrub(s string) string {
	if !k.Redact {
		return replaceHome(s, k.Home)
	}
	if k.target != "" && k.Library != "" {
		s = replacePath(s, filepath.Join(k.Library, k.target), placeholderTarget)
	}
	s = replacePath(s, k.source, placeholderSource)
	if k.Library != "" {
		s = replacePath(s, k.Library, placeholderLibrary)
	}
	if k.name != "" {
		s = strings.ReplaceAll(s, k.name, placeholderName)
	}
	return pathToken.ReplaceAllString(s, "${1}"+placeholderPath)
}

// replaceHome turns the home directory, in either separator, into $HOME. A
// home of "" or "/" would match everything, so it replaces nothing.
func replaceHome(s, home string) string {
	if home == "" || home == "/" || home == `\` {
		return s
	}
	s = replacePath(s, home, HomePlaceholder)
	if slashed := filepath.ToSlash(home); slashed != home {
		s = replacePath(s, slashed, HomePlaceholder)
	}
	return s
}

// replacePath replaces prefix with with wherever it stands as a whole path
// segment: /Users/jam is replaced in /Users/jam/x, not in /Users/jamie/x.
func replacePath(s, prefix, with string) string {
	if prefix == "" {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, prefix)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(prefix)
		b.WriteString(s[:i])
		if end == len(s) || !isNameByte(s[end]) {
			b.WriteString(with)
		} else {
			b.WriteString(prefix)
		}
		s = s[end:]
	}
}

// isNameByte reports whether c continues a path segment's name.
func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c >= 0x80
}
