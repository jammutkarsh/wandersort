// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package path

import (
	"path/filepath"

	"golang.org/x/text/unicode/norm"
)

// ToLibrary normalizes p for storage in an in-library path column
// (virtual_fs_entries.target_path, folder_nodes.name) — a path this app created
// itself (spec D9). The database travels with the library, so it must not
// depend on the OS that wrote it (/ separators) or how that OS spelled
// composed characters (NFC — macOS gives NFD from the filesystem). Safe here
// specifically because the app chose every segment's spelling, so folding it
// to NFC changes nothing about whether the file can be found again.
func ToLibrary(p string) string {
	return norm.NFC.String(filepath.ToSlash(p))
}

// FromLibrary converts a stored in-library path back to the OS's native
// separator for use with os/filepath calls.
func FromLibrary(p string) string {
	return filepath.FromSlash(p)
}

// ToSourcePath normalizes an OS-given path for storage in a source-path
// column (file_registry.file_dir/file_name, virtual_fs_entries.source_path):
// only the separator changes, never a name's bytes. The filesystem, not this
// app, chose that spelling — NFC-folding it breaks byte-exact lookup on Linux
// (and Windows) for a name a Mac wrote as NFD, and can collide two genuinely
// different Linux files whose names differ only in normalization form.
// Exactly filepath.ToSlash: a no-op except when compiled for windows, where
// the OS really did use backslash — never an unconditional replace, since a
// literal backslash is a legal byte in a Linux filename.
func ToSourcePath(p string) string {
	return filepath.ToSlash(p)
}

// FromSourcePath reverses ToSourcePath for use with os/filepath calls.
func FromSourcePath(p string) string {
	return filepath.FromSlash(p)
}
