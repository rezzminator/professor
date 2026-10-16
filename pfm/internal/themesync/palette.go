package themesync

import (
	"fmt"
	"strings"
)

// withState derives the four state colours every surface draws: blocked is
// the scheme's red, working its yellow, done its green, idle its muted grey.
func (variant Variant) withState() Variant {
	variant.State = StateColors{
		Blocked: variant.ANSI[1], Working: variant.ANSI[3], Done: variant.ANSI[2], Idle: variant.Muted,
	}
	return variant
}

// validate checks that every colour of the variant is a lowercase #rrggbb,
// naming the first field that is not.
func (variant Variant) validate() error {
	fields := []struct{ name, value string }{
		{"background", variant.Background},
		{"foreground", variant.Foreground},
		{"cursor", variant.Cursor},
		{"selectionBackground", variant.SelectionBackground},
		{"selectionForeground", variant.SelectionForeground},
		{"surface", variant.Surface},
		{"overlay", variant.Overlay},
		{"muted", variant.Muted},
		{"accent", variant.Accent},
		{"state.blocked", variant.State.Blocked},
		{"state.working", variant.State.Working},
		{"state.done", variant.State.Done},
		{"state.idle", variant.State.Idle},
	}
	for index, value := range variant.ANSI {
		fields = append(fields, struct{ name, value string }{fmt.Sprintf("ansi[%d]", index), value})
	}
	for _, field := range fields {
		parsed, err := parseHexColor(field.value)
		if err != nil || parsed.String() != field.value {
			return fmt.Errorf("themesync: %s = %q is not a lowercase #rrggbb", field.name, field.value)
		}
	}
	return nil
}

// embeddedPalettes holds the three shipped palettes. Terminal colours (ANSI
// 0–15, background, foreground, cursor, selection) come from Ghostty's bundled
// theme files, named below relative to Ghostty.app/Contents/Resources/ghostty/themes/;
// the UI roles (surface, overlay, muted, accent) are each scheme's own UI colours.
// paletteColours is how many colours a variant spec lists: background,
// foreground, cursor, selection background and foreground, surface, overlay,
// muted, accent, then ANSI 0–15.
const paletteColours = 25

// variantOf builds a variant from its space-separated colour spec (the order
// paletteColours names) and derives its state colours. A malformed spec yields
// the zero variant, which validate rejects by name.
func variantOf(spec string) Variant {
	colours := strings.Fields(spec)
	if len(colours) != paletteColours {
		return Variant{}
	}
	variant := Variant{
		Background: colours[0], Foreground: colours[1], Cursor: colours[2],
		SelectionBackground: colours[3], SelectionForeground: colours[4],
		Surface: colours[5], Overlay: colours[6], Muted: colours[7], Accent: colours[8],
	}
	copy(variant.ANSI[:], colours[9:])
	return variant.withState()
}

var embeddedPalettes = []Palette{
	{
		Name: "tokyo-night", Title: "Tokyo Night", HerdrDark: "tokyo-night", HerdrLight: "tokyo-night-day",
		// TokyoNight Night; UI roles are Tokyo Night's bg_dark-storm, bg_highlight, comment and blue.
		Dark: variantOf(
			"#1a1b26 #c0caf5 #c0caf5 #283457 #c0caf5 #1f2335 #292e42 #565f89 #7aa2f7" +
				" " + "#15161e #f7768e #9ece6a #e0af68 #7aa2f7 #bb9af7 #7dcfff #a9b1d6" +
				" " + "#414868 #f7768e #9ece6a #e0af68 #7aa2f7 #bb9af7 #7dcfff #c0caf5",
		),
		// TokyoNight Day; UI roles are Tokyo Night Day's bg_dark, bg_highlight, comment and blue.
		Light: variantOf(
			"#e1e2e7 #3760bf #3760bf #99a7df #3760bf #d0d5e3 #c4c8da #848cb5 #2e7de9" +
				" " + "#e9e9ed #f52a65 #587539 #8c6c3e #2e7de9 #9854f1 #007197 #6172b0" +
				" " + "#a1a6c5 #f52a65 #587539 #8c6c3e #2e7de9 #9854f1 #007197 #3760bf",
		),
	},
	{
		Name: "solarized", Title: "Solarized", HerdrDark: "solarized", HerdrLight: "solarized-light",
		// iTerm2 Solarized Dark; UI roles are base02, base01, base00 and blue.
		Dark: variantOf(
			"#002b36 #839496 #839496 #073642 #93a1a1 #073642 #586e75 #657b83 #268bd2" +
				" " + "#073642 #dc322f #859900 #b58900 #268bd2 #d33682 #2aa198 #eee8d5" +
				" " + "#335e69 #cb4b16 #586e75 #657b83 #839496 #6c71c4 #93a1a1 #fdf6e3",
		),
		// iTerm2 Solarized Light; UI roles are base2, base1, base0 and blue.
		Light: variantOf(
			"#fdf6e3 #657b83 #657b83 #eee8d5 #586e75 #eee8d5 #93a1a1 #839496 #268bd2" +
				" " + "#073642 #dc322f #859900 #b58900 #268bd2 #d33682 #2aa198 #bbb5a2" +
				" " + "#002b36 #cb4b16 #586e75 #657b83 #839496 #6c71c4 #93a1a1 #fdf6e3",
		),
	},
	{
		Name: "catppuccin", Title: "Catppuccin", HerdrDark: "catppuccin", HerdrLight: "catppuccin-latte",
		// Catppuccin Mocha; UI roles are surface0, surface1, overlay1 and mauve.
		Dark: variantOf(
			"#1e1e2e #cdd6f4 #f5e0dc #585b70 #cdd6f4 #313244 #45475a #7f849c #cba6f7" +
				" " + "#45475a #f38ba8 #a6e3a1 #f9e2af #89b4fa #f5c2e7 #94e2d5 #a6adc8" +
				" " + "#585b70 #f37799 #89d88b #ebd391 #74a8fc #f2aede #6bd7ca #bac2de",
		),
		// Catppuccin Latte; UI roles are surface0, surface1, overlay1 and mauve.
		Light: variantOf(
			"#eff1f5 #4c4f69 #dc8a78 #acb0be #4c4f69 #ccd0da #bcc0cc #8c8fa1 #8839ef" +
				" " + "#5c5f77 #d20f39 #40a02b #df8e1d #1e66f5 #ea76cb #179299 #acb0be" +
				" " + "#6c6f85 #de293e #49af3d #eea02d #456eff #fe85d8 #2d9fa8 #bcc0cc",
		),
	},
}

// Palettes lists the embedded palettes in display order.
func Palettes() []Palette { return append([]Palette(nil), embeddedPalettes...) }

// PaletteByName returns the named palette or an error naming every known one.
func PaletteByName(name string) (Palette, error) {
	var known []string
	for index := range embeddedPalettes {
		if embeddedPalettes[index].Name == name {
			return embeddedPalettes[index], nil
		}
		known = append(known, embeddedPalettes[index].Name)
	}
	return Palette{}, fmt.Errorf("themesync: unknown palette %q (known: %s)", name, strings.Join(known, ", "))
}
