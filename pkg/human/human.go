// Package human writes counts the way people read them.
package human

import (
	"strconv"
	"strings"
)

// Count writes n with thousands separators: 15,481.
func Count(n int) string {
	if n < 0 {
		return "-" + Count(-n)
	}
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String()
}

// Plural puts n in front of one or many: "1 file", "15,481 files".
func Plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return Count(n) + " " + many
}
