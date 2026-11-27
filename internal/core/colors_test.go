package core

import "testing"

func TestGroupColorsComeInThreeTiers(t *testing.T) {
	for in, want := range map[string]string{
		"blue": "blue", "Bright-Red": "bright-red", "orange": "fixed-orange", "fixed-red": "fixed-red",
		"red": "red", "#0a0B0c": "#0A0B0C",
	} {
		if got, err := NormalizeColor(in); err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "mauve", "bright-pink", "fixed-", "#12345", "#1234567", "#xyzxyz", "#+12345"} {
		if _, err := NormalizeColor(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if len(ThemeColors) != 12 || len(FixedColors) != 12 {
		t.Errorf("tiers of %d and %d, want 12 each", len(ThemeColors), len(FixedColors))
	}
	if h, ok := ColorHex("fixed-teal", true); !ok || h != "#4DD0C0" {
		t.Errorf("teal on dark: %q", h)
	}
	if h, ok := ColorHex("fixed-teal", false); !ok || h != "#138D75" {
		t.Errorf("teal on light: %q", h)
	}
	if h, ok := ColorHex("#ABCDEF", false); !ok || h != "#ABCDEF" {
		t.Errorf("custom: %q", h)
	}
	for _, theme := range []string{"blue", "bright-blue", "teal"} {
		if _, ok := ColorHex(theme, true); ok {
			t.Errorf("%q has no exact value", theme)
		}
	}
}

func TestAColorIsTrimmed(t *testing.T) {
	if got, err := NormalizeColor("  cyan\n"); err != nil || got != "cyan" {
		t.Errorf("%q, %v", got, err)
	}
}
