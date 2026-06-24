// Package recipe defines the normalized recipe model the display renders,
// independent of the source format it was imported from (e.g. Paprika).
package recipe

import "strings"

// Recipe is one recipe reduced to what the panel shows: a title, a few optional
// metadata fields, and the ingredient and step lines.
type Recipe struct {
	Title       string
	Servings    string
	Time        string // human total time, e.g. "25 min"
	Source      string
	Ingredients []string
	Steps       []string
}

// SplitLines turns one of Paprika's newline-joined strings (ingredients or
// directions) into trimmed, non-empty lines. Blank separator lines are dropped;
// section headers like "For the sauce:" are kept verbatim.
func SplitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
