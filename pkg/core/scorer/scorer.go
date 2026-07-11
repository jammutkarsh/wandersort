// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

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

	// Known exact low-signal directory names (case-insensitive).
	genericDirs = map[string]bool{
		"dcim": true, "camera": true, "photos": true, "temp": true,
		"downloads": true, "desktop": true, "backup": true, "misc": true,
	}

	// Catches variants a fixed set can't enumerate: "DCIM 1", "Backup_2023",
	// "New folder (2)", "old backup", ".thumbnails", "WhatsApp Images", etc.
	genericDirPattern = regexp.MustCompile(`(?i)^(\.?(thumbnails|trashed.*|sync|cache))$` +
		`|^new folder(\s*\(\d+\))?$` +
		`|^(old\s+)?backup[\s_-]*\d*$` +
		`|^(dcim|temp|tmp|misc|downloads?)[\s_-]*\d*$` +
		`|^(whatsapp|telegram|signal)[\s_-]*(images?|videos?|media)$`)
)

const (
	scoreMeaningful     = 4
	scoreDatePattern    = 3
	scoreDirBonus       = 2
	penaltyDuplicateTag = -3
)

type Scorer struct {
	db  *db.DB
	log logger.Logger
}

func New(db *db.DB, log logger.Logger) *Scorer {
	return &Scorer{db: db, log: log}
}

func (s *Scorer) Run(ctx context.Context, sessionID uuid.UUID) (int, error) {
	s.log.Info("Scoring session", "sessionId", sessionID)

	type member struct {
		FileHash   string `db:"file_hash"`
		FileID     int64  `db:"file_id"`
		FilePath   string `db:"file_path"`
		SourceRoot string `db:"source_root"`
	}

	var rows []member
	if err := s.db.SQL.SelectContext(ctx, &rows, `
		SELECT fm.file_hash, fm.file_id,
			fr.file_path, fr.source_root
		FROM file_metadata fm
		JOIN file_registry fr ON fr.id = fm.file_id
		WHERE fm.file_hash IN (
			SELECT file_hash FROM file_metadata
			GROUP BY file_hash HAVING COUNT(*) > 1 )
		ORDER BY fm.file_hash`); err != nil {
		return 0, fmt.Errorf("query members: %w", err)
	}

	count, start := 0, 0
	for start < len(rows) {
		// Sliding window to find the next group of duplicates with the same hash
		end := start
		for end < len(rows) && rows[end].FileHash == rows[start].FileHash {
			end++
		}
		duplicates := rows[start:end]
		start = end
		count++

		bestScore, bestPathLen, master := math.MinInt, math.MaxInt, member{}
		for _, dupe := range duplicates {
			score := perFileScore(dupe.FilePath)
			pathLen := len(dupe.SourceRoot) + len(dupe.FilePath)
			if score > bestScore || (score == bestScore && pathLen < bestPathLen) {
				master = dupe
				bestScore = score
				bestPathLen = pathLen
			}
		}

		s.log.Debug("Persisted group master", "sessionId", sessionID, "hash", master.FileHash,
			"masterFileId", master.FileID, "memberCount", len(duplicates))
	}

	s.log.Info("Scored duplicate groups", "sessionId", sessionID, "count", count)
	return count, nil
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
	if !isInGenericDir(dir) {
		score += scoreDirBonus
	}
	// Penalize duplicate-copy suffixes, which are common in camera roll imports and cloud syncs.
	if duplicateSuffixPattern.MatchString(stem) {
		score += penaltyDuplicateTag
	}
	return score
}

// isInGenericDir reports whether any segment of dir is a known or
// pattern-matched low-signal folder name (DCIM, Backup, temp, etc).
func isInGenericDir(dir string) bool {
	dir = filepath.Clean(dir)
	for dir != "." && dir != "/" && dir != "" {
		seg := strings.ToLower(filepath.Base(dir))
		if genericDirs[seg] || genericDirPattern.MatchString(seg) {
			return true
		}
		dir = filepath.Dir(dir)
	}
	return false
}
