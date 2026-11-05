package tui

import (
	"math"
	"strconv"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	var sum float64
	for i, weight := range []float64{0.2126, 0.7152, 0.0722} {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatalf("bad colour %q: %v", hex, err)
		}
		c := float64(v) / 255
		if c <= 0.03928 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		sum += weight * c
	}
	return sum
}

func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := luminance(t, a), luminance(t, b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// Every colour that carries text must stay readable on a plain light and on a
// plain dark terminal; Border is for lines and is left out.
func TestTextColoursAreReadable(t *testing.T) {
	const minRatio = 4.5
	text := map[string]lipgloss.AdaptiveColor{
		"Primary": Primary, "Success": Success, "Warn": Warn, "Error": Error,
		"Muted": Muted, "Subtle": Subtle, "Fg": Fg, "Folder": Folder,
	}
	for name, c := range text {
		for _, bg := range []string{"#FFFFFF"} {
			if got := contrast(t, c.Light, bg); got < minRatio {
				t.Errorf("%s on a light terminal: %.1f:1, want %.1f", name, got, minRatio)
			}
		}
		for _, bg := range []string{"#000000", "#1E1E1E"} {
			if got := contrast(t, c.Dark, bg); got < minRatio {
				t.Errorf("%s on a dark terminal (%s): %.1f:1, want %.1f", name, bg, got, minRatio)
			}
		}
	}
	if got := contrast(t, Fg.Light, Highlight.Light); got < minRatio {
		t.Errorf("Fg on Highlight (light): %.1f:1", got)
	}
	if got := contrast(t, Fg.Dark, Highlight.Dark); got < minRatio {
		t.Errorf("Fg on Highlight (dark): %.1f:1", got)
	}
}
