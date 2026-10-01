// Package volume resolves volume UUIDs and storage classes for paths, so files
// on external drives can be re-anchored when the drive remounts elsewhere.
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

// ForPath returns the UUID of the volume containing path, or "" when it can't
// be resolved.
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
