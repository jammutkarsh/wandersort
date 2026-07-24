// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

var schema004 = Migration{
	Version:     4,
	Description: "vfs_suggestion_dir",
	SQL:         []string{suggestionDir},
}

// suggestion_dir is the directory a proposal's suggestion belongs to — the
// folder a reviewer renames. The VFS build records it so the review tree can
// attach the suggestion by path instead of guessing a depth (which broke as
// soon as location wasn't the first --rules level).
//
// Its own migration rather than an edit to 003, unlike the usual pre-tag rule:
// 003 has already run on real libraries, and re-creating the schema from
// scratch would mean re-hashing every file. An ALTER costs nothing.
const suggestionDir = `
ALTER TABLE virtual_fs_entries ADD COLUMN suggestion_dir TEXT;
`
