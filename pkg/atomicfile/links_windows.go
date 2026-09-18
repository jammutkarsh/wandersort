// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package atomicfile

import "io/fs"

// sharedInode has no link count to read on Windows (fs.FileInfo.Sys is a
// Win32FileAttributeData there), so it trusts sameEntry alone and says yes.
func sharedInode(fs.FileInfo) bool { return true }
