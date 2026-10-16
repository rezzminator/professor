package themesync

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// rgb is one colour, the unit every derivation computes in.
type rgb struct{ red, green, blue uint8 }

// String is the lowercase #rrggbb spelling every surface is compared in.
func (c rgb) String() string { return fmt.Sprintf("#%02x%02x%02x", c.red, c.green, c.blue) }

// xterm16 is Claude Code's `ansi:name` palette (the xterm defaults, D15).
var xterm16 = []struct{ name, hex string }{
	{"black", "#000000"},
	{"red", "#cd0000"},
	{"green", "#00cd00"},
	{"yellow", "#cdcd00"},
	{"blue", "#0000ee"},
	{"magenta", "#cd00cd"},
	{"cyan", "#00cdcd"},
	{"white", "#e5e5e5"},
	{"blackBright", "#7f7f7f"},
	{"redBright", "#ff0000"},
	{"greenBright", "#00ff00"},
	{"yellowBright", "#ffff00"},
	{"blueBright", "#5c5cff"},
	{"magentaBright", "#ff00ff"},
	{"cyanBright", "#00ffff"},
	{"whiteBright", "#ffffff"},
}

var (
	rgbCall    = regexp.MustCompile(`^rgb\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*\)$`)
	ansi256Num = regexp.MustCompile(`^ansi256\(\s*(\d{1,3})\s*\)$`)
)

// parseColor reads every colour spelling Claude Code's theme files use:
// rgb(r, g, b), #rgb, #rrggbb, ansi256(n) and ansi:name. Anything else is an
// error naming the text, never a guessed colour.
func parseColor(text string) (rgb, error) {
	text = strings.TrimSpace(text)
	if match := rgbCall.FindStringSubmatch(text); match != nil {
		var channels [3]uint8
		for index := range channels {
			value, _ := strconv.Atoi(match[index+1])
			if value > 255 {
				return rgb{}, fmt.Errorf("themesync: colour %q has a channel above 255", text)
			}
			channels[index] = uint8(value)
		}
		return rgb{channels[0], channels[1], channels[2]}, nil
	}
	if match := ansi256Num.FindStringSubmatch(text); match != nil {
		value, _ := strconv.Atoi(match[1])
		if value > 255 {
			return rgb{}, fmt.Errorf("themesync: colour %q is above ansi256(255)", text)
		}
		return ansi256Color(value), nil
	}
	if name, ok := strings.CutPrefix(text, "ansi:"); ok {
		for _, entry := range xterm16 {
			if entry.name == name {
				return parseHex(entry.hex)
			}
		}
		return rgb{}, fmt.Errorf("themesync: colour %q names no ansi colour", text)
	}
	if strings.HasPrefix(text, "#") {
		return parseHex(text)
	}
	return rgb{}, fmt.Errorf("themesync: colour %q is not rgb(), #rgb, #rrggbb, ansi256() or ansi:name", text)
}

// parseHex reads #rgb or #rrggbb (either case).
func parseHex(text string) (rgb, error) {
	digits := strings.TrimPrefix(text, "#")
	if len(digits) == 3 {
		digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
	}
	if len(digits) != 6 {
		return rgb{}, fmt.Errorf("themesync: colour %q is not #rgb or #rrggbb", text)
	}
	value, err := strconv.ParseUint(digits, 16, 32)
	if err != nil {
		return rgb{}, fmt.Errorf("themesync: colour %q: %w", text, err)
	}
	return rgb{red: uint8(value >> 16 & 0xff), green: uint8(value >> 8 & 0xff), blue: uint8(value & 0xff)}, nil
}

// ansi256Color is xterm's 256-colour table: the 16 named colours, a 6x6x6
// cube, then 24 greys.
func ansi256Color(index int) rgb {
	switch {
	case index < 16:
		colour, _ := parseHex(xterm16[index].hex)
		return colour
	case index < 232:
		levels := [6]uint8{0, 95, 135, 175, 215, 255}
		index -= 16
		return rgb{levels[index/36], levels[index/6%6], levels[index%6]}
	}
	grey := uint8(8 + 10*(index-232))
	return rgb{grey, grey, grey}
}
