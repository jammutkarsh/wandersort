package scanner

import "time"

// FileDiscovery is the lightweight struct used during directory walking.
// Dir is the file's absolute parent directory
type FileDiscovery struct {
	Dir        string
	Name       string
	Size       int64
	ModTime    time.Time
	Extension  string
	VolumeUUID string
	MediaType  string
}
