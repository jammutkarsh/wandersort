// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package exif

// fileRecord is the minimal info the exif phase needs about a file the hash
// phase already registered
type fileRecord struct {
	id      int64
	absPath string
}
