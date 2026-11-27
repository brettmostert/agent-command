package core

import (
	"fmt"
	"strconv"
	"strings"
)

// A group's color is one of three tiers (PAN-190):
//
//   - a theme color: one of the terminal theme's six, or its bright version
//     ("bright-blue"), so it always matches the theme;
//   - a fixed color ("fixed-orange"), with one exact value for dark themes
//     and one for light themes;
//   - a custom color, as "#RRGGBB".

// ThemeColors are the theme's 12 colors, in the order new groups take them.
var ThemeColors = []string{
	"blue", "magenta", "cyan", "yellow", "green", "red",
	"bright-blue", "bright-magenta", "bright-cyan", "bright-yellow", "bright-green", "bright-red",
}

// FixedColor is a color that doesn't change with the theme.
type FixedColor struct {
	Name        string // without "fixed-"
	Dark, Light string // #RRGGBB on a dark theme, and on a light one
}

// FixedColors are the 12 fixed colors, in the order pickers show them.
var FixedColors = []FixedColor{
	{"red", "#F2777A", "#C0392B"},
	{"orange", "#F99157", "#D35400"},
	{"amber", "#FFB454", "#B7791F"},
	{"yellow", "#FFE066", "#9A7D0A"},
	{"lime", "#B5E655", "#5E8C10"},
	{"green", "#66CC88", "#1E8449"},
	{"teal", "#4DD0C0", "#138D75"},
	{"cyan", "#66D9EF", "#0E7C9B"},
	{"blue", "#6699FF", "#1F5FBF"},
	{"indigo", "#8C8CFF", "#3F3FBF"},
	{"purple", "#C594F5", "#7D3C98"},
	{"pink", "#F58CC8", "#B03A7A"},
}

const fixedPrefix = "fixed-"

// NormalizeColor checks a group color and returns it as it's stored. A
// fixed color's name alone ("orange") means that fixed color, unless it's
// also a theme color's name ("red" is the theme's). A custom color is
// stored in capitals.
func NormalizeColor(c string) (string, error) {
	c = strings.ToLower(strings.TrimSpace(c))
	switch {
	case isThemeColor(c):
		return c, nil
	case fixedColor(strings.TrimPrefix(c, fixedPrefix)) != nil:
		return fixedPrefix + strings.TrimPrefix(c, fixedPrefix), nil
	case len(c) == 7 && c[0] == '#':
		if _, err := strconv.ParseUint(c[1:], 16, 32); err == nil {
			return strings.ToUpper(c), nil
		}
	}
	return "", fmt.Errorf("%q isn't a group color: use a theme color (blue, magenta, cyan, yellow, green, red, or bright-<one of those>), "+
		"a fixed one (%s), or #RRGGBB", c, strings.Join(fixedNames(), ", "))
}

// ColorHex is the exact value of a fixed or custom color, on a dark or a
// light theme. A theme color has none: "", false.
func ColorHex(c string, dark bool) (string, bool) {
	if strings.HasPrefix(c, "#") {
		return c, true
	}
	if f := fixedColor(strings.TrimPrefix(c, fixedPrefix)); f != nil && strings.HasPrefix(c, fixedPrefix) {
		if dark {
			return f.Dark, true
		}
		return f.Light, true
	}
	return "", false
}

func isThemeColor(c string) bool {
	for _, t := range ThemeColors {
		if t == c {
			return true
		}
	}
	return false
}

func fixedColor(name string) *FixedColor {
	for i := range FixedColors {
		if FixedColors[i].Name == name {
			return &FixedColors[i]
		}
	}
	return nil
}

func fixedNames() []string {
	out := make([]string, len(FixedColors))
	for i, f := range FixedColors {
		out[i] = f.Name
	}
	return out
}
