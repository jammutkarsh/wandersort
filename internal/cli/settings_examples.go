package cli

import (
	"slices"
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// settingsExamples renders the wizard's live tree previews and their paired description text.
type settingsExamples struct {
	rules     func() []string // the folder levels the answers so far stand for
	collapse  *bool
	mergeDays *bool
	dateOnly  *bool
	home      *string
}

func newSettingsExamples(rules func() []string, collapse, mergeDays, dateOnly *bool, home *string) *settingsExamples {
	return &settingsExamples{rules, collapse, mergeDays, dateOnly, home}
}

// exampleDay is the fixed date every wizard example uses.
func exampleDay(d int) time.Time { return time.Date(2024, time.August, d, 12, 0, 0, 0, time.UTC) }

// has reports whether the answers so far include folder level rule.
func (e *settingsExamples) has(rule string) bool { return slices.Contains(e.rules(), rule) }

// previewRules is lead plus the ticked collapsible levels, so each example
// shows its own question.
func (e *settingsExamples) previewRules(lead ...string) []string {
	rules := append([]string{}, lead...)
	for _, r := range []string{vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia} {
		if e.has(r) {
			rules = append(rules, r)
		}
	}
	return rules
}

// homeTown is what the saved-place examples name; a stand-in until the user
// has typed a town, so the example is never blank.
func (e *settingsExamples) homeTown() string {
	if t := strings.TrimSpace(*e.home); t != "" {
		city, _, _ := strings.Cut(t, ",")
		return city
	}
	return "Indore"
}

// Rules is the Rules field's own example.
func (e *settingsExamples) Rules() string {
	cfg := vfs.DefaultConfig()
	cfg.Rules = e.rules()
	cfg.CollapseLevels = false // always show every level, to demonstrate the order itself
	sample := vfs.Sample{
		TakenAt: exampleDay(2), Location: "Goa", Device: "iPhone 13",
		Width: 1170, Height: 2532, MediaType: classifier.MediaTypeImage, FileName: "IMG_1234.jpg",
	}
	return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{sample})...)
}

// collapsibleSelected is the camera, orientation and photo/video levels the
// answers so far include, in folder order.
func (e *settingsExamples) collapsibleSelected() []string {
	var levels []string
	for _, r := range []string{vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia} {
		if e.has(r) {
			levels = append(levels, r)
		}
	}
	return levels
}

// Collapse shows one camera's files with the question's answer applied: on, they
// sit straight in their day; off, the camera, orientation and photo/video
// folders are made anyway. A layout with none of those levels borrows all three.
func (e *settingsExamples) Collapse() string {
	cfg := vfs.DefaultConfig()
	levels := e.collapsibleSelected()
	if len(levels) == 0 {
		levels = []string{vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia}
	}
	cfg.Rules = nil
	for _, r := range []string{vfs.RuleDate, vfs.RuleLocation} {
		if e.has(r) {
			cfg.Rules = append(cfg.Rules, r)
		}
	}
	cfg.Rules = append(cfg.Rules, levels...)
	cfg.CollapseLevels = *e.collapse

	day := func(d int, name string) vfs.Sample {
		return vfs.Sample{
			TakenAt: exampleDay(d), Location: e.homeTown(), Device: "iPhone 13",
			Width: 1170, Height: 2532, MediaType: classifier.MediaTypeImage, FileName: name,
		}
	}
	samples := []vfs.Sample{day(12, "IMG_1234.jpg"), day(13, "IMG_1250.jpg"), day(13, "IMG_1251.jpg")}
	return treeExample("", vfs.PreviewPaths(cfg, samples)...)
}

// CollapseDescribe says what the question does; the answer does not change it.
func (e *settingsExamples) CollapseDescribe() string {
	d := "Leaves out a camera, portrait/landscape or photo/video folder that would hold every file in the library."
	if len(e.collapsibleSelected()) == 0 {
		d += " Your layout has none of these folders yet, so the example borrows them."
	}
	return d
}

// DateOnlyDescribe says what the question does; only the town's name follows the form.
func (e *settingsExamples) DateOnlyDescribe() string {
	return "Puts photos taken at home straight into their date folder. Without it they go in a " +
		e.homeTown() + " folder, like any trip."
}

func (e *settingsExamples) DateOnly() string {
	cfg := vfs.DefaultConfig()
	cfg.Rules = e.previewRules(vfs.RuleDate, vfs.RuleLocation)
	cfg.CollapseLevels = *e.collapse
	cfg.SavedPlacesDateOnly = *e.dateOnly
	sample := vfs.Sample{
		TakenAt: exampleDay(12), Location: e.homeTown(), AtSavedPlace: true, Device: "iPhone 13",
		Width: 1170, Height: 2532, MediaType: classifier.MediaTypeImage, FileName: "IMG_1234.jpg",
	}
	return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{sample})...)
}

// MergeDaysDescribe says what the question does; the answer does not change it.
func (e *settingsExamples) MergeDaysDescribe() string {
	return "Days in a row in one place share one folder. You can combine more folders in Organise."
}

func (e *settingsExamples) MergeDays() string {
	cfg := vfs.DefaultConfig()
	cfg.Rules = e.previewRules(vfs.RuleDate, vfs.RuleLocation)
	cfg.CollapseLevels = *e.collapse
	cfg.MergeSameLocationDays = *e.mergeDays
	trip := func(d int, file string) vfs.Sample {
		return vfs.Sample{
			TakenAt: exampleDay(d), Location: "Greece",
			Device: "iPhone 13", Width: 1170, Height: 2532,
			MediaType: classifier.MediaTypeImage, FileName: file,
		}
	}
	// the same three days either way; the setting decides whether they fold
	return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{
		trip(2, "IMG_1234.jpg"),
		trip(3, "IMG_1250.jpg"),
		trip(4, "IMG_1251.jpg"),
	})...)
}

// treeExample renders slash paths as the same guided tree the review screen
// draws, shared prefixes folded together
func treeExample(note string, paths ...string) string {
	type node struct {
		name string
		kids []*node
	}
	root := &node{}
	for _, p := range paths {
		cur := root
		for seg := range strings.SplitSeq(p, "/") {
			var next *node
			for _, k := range cur.kids {
				if k.name == seg {
					next = k
					break
				}
			}
			if next == nil {
				next = &node{name: seg}
				cur.kids = append(cur.kids, next)
			}
			cur = next
		}
	}
	var names []string
	var depths []int
	var walk func(nodes []*node, depth int)
	walk = func(nodes []*node, depth int) {
		for _, n := range nodes {
			names = append(names, n.name)
			depths = append(depths, depth)
			walk(n.kids, depth+1)
		}
	}
	walk(root.kids, 0)

	guides := tui.Guides(depths)
	var b strings.Builder
	for i, name := range names {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(guides[i])
		b.WriteString(name)
	}
	if note != "" {
		b.WriteString("\n\n")
		b.WriteString(note)
	}
	return b.String()
}
