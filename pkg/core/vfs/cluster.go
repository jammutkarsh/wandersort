package vfs

import (
	"cmp"
	"slices"
	"time"
)

// maxFolderSpan is the longest a cluster may run and still put all its files
// in its start month. Longer clusters (a trip) keep each file's own month.
const maxFolderSpan = 24 * time.Hour

// cluster groups masters whose capture times sit within the configured gap of
// each other — one cluster ≈ one real-world event
type cluster struct {
	members    []int // indices into masters
	start, end time.Time
}

// sortKey is one master reduced to what the sort compares: 24 bytes instead of
// the whole masterFile, so a large library sorts in cache.
type sortKey struct {
	sec  int64
	idx  int   // indexes masters, so it holds whatever a slice can
	nsec int32 // nanosecond-within-second, 0..999999999 by definition
}

// sortByCaptureTime orders masters oldest-first, stably, by sorting compact
// keys and applying the permutation once.
func sortByCaptureTime(masters []masterFile) {
	keys := make([]sortKey, len(masters))
	for i := range masters {
		takenAt := masters[i].takenAt
		// Unix()+Nanosecond() orders identically to takenAt.Compare (no
		// monotonic readings here — every time comes from time.Parse) and,
		// unlike UnixNano, doesn't overflow on an undated file's zero time.
		keys[i] = sortKey{sec: takenAt.Unix(), idx: i, nsec: int32(takenAt.Nanosecond())}
	}
	// carrying idx in the key makes an unstable sort stable, which is what lets
	// this use the faster SortFunc
	slices.SortFunc(keys, func(a, b sortKey) int {
		if a.sec != b.sec {
			return cmp.Compare(a.sec, b.sec)
		}
		if a.nsec != b.nsec {
			return cmp.Compare(a.nsec, b.nsec)
		}
		return cmp.Compare(a.idx, b.idx)
	})
	// Apply the permutation in place, one cycle at a time: each master moves
	// once and idx is reset as it goes, so placed slots are skipped.
	for k := range keys {
		if keys[k].idx == k {
			continue
		}
		held := masters[k] // the one element a cycle can't move into place directly
		cur := k
		for {
			src := keys[cur].idx
			if src == k { // cycle closed — its last slot wants what we lifted out
				break
			}
			masters[cur] = masters[src]
			keys[cur].idx = cur
			cur = src
		}
		masters[cur] = held
		keys[cur].idx = cur
	}
}

// eventSegment renders the dated placeholder for an unresolved cluster. It
// sits under the month folder, so days alone suffice ("03", "03-05"); only a
// cross-month span keeps month names ("Jun_30-Jul_01").
func eventSegment(start, end time.Time) string {
	switch {
	case start.Year() == end.Year() && start.YearDay() == end.YearDay():
		return start.Format("02")
	case start.Year() == end.Year() && start.Month() == end.Month():
		return start.Format("02") + "-" + end.Format("02")
	default:
		return start.Format("Jan_02") + "-" + end.Format("Jan_02")
	}
}
