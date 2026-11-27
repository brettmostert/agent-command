package tui

import "testing"

func TestGroupHeadersAreDrawnInTheirColor(t *testing.T) {
	for color, want := range map[string]string{
		"blue":         "\x1b[34mX" + reset,
		"bright-red":   "\x1b[91mX" + reset,
		"fixed-orange": "\x1b[38;2;249;145;87mX" + reset, // its dark-theme value
		"#010203":      "\x1b[38;2;1;2;3mX" + reset,
		"":             "X",
		"mauve":        "X",
	} {
		if got := colored("X", color); got != want {
			t.Errorf("%q: %q, want %q", color, got, want)
		}
	}
}
