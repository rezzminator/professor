package ui

import (
	"strings"
	"testing"
)

func TestTruecolorOKAcceptsOnlyNoColourOrHashRRGGBB(t *testing.T) {
	cases := map[string]bool{
		"": true, "#5f3dc4": true, "#5F3DC4": true,
		"#zzzzzz": false, "x5f3dc4": false, "5f3dc4": false, "#5f3dc": false, "red": false,
	}
	for hex, want := range cases {
		if got := truecolorOK(hex); got != want {
			t.Errorf("truecolorOK(%q) = %v, want %v", hex, got, want)
		}
	}
}

func TestWriteChannelsSpellsEachChannelInDecimal(t *testing.T) {
	for hex, want := range map[string]string{
		"#5f3dc4": "95;61;196", "#000000": "0;0;0", "#ffffff": "255;255;255", "#0a0B0c": "10;11;12",
	} {
		var buf strings.Builder
		writeChannels(&buf, hex)
		if got := buf.String(); got != want {
			t.Errorf("writeChannels(%q) = %q, want %q", hex, got, want)
		}
	}
}

// A line built in place must be byte-identical to the joined renderings it
// replaces, for a styled run, a bare one and one lipgloss has to spell.
func TestRenderWritersAppendExactlyWhatRenderReturns(t *testing.T) {
	tones := []tone{
		{},
		{fg: "#5f3dc4"},
		{bg: "#334155", bold: true},
		{fg: "#ffffff", bg: "#000000", italic: true, dim: true},
		{fg: "red"},
		{fg: "#5f3dc4", bg: "blue"},
	}
	for _, paint := range tones {
		for _, text := range []string{"", "x", "fleet ● 3"} {
			var line, fromBytes strings.Builder
			line.WriteString("·")
			fromBytes.WriteString("·")
			paint.renderTo(&line, text)
			paint.renderBytesTo(&fromBytes, []byte(text))
			want := "·" + paint.render(text)
			if line.String() != want || fromBytes.String() != want {
				t.Errorf(
					"%+v %q: renderTo %q, renderBytesTo %q, want %q",
					paint,
					text,
					line.String(),
					fromBytes.String(),
					want,
				)
			}
		}
	}
}
