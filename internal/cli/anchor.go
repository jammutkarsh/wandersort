package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"golang.org/x/term"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// syncAnchorsFromConfig ensures the home/work anchors saved globally (see
// `wandersort setup`) exist as ANCHOR_HOME/ANCHOR_WORK user_labels in this
// library's DB — anchors are a global setting, but resolveLocations reads them
// per-library, so each library's DB needs its own copy. Idempotent and silent
// once synced; a library with no global anchors set is a no-op.
func (a *App) syncAnchorsFromConfig(ctx context.Context) error {
	g, err := config.LoadGlobal()
	if err != nil {
		a.Log.Warn("Could not read global config, skipping anchor sync", "error", err)
		return nil
	}
	if a.LocationResolver == nil {
		return nil
	}

	for _, anchor := range []struct{ name, kind string }{
		{g.Anchors.Home, "ANCHOR_HOME"},
		{g.Anchors.Work, "ANCHOR_WORK"},
	} {
		if anchor.name == "" {
			continue
		}
		var exists int
		if err := a.AppDB.SQL.GetContext(ctx, &exists,
			`SELECT COUNT(*) FROM user_labels WHERE kind = ? AND label = ?`, anchor.kind, anchor.name); err != nil {
			return fmt.Errorf("check anchor %q: %w", anchor.name, err)
		}
		if exists > 0 {
			continue
		}
		lat, lon, err := a.LocationResolver.ResolveByName(ctx, anchor.name)
		if err != nil {
			a.Log.Warn("Could not resolve saved anchor town", "town", anchor.name, "error", err)
			continue
		}
		if !a.AppDB.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO user_labels (label, kind, gps_lat, gps_lon) VALUES (?, ?, ?, ?)`,
				anchor.name, anchor.kind, lat, lon)
			return err
		}) {
			return fmt.Errorf("save anchor %q: writer closed", anchor.name)
		}
		a.Log.Info("Synced anchor for this library", logger.UserKey, true, "town", anchor.name, "kind", anchor.kind)
	}
	return nil
}

// promptAndSaveAnchors asks for home/work town (whichever isn't already set
// globally) and saves them to ~/.wandersort/config.yaml. Skipped outside an
// interactive terminal so a scripted `setup` never hangs. Names come from
// PlaceMatch (SearchByName), not free text, so they can never drift from what
// the location DB actually has — ResolveByName later is a guaranteed exact hit.
// Work defaults to "same as Home" (very common — most people don't commute
// across metros) with a one-key override to search for a different town.
func (a *App) promptAndSaveAnchors(ctx context.Context) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil
	}
	g, err := config.LoadGlobal()
	if err != nil {
		return fmt.Errorf("read global config: %w", err)
	}
	if g.Anchors.Home != "" && g.Anchors.Work != "" {
		return nil
	}
	if a.LocationResolver == nil {
		return nil
	}

	reader := bufio.NewReader(os.Stdin)
	changed := false
	if g.Anchors.Home == "" {
		if name, ok := pickPlace(ctx, reader, a.LocationResolver, "Home town"); ok {
			g.Anchors.Home = name
			changed = true
		}
	}
	if g.Anchors.Work == "" {
		if g.Anchors.Home != "" {
			fmt.Fprintf(os.Stderr, "Work town same as Home (%s)? [Y/n]: ", g.Anchors.Home)
			if sameAsHomeAnswer(readLine(reader)) {
				g.Anchors.Work = g.Anchors.Home
				changed = true
			} else if name, ok := pickPlace(ctx, reader, a.LocationResolver, "Work town"); ok {
				g.Anchors.Work = name
				changed = true
			}
		} else if name, ok := pickPlace(ctx, reader, a.LocationResolver, "Work town"); ok {
			g.Anchors.Work = name
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := config.SaveAnchors(g.Anchors.Home, g.Anchors.Work); err != nil {
		return fmt.Errorf("save global config: %w", err)
	}
	a.Log.Info("Saved home/work anchors — every library will fold nearby suburbs into them", logger.UserKey, true)
	return nil
}

// placeSearcher is the seam pickPlace needs — *location.Resolver satisfies it —
// so the prompt/select loop is testable without a real location.db fixture.
type placeSearcher interface {
	SearchByName(ctx context.Context, prefix string, limit int) ([]location.PlaceMatch, error)
}

// pickPlace prompts for a place name and, once the location DB has matches,
// shows them numbered so the reviewer picks an exact gazetteer entry instead
// of typing free text that might not resolve. Typing something other than a
// valid number re-searches with that text; blank at any point skips. Typing
// the exact name of one of the matches (case-insensitively) selects it right
// away — no point asking the user to also type the number of the thing they
// already spelled out correctly.
func pickPlace(ctx context.Context, reader *bufio.Reader, resolver placeSearcher, label string) (string, bool) {
	fmt.Fprintf(os.Stderr, "%s (blank to skip): ", label)
	text := readLine(reader)
	for text != "" {
		matches, err := resolver.SearchByName(ctx, text, 8)
		if err != nil || len(matches) == 0 {
			fmt.Fprint(os.Stderr, "  no match, try again (blank to skip): ")
			text = readLine(reader)
			continue
		}
		for _, m := range matches {
			if strings.EqualFold(m.Name, text) {
				return m.Name, true
			}
		}
		for i, m := range matches {
			fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, m.Name)
		}
		fmt.Fprint(os.Stderr, "  number to select, or type to search again: ")
		sel := readLine(reader)
		if idx, err := strconv.Atoi(sel); err == nil && idx >= 1 && idx <= len(matches) {
			return matches[idx-1].Name, true
		}
		text = sel
	}
	return "", false
}

func readLine(r *bufio.Reader) string {
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

// sameAsHomeAnswer parses the reply to "Work town same as Home?" — blank
// (just pressing enter) or y/yes means yes, matching the [Y/n] default shown.
func sameAsHomeAnswer(ans string) bool {
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}
