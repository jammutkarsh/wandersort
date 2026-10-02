package human

import "testing"

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "0 files", 1: "1 file", 999: "999 files", 15481: "15,481 files", 1234567: "1,234,567 files", -1000: "-1,000 files"} {
		if got := Plural(n, "file", "files"); got != want {
			t.Errorf("Plural(%d) = %q, want %q", n, got, want)
		}
	}
}
