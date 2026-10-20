package scanner

import "time"

// FileDiscovery is the lightweight struct used during directory walking.
// Dir is the file's absolute parent directory
type FileDiscovery struct {
	ModTime    time.Time
	Dir        string
	Name       string
	Extension  string
	VolumeUUID string
	MediaType  string
	Size       int64
}
