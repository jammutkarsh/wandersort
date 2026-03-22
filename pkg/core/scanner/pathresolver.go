package scanner

import "github.com/jammutkarsh/wandersort/pkg/util"

// ResolveAbsolute reconstructs an absolute file path from registry values.
func ResolveAbsolute(filePath, sourceRoot string) string {
	return util.NewUtil().MakeAbsolute(filePath, sourceRoot)
}
