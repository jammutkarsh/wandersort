// Package volume resolves the storage-volume UUID for filesystem paths, so
// files on external drives can be re-anchored when the drive remounts at a
// different location (drive letters on Windows, /Volumes/<name> on macOS).
package volume

import "sync"

// Resolver caches volume UUID lookups per path. Lookups shell out or read
// system tables, so scan roots (a handful per run) are the intended keys
type Resolver struct {
	mu    sync.Mutex
	cache map[string]string
}

func New() *Resolver {
	return &Resolver{cache: map[string]string{}}
}

// ForPath returns the UUID of the volume containing path. Best-effort: an
// unsupported platform or unresolvable volume yields "" rather than an error,
// because volume identity is advisory metadata, never a scan precondition
func (r *Resolver) ForPath(path string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.cache[path]; ok {
		return id
	}
	id, err := uuidForPath(path)
	if err != nil {
		id = ""
	}
	r.cache[path] = id
	return id
}
