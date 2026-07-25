// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scanner

import "time"

// FileOriginSource marks a file found under a user-supplied scan root, as
// opposed to one WanderSort itself wrote into the organized library.
const FileOriginSource = "SOURCE"

// FileDiscovery is the lightweight struct used during directory walking.
// Dir is the file's absolute parent directory
type FileDiscovery struct {
	ID         int64
	Dir        string
	Name       string
	Size       int64
	ModTime    time.Time
	Extension  string
	VolumeUUID string
	MediaType  string
}
