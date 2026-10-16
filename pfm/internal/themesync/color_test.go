package themesync

import "testing"

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "#1a1b26", want: "#1a1b26"},
		{in: "#C0CAF5", want: "#c0caf5"},
		{in: "#12", wantErr: true},
		{in: "red", wantErr: true},
		{in: "#gggggg", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseHexColor(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseHexColor(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if err == nil && got.String() != tc.want {
				t.Fatalf("parseHexColor(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestBlendHex(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		weight   float64
		want     string
	}{
		{name: "weight zero keeps from", from: "#102030", to: "#ffffff", weight: 0, want: "#102030"},
		{name: "weight one is to", from: "#102030", to: "#ffffff", weight: 1, want: "#ffffff"},
		{name: "midpoint rounds half up", from: "#000000", to: "#ffffff", weight: 0.5, want: "#808080"},
		{name: "tokyo night diff green", from: "#1a1b26", to: "#9ece6a", weight: 0.22, want: "#374235"},
		{name: "weight above one clamps", from: "#000000", to: "#ff0000", weight: 2, want: "#ff0000"},
		{name: "weight below zero clamps", from: "#00ff00", to: "#ff0000", weight: -1, want: "#00ff00"},
		{name: "invalid input is empty never a guess", from: "nope", to: "#ffffff", weight: 0.5, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mixHex(tc.from, tc.to, tc.weight); got != tc.want {
				t.Fatalf("mixHex(%s,%s,%v) = %q, want %q", tc.from, tc.to, tc.weight, got, tc.want)
			}
		})
	}
}
