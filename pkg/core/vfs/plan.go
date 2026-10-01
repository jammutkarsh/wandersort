package vfs

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"golang.org/x/text/unicode/norm"
)

// Plan sets every master's targetPath. It touches no database and no files:
// derive facts, resolve locations, then assignTargetPaths.
func Plan(ctx context.Context, masters []masterFile, cfg Config, geo *location.Resolver, log logger.Logger) error {
	deriveAll(ctx, masters, cfg)
	resolveLocations(ctx, masters, cfg, geo, log)
	assignTargetPaths(ctx, masters, cfg)
	if ctx.Err() != nil { // don't leave a half-built proposal for persist to write
		return ctx.Err()
	}
	return nil
}

// assignTargetPaths sets every master's targetPath from its derived facts.
// Each step edits masters in place and reads what earlier steps wrote, so the
// step order is the rule. Plan and PreviewPaths both run all of it.
func assignTargetPaths(ctx context.Context, masters []masterFile, cfg Config) {
	hasLocationRule := slices.Contains(cfg.Rules, RuleLocation)

	// 1. Cluster by capture time (one cluster ≈ one event). Sorts masters.
	// Writes folderDate for short clusters, and clusterID + eventSegment for
	// clusters with nothing located. A GPS-less file never borrows a located
	// sibling's city: step 5 gives it an Unknown folder instead.
	gap := cfg.ClusterGap
	if gap <= 0 {
		gap = defaultClusterGap
	}
	sortByCaptureTime(masters)
	placed := slices.SortedFunc(slices.Values(cfg.placedTimes), time.Time.Compare)

	// Placed files move a cluster's start and end, so a new file continuing a
	// placed evening gets its month, but they are never members.
	var clusters []cluster
	addToCluster := func(t time.Time, member int) {
		if len(clusters) == 0 || t.Sub(clusters[len(clusters)-1].end) > gap {
			clusters = append(clusters, cluster{start: t, end: t})
		}
		c := &clusters[len(clusters)-1]
		if member >= 0 {
			c.members = append(c.members, member)
		}
		c.end = t
	}
	p := 0
	for i := range masters {
		for ; p < len(placed) && placed[p].Before(masters[i].takenAt); p++ {
			addToCluster(placed[p], -1)
		}
		addToCluster(masters[i].takenAt, i)
	}
	for ; p < len(placed); p++ {
		addToCluster(placed[p], -1)
	}

	clusterNum := 0
	for ci := range clusters {
		c := &clusters[ci]

		// An evening running past midnight into the next month is one event in
		// one folder. Longer clusters (a trip) keep each file's own month.
		if c.end.Sub(c.start) <= maxFolderSpan {
			for _, i := range c.members {
				masters[i].folderDate = c.start
			}
		}

		located := 0
		for _, i := range c.members {
			if masters[i].location != "" {
				located++
			}
		}
		if len(c.members) == 0 || located > 0 {
			continue // only placed files, or something located: nothing to decide
		}

		clusterNum++
		id := fmt.Sprintf("c%d", clusterNum)
		// the new files' own days, not the placed ones around them
		seg := eventSegment(masters[c.members[0]].takenAt, masters[c.members[len(c.members)-1]].takenAt)
		for _, i := range c.members {
			masters[i].clusterID = id
			masters[i].eventSegment = seg
		}
	}

	// 2. Title-case location and device names. Filenames are left alone.
	forEachMaster(ctx, masters, cfg.Workers, func(_ int, m *masterFile) {
		m.location = caseName(m.location)
		m.city = caseName(m.city)
		m.device = caseName(m.device)
	})

	// 3. skip = collapsible levels naming at most one folder library-wide.
	// Library-wide, not per branch, so the tree has one depth everywhere.
	var skip map[string]bool
	if cfg.CollapseLevels {
		seen := map[string]map[string]bool{}
		for _, level := range cfg.Rules {
			if collapsibleLevels[level] {
				seen[level] = map[string]bool{}
			}
		}
		for i := range masters {
			for level := range seen {
				if seg := segmentFor(&masters[i], level, cfg); seg != "" {
					seen[level][seg] = true
				}
			}
		}
		skip = map[string]bool{}
		for level, values := range seen {
			if len(values) <= 1 {
				skip[level] = true
			}
		}
	}

	// 4. SavedPlacesDateOnly drops the city folder for everyday shots, unless
	// the same parent folder also holds files from elsewhere: then those
	// saved-place files keep their folder (keepLocationFolder) rather than
	// sit loose beside nested neighbours. Must run before step 5, which needs
	// the lifted city to decide on Unknown.
	if cfg.SavedPlacesDateOnly && hasLocationRule {
		mixed := map[string]bool{}
		for i := range masters {
			if m := &masters[i]; hasLocationLevel(m) && !m.atSavedPlace {
				mixed[locationParent(m, skip, cfg)] = true
			}
		}
		for i := range masters {
			m := &masters[i]
			if m.atSavedPlace && hasLocationLevel(m) && mixed[locationParent(m, skip, cfg)] {
				m.keepLocationFolder = true
			}
		}
	}

	// 5. An unlocated file whose parent folder also holds located files gets
	// location = Unknown, so step 6 merges its days like any other place. A
	// folder whose files are all unlocated gets no Unknown level.
	if hasLocationRule {
		located := map[string]bool{}
		for i := range masters {
			if m := &masters[i]; hasLocationLevel(m) && segmentFor(m, RuleLocation, cfg) != "" {
				located[locationParent(m, skip, cfg)] = true
			}
		}
		for i := range masters {
			m := &masters[i]
			// atSavedPlace under SavedPlacesDateOnly is deliberately suppressed, not unknown
			if !hasLocationLevel(m) || m.atSavedPlace || segmentFor(m, RuleLocation, cfg) != "" {
				continue
			}
			if located[locationParent(m, skip, cfg)] {
				m.location = UnknownLocation
			}
		}
	}

	// 6. Merge consecutive same-location days into one range folder
	// (08/{02,03,04}/<city> → 08/02_04/<city>). Days are calendar dates, so a
	// run may cross a month end; the whole run takes its first day's
	// Year/Month. Writes dayOverride and folderDate. Location must sit at or
	// above Date in Rules, or the range folder wouldn't contain the location.
	dateIdx, locIdx := slices.Index(cfg.Rules, RuleDate), slices.Index(cfg.Rules, RuleLocation)
	if cfg.MergeSameLocationDays && dateIdx >= 0 && (locIdx < 0 || dateIdx <= locIdx) {
		// location → calendar days present
		days := map[string]map[int]bool{}
		for i := range masters {
			m := &masters[i]
			if !hasLocationLevel(m) || m.location == "" {
				continue
			}
			if days[m.location] == nil {
				days[m.location] = map[int]bool{}
			}
			days[m.location][calendarDay(m.takenAt)] = true
		}

		// dayRun is the folder a merged day lands in: its label and first day
		type dayRun struct {
			label string
			first int
		}

		// A day lives in exactly one date folder, so every file of a day must
		// agree on its run. A disagreeing day is dropped from merging and acts
		// as a break, which can change the runs around it, so repeat until no
		// new day breaks. Each pass only adds broken days, so it terminates.
		broken := map[int]bool{}
		var runs map[string]map[int]dayRun
		for {
			runs = map[string]map[int]dayRun{}
			for loc, set := range days {
				ds := make([]int, 0, len(set))
				for d := range set {
					if !broken[d] {
						ds = append(ds, d)
					}
				}
				sort.Ints(ds)
				// every day inside a run of 2 or more consecutive days
				for start := 0; start < len(ds); {
					end := start
					for end+1 < len(ds) && ds[end+1] == ds[end]+1 {
						end++
					}
					if end > start {
						lo, hi := ds[start], ds[end]
						r := dayRun{dayRange(dayStart(lo), dayStart(hi)), lo}
						if runs[loc] == nil {
							runs[loc] = map[int]dayRun{}
						}
						for d := lo; d <= hi; d++ {
							runs[loc][d] = r
						}
					}
					start = end + 1
				}
			}

			// one run per day, or the day breaks; a file with no location
			// votes for "no run"
			seen := map[int]dayRun{}
			found := false
			for i := range masters {
				m := &masters[i]
				if !hasLocationLevel(m) {
					continue
				}
				d := calendarDay(m.takenAt)
				if broken[d] {
					continue
				}
				r := runs[m.location][d]
				if prev, ok := seen[d]; ok && prev != r {
					broken[d] = true
					found = true
					continue
				}
				seen[d] = r
			}
			if !found {
				break
			}
		}

		for i := range masters {
			m := &masters[i]
			if !hasLocationLevel(m) || m.location == "" || broken[calendarDay(m.takenAt)] {
				continue
			}
			if r, ok := runs[m.location][calendarDay(m.takenAt)]; ok {
				m.dayOverride, m.folderDate = r.label, dayStart(r.first)
			}
		}
	}

	// 7. Build each targetPath: a directory per file, then a collision-free
	// file name in it.
	for i := range masters {
		masters[i].orderTime, masters[i].orderHash = masters[i].takenAt, masters[i].FileHash
	}
	groupDirs := captureDirs(masters, skip, cfg)
	pairLiveVideos(masters)

	// dirFor writes only its own master's dirLevels, so directories fan out.
	dirs := make([]string, len(masters))
	forEachMaster(ctx, masters, cfg.Workers, func(i int, m *masterFile) {
		if dir, ok := groupDirs[i]; ok {
			dirs[i] = dir // a capture group shares its leader's directory
		} else if m.MediaType == classifier.MediaTypeSidecar {
			// an unpaired sidecar has nothing to derive a folder from
			dirs[i], m.dirLevels, m.dirBounds = OrphanDir, []string{LevelOrphan}, []Bounds{{{}}}
		} else {
			dirs[i] = dirFor(m, skip, cfg)
		}
		// a placed folder the file matches completely wins over the rules
		if dir, ok := cfg.placedTree.route(m); ok {
			dirs[i] = dir
		}
	})

	// Names are assigned sequentially: who gets the bare name and who gets _2
	// depends on the order files are reached. Seeded with placed files' names,
	// which a new file must never take.
	taken := make(map[string]bool, len(cfg.Placed)+len(masters))
	for _, p := range cfg.Placed {
		taken[nameKey(p)] = true
	}

	// Order by the capture group leader's time and hash, then the file's own
	// name, never by source path, so any folder layout gives the same
	// suffixes. A sidecar ranks with its photo, which Apple Photos pairs it
	// with by name.
	order := make([]int, len(masters))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		ma, mb := &masters[a], &masters[b]
		return cmp.Or(
			ma.orderTime.Compare(mb.orderTime),
			strings.Compare(ma.orderHash, mb.orderHash),
			strings.Compare(ma.FileName, mb.FileName),
			strings.Compare(ma.absPath, mb.absPath),
		)
	})

	// A capture group takes one suffix: the lowest number free for all members.
	groups := map[string][]int{}
	for _, i := range order {
		if k := masters[i].pairKey; k != "" {
			groups[k] = append(groups[k], i)
		}
	}
	done := make([]bool, len(masters))
	for _, i := range order {
		if done[i] {
			continue
		}
		members := []int{i}
		if k := masters[i].pairKey; k != "" {
			members = groups[k]
		}
		paths := make([]string, len(members))
		for _, j := range members {
			done[j] = true
		}
		assignSuffix(taken, paths, func(k int) (string, string) {
			return dirs[members[k]], path.SanitizeFileName(masters[members[k]].FileName)
		})
		for k, j := range members {
			masters[j].targetPath = paths[k]
		}
	}
}

// Sample is one synthetic file for PreviewPaths: the derived facts a master
// carries after Plan's first half.
type Sample struct {
	TakenAt      time.Time
	Location     string // resolved city; "" = unknown
	AtSavedPlace bool
	Device       string
	Width        int64
	Height       int64
	MediaType    string // classifier.MediaTypeVideo, else treated as a photo
	FileName     string
}

// PreviewPaths runs assignTargetPaths over made-up files, so a settings example
// is the real proposal. Paths come back in capture-time order.
func PreviewPaths(cfg Config, samples []Sample) []string {
	masters := make([]masterFile, len(samples))
	for i, s := range samples {
		masters[i] = masterFile{
			FileName:     s.FileName,
			MediaType:    s.MediaType,
			takenAt:      s.TakenAt,
			location:     s.Location,
			atSavedPlace: s.AtSavedPlace,
			device:       s.Device,
			width:        s.Width,
			height:       s.Height,
		}
	}
	assignTargetPaths(context.Background(), masters, cfg)
	paths := make([]string, len(masters))
	for i := range masters {
		paths[i] = masters[i].targetPath
	}
	return paths
}

// forEachMaster runs fn over every master on `workers` goroutines (<= 1 runs
// the plain loop). fn may write only to its own master or slot i.
func forEachMaster(ctx context.Context, masters []masterFile, workers int, fn func(i int, m *masterFile)) {
	if workers <= 1 {
		for i := range masters {
			if ctx.Err() != nil {
				return
			}
			fn(i, &masters[i])
		}
		return
	}

	// Hand out contiguous runs, not single indices: one channel send per file
	// costs more than the work for cheap passes.
	const runSize = 512
	type run struct {
		base    int // index this run's first master sits at, for callers that need it
		masters []masterFile
	}
	runs := make(chan run, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for r := range runs {
				for i := range r.masters {
					fn(r.base+i, &r.masters[i])
				}
			}
		})
	}
	base := 0
	for chunk := range slices.Chunk(masters, runSize) {
		if ctx.Err() != nil {
			break
		}
		runs <- run{base, chunk}
		base += len(chunk)
	}
	close(runs)
	wg.Wait()
}

// deriveAll fills each master's derived fields from the stored metadata;
// files on disk are never read again.
func deriveAll(ctx context.Context, masters []masterFile, cfg Config) {
	forEachMaster(ctx, masters, cfg.Workers, func(_ int, m *masterFile) {
		m.takenAt = m.captureTime()
		if m.DBWidth != nil {
			m.width = *m.DBWidth
		}
		if m.DBHeight != nil {
			m.height = *m.DBHeight
		}
		// orientations 5-8 store the pixels rotated 90°/270°; swap so the
		// orientation level reflects how the shot is viewed
		if o := m.DBOrientation; o != nil && *o >= 5 && *o <= 8 {
			m.width, m.height = m.height, m.width
		}

		if m.DBLat != nil && m.DBLon != nil {
			m.hasGPS, m.lat, m.lon = true, *m.DBLat, *m.DBLon
		}

		m.device = deviceName(deref(m.DBMake), deref(m.DBModel))
	})
}

// captureTime is when m was shot, as wall-clock time: the EXIF dates, else the
// stored file date. CreationDate (iOS video) carries an offset, which
// stripOffset drops so every time is read the same way.
func (m *masterFile) captureTime() time.Time {
	return firstTime(deref(m.DBDateTaken), stripOffset(deref(m.DBCreationDate)), deref(m.DBCreateDate), deref(m.DBMediaCreateDate), m.ModifiedAt)
}

// resolveLocations reverse-geocodes every GPS-tagged master, then folds a
// city within range of a saved place into that place.
func resolveLocations(ctx context.Context, masters []masterFile, cfg Config, geo *location.Resolver, log logger.Logger) {
	if geo == nil {
		return
	}
	forEachMaster(ctx, masters, cfg.Workers, func(_ int, m *masterFile) {
		if !m.hasGPS {
			return
		}
		city, err := geo.Lookup(ctx, m.lat, m.lon)
		if err != nil {
			log.Debug("No location for coordinates", "lat", m.lat, "lon", m.lon, "error", err)
			return
		}
		m.location, m.city = city, city
		for _, a := range cfg.Anchors {
			dLat, dLon := m.lat-a.Lat, m.lon-a.Lon
			if dLat*dLat+dLon*dLon <= location.MaxDistSquared {
				m.location = a.FolderName
				m.atSavedPlace = true
				break
			}
		}
	})
}

// fallbackDir is the folder for files with no date at all.
const fallbackDir = "Unsorted"

// UnknownLocation is the location folder a file with no resolvable place gets
// when located siblings share its parent folder.
const UnknownLocation = "Unknown"

// hasLocationLevel reports whether dirFor emits a location level for m at all
// — an undated file goes straight to Fallback and a screenshot to Screenshots.
func hasLocationLevel(m *masterFile) bool {
	return !m.takenAt.IsZero() && !m.IsScreenshot
}

// locationParent is the folder path m's location level sits under: everything
// dirFor emits above it. Files sharing it are siblings at that level.
//
// ponytail: computed pre-merge, so a located sibling that the day merge (step 6)
// later lifts into a day *range* leaves the Unknown behind alone in its day.
// Order the two passes properly if that shows up in practice.
func locationParent(m *masterFile, skip map[string]bool, cfg Config) string {
	parts := monthParts(m)
	for _, level := range cfg.Rules {
		if level == RuleLocation {
			break
		}
		if skip[level] {
			continue
		}
		if seg := segmentFor(m, level, cfg); seg != "" {
			parts = append(parts, path.SanitizeSegment(seg))
		}
	}
	return strings.Join(parts, "/")
}

// crossesFolderMonth reports whether m was shot in a different month from its
// folder's (a cluster or day run crossing a month end). Such a file gets a
// month-qualified day folder and its full date in its folders' bounds.
func crossesFolderMonth(m *masterFile) bool {
	t, f := m.takenAt, m.folderTime()
	return t.Year() != f.Year() || t.Month() != f.Month()
}

// calendarDay numbers t's date so consecutive dates are consecutive ints
// across a month or year end, which day-of-month is not.
func calendarDay(t time.Time) int {
	return int(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// dayStart is the midnight calendarDay numbered d.
func dayStart(d int) time.Time {
	return time.Unix(int64(d)*86400, 0).UTC()
}

// dayRange names a merged run: "28_31" inside one month, eventSegment's
// cross-month shape ("Aug_28-Sep_04", "Dec_30-Jan_02") across one.
func dayRange(lo, hi time.Time) string {
	if lo.Year() == hi.Year() && lo.Month() == hi.Month() {
		return fmt.Sprintf("%02d_%02d", lo.Day(), hi.Day())
	}
	return eventSegment(lo, hi)
}

// assignSuffix fills paths with dir/stem[_N]ext for every member, using the
// lowest N (1 = no suffix) free for all of them, and marks them taken. Shared
// by assignTargetPaths and Confirm so both agree on what a collision is.
func assignSuffix(taken map[string]bool, paths []string, member func(k int) (dir, name string)) {
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = fmt.Sprintf("_%d", n)
		}
		free := true
		seen := make(map[string]bool, len(paths))
		for k := range paths {
			dir, name := member(k)
			ext := filepath.Ext(name)
			paths[k] = dir + "/" + strings.TrimSuffix(name, ext) + suffix + ext
			key := nameKey(paths[k])
			if taken[key] || seen[key] {
				free = false
				break
			}
			seen[key] = true
		}
		if free {
			for _, p := range paths {
				taken[nameKey(p)] = true
			}
			return
		}
	}
}

// pairLiveVideos puts a Live Photo's video in its photo's capture group for
// the suffix and ordering key only, never the folder (videos stay out of
// captureDirs). Pairs within liveVideoWindow; closest in time wins.
func pairLiveVideos(masters []masterFile) {
	photos := map[string][]int{}
	for i := range masters {
		if masters[i].MediaType == classifier.MediaTypeVideo || masters[i].MediaType == classifier.MediaTypeSidecar {
			continue
		}
		key := masters[i].FileDir + "|" + captureStem(masters[i].FileName)
		photos[key] = append(photos[key], i)
	}
	for i := range masters {
		v := &masters[i]
		if v.MediaType != classifier.MediaTypeVideo {
			continue
		}
		var best *masterFile
		var bestGap time.Duration
		for _, j := range photos[v.FileDir+"|"+captureStem(v.FileName)] {
			p := &masters[j]
			gap := v.takenAt.Sub(p.takenAt).Abs()
			if gap > liveVideoWindow || (v.device != "" && p.device != "" && v.device != p.device) {
				continue
			}
			if best == nil || cmp.Or(cmp.Compare(gap, bestGap), strings.Compare(p.orderHash, best.orderHash)) < 0 {
				best, bestGap = p, gap
			}
		}
		if best == nil {
			continue
		}
		if best.pairKey == "" {
			best.pairKey = best.absPath // ungrouped photo: its own path names the pair
		}
		v.pairKey, v.orderTime, v.orderHash = best.pairKey, best.orderTime, best.orderHash
	}
}

// nameKey is what two paths are compared by: NFC and case-folded, because
// Café in NFC and NFD, or IMG.JPG and img.jpg, are one name on macOS and Windows.
func nameKey(p string) string {
	return strings.ToLower(norm.NFC.String(p))
}

// variantPrefixes fold a phone's edited/original filename markers to the
// canonical form, so IMG_E1783 and IMG_O1783 group with IMG_1783.
var variantPrefixes = []struct{ variant, canonical string }{
	{"IMG_E", "IMG_"},
	{"IMG_O", "IMG_"},
}

// captureAgreementWindow is how far apart two EXIF capture times may sit and
// still be one capture: an edit is written seconds after its original, while a
// reused filename counter is hours or days apart.
const captureAgreementWindow = 5 * time.Minute

// liveVideoWindow is how far a Live Photo's video may sit from its photo: one
// shutter press.
const liveVideoWindow = time.Second

// captureStem normalizes a filename to the key used to group same-capture
// files: strip the extension, then fold a known variant prefix.
func captureStem(filename string) string {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	for _, p := range variantPrefixes {
		if strings.HasPrefix(base, p.variant) {
			return p.canonical + base[len(p.variant):]
		}
	}
	return base
}

// hasExifTime reports whether m's takenAt came from a real EXIF tag rather
// than deriveAll's file-mtime fallback — exif never runs on a sidecar, so
// this is false for every .AAE regardless of what takenAt ended up holding.
func (m *masterFile) hasExifTime() bool {
	return deref(m.DBDateTaken) != "" || deref(m.DBCreationDate) != "" || deref(m.DBCreateDate) != "" || deref(m.DBMediaCreateDate) != ""
}

// captureDirs finds files that are one capture split across extensions
// (edit/sidecar bundle, RAW+JPG) and returns their shared directory by index.
func captureDirs(masters []masterFile, skip map[string]bool, cfg Config) map[int]string {
	type group struct{ members []int }
	groups := map[string]*group{}
	for i := range masters {
		// videos stay out so a Live Photo video isn't pushed across the
		// Photos/Videos split
		if masters[i].MediaType == classifier.MediaTypeVideo {
			continue
		}
		// candidate key: same source directory + same captureStem
		key := masters[i].FileDir + "|" + captureStem(masters[i].FileName)
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}
		g.members = append(g.members, i)
	}

	dirs := map[int]string{}
	for key, g := range groups {
		if len(g.members) < 2 {
			continue
		}
		// A group needs its EXIF-timed members to agree on time (filename
		// counters get reused) and, where known, on device. A sidecar has no
		// EXIF time and rides along.
		var lo, hi time.Time
		device := ""
		deviceMismatch := false
		for _, i := range g.members {
			m := &masters[i]
			if !m.hasExifTime() {
				continue
			}
			switch t := m.takenAt; {
			case lo.IsZero():
				lo, hi = t, t
			case t.Before(lo):
				lo = t
			case t.After(hi):
				hi = t
			}
			if m.device != "" {
				switch {
				case device == "":
					device = m.device
				case device != m.device:
					deviceMismatch = true
				}
			}
		}
		if lo.IsZero() || hi.Sub(lo) > captureAgreementWindow || deviceMismatch {
			continue // can't safely anchor this group — leave members independent
		}

		// leader: screenshot (Rules don't apply to it), then non-sidecar, then
		// located over unlocated, then canonical filename, then input order
		leader, bestScore := g.members[0], -1
		for _, i := range g.members {
			score := 0
			if masters[i].IsScreenshot {
				score += 8
			}
			if masters[i].MediaType != classifier.MediaTypeSidecar {
				score += 4
			}
			if masters[i].location != "" {
				score += 2
			}
			name := masters[i].FileName
			if captureStem(name) == strings.TrimSuffix(name, filepath.Ext(name)) {
				score++
			}
			if score > bestScore {
				leader, bestScore = i, score
			}
		}
		dir := dirFor(&masters[leader], skip, cfg)
		for _, i := range g.members {
			dirs[i] = dir
			// the leader's levels come with its directory, or the member has
			// no location folder (and no GPS for review renames)
			masters[i].dirLevels = masters[leader].dirLevels
			masters[i].dirBounds = masters[leader].dirBounds
			// the leader's time and hash also rank the member when names
			// collide, so a sidecar keeps its photo's suffix
			masters[i].orderTime = masters[leader].takenAt
			masters[i].orderHash = masters[leader].FileHash
			masters[i].pairKey = key
			// and its folder date, or a sidecar (file mtime only) lands in a
			// different month than its path
			//
			// ponytail: an undated leader has no folder time to copy, so a dated
			// member still lands in its own Fallback folder. Give masterFile an
			// explicit folder time if that turns up.
			masters[i].folderDate = masters[leader].folderTime()
		}
	}
	return dirs
}

// monthParts is the Year and Month folder pair from folderTime, shared by
// dirFor and locationParent so they agree on a file's month.
func monthParts(m *masterFile) []string {
	t := m.folderTime()
	return []string{
		strconv.Itoa(t.Year()),
		// number-first so months sort chronologically in the review tree,
		// Finder and ls; bare names put December above November
		t.Format("01_January"),
	}
}

// dirFor derives the directory segments for one master, honouring Rules
// order. skip names the levels assignTargetPaths step 3 found nothing to say with.
func dirFor(m *masterFile, skip map[string]bool, cfg Config) string {
	if m.takenAt.IsZero() {
		m.dirLevels, m.dirBounds = []string{LevelFallback}, []Bounds{{{}}}
		return fallbackDir
	}

	parts := monthParts(m)
	levels := []string{LevelYear, LevelMonth}
	bounds := []Bounds{{boundsFor(m, LevelYear)}, {boundsFor(m, LevelMonth)}}

	// A screenshot has no location/device/orientation worth a folder of its
	// own — group every screenshot in the month together instead of letting
	// the configured Rules fragment them.
	if m.IsScreenshot {
		m.dirLevels, m.dirBounds = append(levels, LevelScreenshots), append(bounds, Bounds{{}})
		return strings.Join(append(parts, "Screenshots"), "/")
	}

	for _, level := range cfg.Rules {
		if skip[level] {
			continue
		}
		seg := segmentFor(m, level, cfg)
		if seg == "" {
			continue // level not derivable for this file — skip the folder
		}
		parts = append(parts, path.SanitizeSegment(seg))
		levels = append(levels, level)
		bounds = append(bounds, Bounds{boundsFor(m, level)})
	}
	m.dirLevels, m.dirBounds = levels, bounds
	return strings.Join(parts, "/")
}

// boundsFor is the constraint one dirFor folder puts on m: the value the
// segment came from, not its name.
func boundsFor(m *masterFile, level string) Constraint {
	switch level {
	case LevelYear:
		if m.takenAt.Year() != m.folderTime().Year() {
			return fullDate(m)
		}
		return Constraint{Year: []int{m.folderTime().Year()}}
	case LevelMonth:
		if crossesFolderMonth(m) {
			return fullDate(m)
		}
		return Constraint{Month: []int{int(m.folderTime().Month())}}
	case RuleDate:
		return dayBounds(m)
	case RuleLocation:
		if m.location == "" {
			return dayBounds(m) // a dated event segment standing in for a place
		}
		places := []string{m.location}
		if m.city != "" && m.city != m.location {
			places = append(places, m.city)
			slices.Sort(places)
		}
		return Constraint{Location: places}
	case RuleDevice:
		return Constraint{Device: []string{m.device}}
	case RuleOrientation, RuleMedia:
		seg := segmentFor(m, level, Config{})
		if level == RuleOrientation {
			return Constraint{Orientation: []string{seg}}
		}
		return Constraint{Media: []string{seg}}
	}
	return Constraint{}
}

// dayBounds is the day m was shot, for a folder named after dates.
func dayBounds(m *masterFile) Constraint {
	if crossesFolderMonth(m) {
		return fullDate(m)
	}
	return Constraint{Date: []int{m.takenAt.Day()}}
}

// fullDate is the year+month+day alternative a file shot outside its folder's
// month adds to its folders' bounds; separate month and day sets would admit
// dates that were never there.
func fullDate(m *masterFile) Constraint {
	t := m.takenAt
	return Constraint{Year: []int{t.Year()}, Month: []int{int(t.Month())}, Date: []int{t.Day()}}
}

// segmentFor is the folder name one grouping level gives this file, or "" when
// the level isn't derivable for it (unknown device, no dimensions).
func segmentFor(m *masterFile, level string, cfg Config) string {
	switch level {
	case RuleLocation:
		// SavedPlacesDateOnly: an everyday place gets no location folder, just
		// the (possibly merged) date range — m.location itself stays real,
		// it's only the folder that's suppressed. Unless the day holds files
		// from elsewhere too; see assignTargetPaths step 4.
		if m.atSavedPlace && cfg.SavedPlacesDateOnly && !m.keepLocationFolder {
			return ""
		}
		// ladder: resolved city → dated event segment → nothing. No device
		// fallback: an unknown location says nothing rather than something false.
		switch {
		case m.location != "":
			return m.location
		// a Day level already carries the date; a second one beside it reads
		// as "…/03/03-05/"
		case m.eventSegment != "" && !slices.Contains(cfg.Rules, RuleDate):
			return m.eventSegment
		default:
			return ""
		}
	case RuleDate:
		if m.dayOverride != "" {
			return m.dayOverride // merged consecutive same-location day range
		}
		if crossesFolderMonth(m) {
			// the cluster filed this file under another month; a bare "02"
			// would read as that month's day and collide with it
			return m.takenAt.Format("Jan_02")
		}
		return m.takenAt.Format("02")
	case RuleDevice:
		return m.device
	case RuleOrientation:
		if m.width == 0 || m.height == 0 {
			return ""
		}
		if m.height > m.width {
			return "Vertical"
		}
		return "Horizontal"
	case RuleMedia:
		if m.MediaType == classifier.MediaTypeVideo {
			return "Videos"
		}
		return "Photos"
	}
	return ""
}

// collapsibleLevels are worth dropping when they carry no information. Date
// and location never collapse — they're how a person recognizes a folder;
// [m] in the review TUI is the deliberate way to fold days together.
var collapsibleLevels = map[string]bool{
	RuleDevice:      true,
	RuleOrientation: true,
	RuleMedia:       true,
}

/* small parsing helpers — exiftool values arrive as strings */

// stripOffset removes a trailing timezone offset ("+05:30", "Z") so the value
// parses as the naive wall-clock every other capture-time tag is (see deriveAll).
// Exiftool dates use colons, so the offset is the only '+'/'-' in the string.
func stripOffset(s string) string {
	s = strings.TrimSuffix(s, "Z")
	if i := strings.LastIndexAny(s, "+-"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// looseTimeLayouts is tried in order, so the plain exiftool shape leads: it is
// what almost every DateTimeOriginal parses as, and every layout ahead of it is
// a parse attempt that fails for the common case.
var looseTimeLayouts = []string{
	"2006:01:02 15:04:05",
	"2006:01:02 15:04:05.999999999-07:00",
	"2006:01:02 15:04:05-07:00",
	"2006:01:02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999", // db.TimeLayout: stored file dates
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseTimeLoose(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range looseTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			// Some devices write a bogus CreateDate (e.g. a QuickTime
			// epoch/offset bug landing pre-1970) that still parses cleanly;
			// reject it so firstTime falls through to the next candidate
			// (MediaCreateDate) instead of taking a wrong-but-valid date.
			if t.Year() < 1970 {
				return time.Time{}, false
			}
			return t, true
		}
	}
	return time.Time{}, false
}

// firstTime returns the first candidate that parses as a timestamp
func firstTime(candidates ...string) time.Time {
	for _, c := range candidates {
		if t, ok := parseTimeLoose(c); ok {
			return t
		}
	}
	return time.Time{}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// caseWhitelist are words whose mixed casing is already correct and must
// survive title-casing. Matched case-insensitively.
var caseWhitelist = map[string]string{
	"iphone": "iPhone",
}

// isWordDelim reports whether r separates words in a derived name.
func isWordDelim(r rune) bool {
	return r == ' ' || r == '_' || r == '-'
}

// caseName title-cases a derived name, preserving words in caseWhitelist.
func caseName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	start := 0
	for i, r := range name {
		if !isWordDelim(r) {
			continue
		}
		b.WriteString(titleWord(name[start:i]))
		b.WriteRune(r)
		start = i + 1
	}
	b.WriteString(titleWord(name[start:]))
	return b.String()
}

// titleWord title-cases a single word unless it matches caseWhitelist.
func titleWord(w string) string {
	if exact, ok := caseWhitelist[strings.ToLower(w)]; ok {
		return exact
	}
	if w == "" {
		return ""
	}
	runes := []rune(w)
	runes[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}

// deviceName joins Make and Model, skipping the make when the model already
// starts with it.
func deviceName(mk, model string) string {
	switch {
	case model == "":
		return mk
	case mk == "" || strings.Contains(strings.ToLower(model), strings.ToLower(mk)):
		return model
	default:
		return mk + " " + model
	}
}
