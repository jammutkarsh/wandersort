//go:build !darwin && !linux

package volume

import "errors"

// uuidForPath is unimplemented on this platform; ForPath degrades to ""
// ponytail: Windows needs GetVolumeInformationByHandleW — add when a Windows
// build actually ships
func uuidForPath(string) (string, error) {
	return "", errors.New("volume uuid not supported on this platform")
}
