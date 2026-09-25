// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

var schema001 = Migration{
	Version:     0o01,
	Description: "scanner_schema",
	SQL: []string{
		fileRegistry,
		errorsTable,
	},
}

// file_registry table with indexes
const fileRegistry = `
CREATE TABLE IF NOT EXISTS file_registry (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Physical identity: absolute directory + name. One row per file on disk,
    -- no matter which scan root the file was discovered through
    file_dir         TEXT    NOT NULL,
    file_name        TEXT    NOT NULL,
    file_size        INTEGER NOT NULL,
    file_modified_at TEXT    NOT NULL,

    -- Volume the file lives on; lets a future re-anchor pass rewrite paths
    -- when an external drive remounts elsewhere. NULL when unresolvable
    volume_uuid TEXT,

    -- Discovery metadata
    discovered_at TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL,
    -- The scan that last saw this file: a counter, one past the highest
    -- stored, taken when a scan starts. The sweep deletes rows under a root
    -- whose number is older than the running scan's. Not a timestamp: a
    -- wall clock can step backwards mid-scan (NTP, waking from sleep), and a
    -- file seen in that window would compare as unseen and lose its row
    last_seen_scan INTEGER NOT NULL DEFAULT 0,

    -- File classification
    media_type     TEXT,
    file_extension TEXT NOT NULL,

    file_origin TEXT NOT NULL DEFAULT 'SOURCE',

    -- Set once execute lands this file at its target, copy or move alike.
    -- A fact about the file, not the plan: virtual_fs_entries rows are
    -- replaced on every scan and settings change, this flag never is. The
    -- vfs phase treats a placed file as the permanent master of its hash:
    -- its whole hash group is dropped from the proposal, and its own plan
    -- row is never re-proposed or deleted (spec D10/D11)
    placed INTEGER NOT NULL DEFAULT 0,

    CHECK (media_type IN ('IMAGE', 'VIDEO', 'SIDECAR', 'RAW', 'UNKNOWN'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_registry_dir_name ON file_registry(file_dir, file_name);
`

// errors holds the one failure a stage can have for a file, and only while it
// is true: the row goes the moment the read or transfer works, or the file's
// own row does (a changed file, --force, a sweep — all cascade). One row per
// (file, stage), replaced when it fails again. The file's path, size, type and
// volume are one join away, so nothing about the file is copied here.
const errorsTable = `
CREATE TABLE IF NOT EXISTS errors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id INTEGER NOT NULL REFERENCES file_registry(id) ON DELETE CASCADE,
    stage TEXT NOT NULL CHECK (stage IN ('READ', 'TRANSFER', 'VERIFY')),
    -- the step inside the stage: open, hash, exiftool, stat, mkdir, copy,
    -- rename, remove-source, commit
    op TEXT NOT NULL,
    -- the bucket, derived from the error (errors.Is against fs.ErrPermission,
    -- fs.ErrNotExist, ENOSPC, ...): permission-denied, not-found, io-error,
    -- no-space, checksum-mismatch, panic, other
    kind TEXT NOT NULL,
    -- everything that varies, as one JSON object of named fields: message,
    -- chain (the unwrapped errors), frames, syscall. Stored whole; only the
    -- export (wandersort admin report) has its paths replaced. Go errors carry no
    -- stack, so "frames" is where the pipeline recorded the failure, not where
    -- the OS call returned: it names the code path, it is no post-mortem
    -- trace. A caught panic puts its real stack there. Read in Go only.
    detail TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 1,
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    UNIQUE (file_id, stage)
);
`
