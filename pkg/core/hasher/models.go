// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package hasher

import "github.com/jammutkarsh/wandersort/pkg/classifier"

// fileRecord is the minimal info the pipeline passes from the scan phase
// to drive the hash phase
type fileRecord struct {
	id      int64
	absPath string
}

type hashedRecord struct {
	id   int64
	hash string
	exif classifier.CommonMetadata
}
