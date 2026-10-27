package themesync

import "testing"

func TestParseColorReadsEverySpellingClaudeThemesUse(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "rgb(55, 55, 55)", want: "#373737"},
		{in: "rgb(215,119,87)", want: "#d77757"},
		{in: "#abc", want: "#aabbcc"},
		{in: "#C0CAF5", want: "#c0caf5"},
		{in: "ansi:redBright", want: "#ff0000"},
		{in: "ansi:black", want: "#000000"},
		{in: "ansi256(196)", want: "#ff0000"},
		{in: "ansi256(232)", want: "#080808"},
		{in: "ansi256(9)", want: "#ff0000"},
		{in: "rgb(256,0,0)", wantErr: true},
		{in: "ansi256(256)", wantErr: true},
		{in: "ansi:chartreuse", wantErr: true},
		{in: "#12", wantErr: true},
		{in: "#gggggg", wantErr: true},
		{in: "red", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseColor(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseColor(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if err == nil && got.String() != tc.want {
				t.Fatalf("parseColor(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}
