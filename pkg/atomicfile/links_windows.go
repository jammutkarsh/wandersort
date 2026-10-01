package atomicfile

import "io/fs"

// sharedInode has no link count to read on Windows (fs.FileInfo.Sys is a
// Win32FileAttributeData there), so it trusts sameEntry alone and says yes.
func sharedInode(fs.FileInfo) bool { return true }
