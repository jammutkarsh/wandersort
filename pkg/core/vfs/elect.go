// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

// Electing one copy of each duplicate used to be its own phase writing its
// answer to file_metadata.is_master, which loadMasters and persist then read
// back. It is computed here instead, from the rows loadMasters already has.
//
// The column bought nothing the query could not: the election is a pure
// function of the paths in a hash group, and every reader of it lived in this
// package. It cost something, though — a *persisted* answer can be wrong
// between runs, which is exactly the shape of a reported bug (issue 06/07):
// a re-scanned card photo in `Goa Trip` out-scored the same file already
// placed under a generic `…/Photos`, flipping the placed file's is_master to
// 0, at which point persist deleted the placed file's plan row for "no longer
// being a live master" and proposed the duplicate to be copied again. There is
// no flag to flip now, so there is nothing for persist to misread. It also
// retires the re-promote UPDATE that re-elected the lone survivor of a
// shrunken group: recomputing from the live rows does that for free.
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
// had more than one copy. rows must already be in (file_dir, file_name)
// order: that is what makes a tie deterministic across re-scans, since the
// first member seen keeps the election, and the returned slice is in the same
// order so clustering and collision suffixes don't vary either.
//
// Groups holding an already-placed file never reach here — loadMasters drops
// them in SQL, because a placed file is the master of its hash by definition
// and nothing in the group has anything left to win.
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
