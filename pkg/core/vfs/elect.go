package vfs

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

// Master election is computed every run, never stored, so it can't go stale
// and re-propose an already-placed file.
var (
	// Matches date-prefixed filenames: YYYYMMDD_ or YYYY-MM-DD_ or YYYY_MM_DD_
	// Ref: https://en.wikipedia.org/wiki/ISO_8601#Calendar_dates
	datePattern = regexp.MustCompile(`^(\d{8}_|\d{4}-\d{2}-\d{2}_|\d{4}_\d{2}_\d{2}_)`)
	// Matches camera-generated filenames per DCF spec:
	// IMG_3162, _MG_1721 (Adobe RGB), DSC01234, WP_0001, GOPR1234, ABCD0001, etc.
	// Structure: optional underscore + 1-5 letters + optional underscore + digits.
	// Ref: https://en.wikipedia.org/wiki/Design_rule_for_Camera_File_system
	cameraPattern = regexp.MustCompile(`^(_?[A-Z]{1,5}_?)\d+(_\d+)*$`)

	// Catches copy-artifact suffixes: "IMG_1234 (1)", "Photo - Copy", "Photo copy 2".
	duplicateSuffixPattern = regexp.MustCompile(`(?i)[ _-]*\(\d+\)$|[ _-]copy(\s*\(?\d*\)?)?$`)

	// Possibly human-readable names
	hasLetterPattern = regexp.MustCompile(`[a-zA-Z]`)
)

const (
	scoreMeaningful     = 4
	scoreDatePattern    = 3
	scoreDirBonus       = 2
	penaltyDuplicateTag = -3
)

// electMasters keeps one file per content hash and reports how many hashes
// had duplicates. rows must be in (file_dir, file_name) order, which makes ties
// deterministic. Groups holding a placed file are dropped earlier, in SQL.
func electMasters(rows []masterFile) (masters []masterFile, duplicateGroups int) {
	type candidate struct {
		idx     int
		score   int
		pathLen int
		copies  int
	}
	best := make(map[string]candidate, len(rows))
	for i := range rows {
		score := perFileScore(rows[i].absPath)
		pathLen := len(rows[i].FileDir) + len(rows[i].FileName)
		cur, seen := best[rows[i].FileHash]
		switch {
		case !seen:
			best[rows[i].FileHash] = candidate{idx: i, score: score, pathLen: pathLen, copies: 1}
		case score > cur.score || (score == cur.score && pathLen < cur.pathLen):
			best[rows[i].FileHash] = candidate{idx: i, score: score, pathLen: pathLen, copies: cur.copies + 1}
		default:
			cur.copies++
			best[rows[i].FileHash] = cur
		}
	}

	keep := make([]bool, len(rows))
	for _, c := range best {
		keep[c.idx] = true
		if c.copies > 1 {
			duplicateGroups++
		}
	}
	masters = make([]masterFile, 0, len(best))
	for i := range rows {
		if keep[i] {
			masters = append(masters, rows[i])
		}
	}
	return masters, duplicateGroups
}

// perFileScore computes a metadata quality score for a single file path.
// Signals: human-readable name (+4), date-prefixed name (+3), non-generic
// directory (+2), duplicate-copy suffix like "(1)" or "copy" (-3).
func perFileScore(filePath string) int {
	dir, name := filepath.Split(filePath)
	stem := strings.TrimSuffix(name, filepath.Ext(name))

	score := 0
	if !cameraPattern.MatchString(stem) && hasLetterPattern.MatchString(stem) {
		score += scoreMeaningful
	}
	if datePattern.MatchString(name) {
		score += scoreDatePattern
	}
	// Judge only the immediate parent folder — file_dir is absolute, and
	// generic segments higher up (Users, Photos, Downloads) must not
	// disqualify a meaningful leaf folder
	if !classifier.IsGenericDirName(filepath.Base(dir)) {
		score += scoreDirBonus
	}
	// Penalize duplicate-copy suffixes, which are common in camera roll imports and cloud syncs.
	if duplicateSuffixPattern.MatchString(stem) {
		score += penaltyDuplicateTag
	}
	return score
}
