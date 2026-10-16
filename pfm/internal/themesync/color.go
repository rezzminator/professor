package themesync

import (
	"fmt"
	"math"
	"strconv"
)

// rgb is one #rrggbb colour, the unit every blend computes in.
type rgb struct{ red, green, blue uint8 }

// parseHexColor reads #rrggbb (either case); anything else is an error.
func parseHexColor(text string) (rgb, error) {
	if len(text) != 7 || text[0] != '#' {
		return rgb{}, fmt.Errorf("themesync: colour %q is not #rrggbb", text)
	}
	value, err := strconv.ParseUint(text[1:], 16, 32)
	if err != nil {
		return rgb{}, fmt.Errorf("themesync: colour %q: %w", text, err)
	}
	return rgb{red: uint8(value >> 16 & 0xff), green: uint8(value >> 8 & 0xff), blue: uint8(value & 0xff)}, nil
}

// String is the lowercase #rrggbb spelling every surface is compared in.
func (c rgb) String() string { return fmt.Sprintf("#%02x%02x%02x", c.red, c.green, c.blue) }

// blendChannel moves one channel weight of the way from one value to another.
func blendChannel(from, to uint8, weight float64) uint8 {
	return uint8(math.Round(float64(from) + (float64(to)-float64(from))*weight))
}

// mixHex returns the colour weight (clamped to 0..1) of the way from `from`
// to `to`. An unparseable input yields "" — a palette is validated before it
// is rendered, so an empty string here is a defect the render tests catch,
// never a guessed colour.
func mixHex(from, to string, weight float64) string {
	start, err := parseHexColor(from)
	if err != nil {
		return ""
	}
	end, err := parseHexColor(to)
	if err != nil {
		return ""
	}
	weight = math.Min(1, math.Max(0, weight))
	return rgb{
		red:   blendChannel(start.red, end.red, weight),
		green: blendChannel(start.green, end.green, weight),
		blue:  blendChannel(start.blue, end.blue, weight),
	}.String()
}
