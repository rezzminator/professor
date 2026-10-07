package reminder

import (
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

func TestParseInterval(t *testing.T) {
	t.Parallel()
	const accepted = "weekly, <N>d, or a Go duration like 90m, minimum 1m"
	cases := []struct {
		name    string
		text    string
		want    time.Duration
		wantErr string
	}{
		{name: "weekly", text: "weekly", want: 7 * 24 * time.Hour},
		{name: "weekly mixed case padded", text: "  Weekly ", want: 7 * 24 * time.Hour},
		{name: "fifteen days", text: "15d", want: 15 * 24 * time.Hour},
		{name: "thirty days upper", text: "30D", want: 30 * 24 * time.Hour},
		{name: "go duration", text: "90m", want: 90 * time.Minute},
		{name: "compound duration", text: "1h30m", want: 90 * time.Minute},
		{name: "exact minimum", text: "1m", want: time.Minute},
		{name: "below minimum", text: "30s", wantErr: "minimum 1m"},
		{name: "zero duration", text: "0s", wantErr: "minimum 1m"},
		{name: "empty", text: "  ", wantErr: accepted},
		{name: "garbage", text: "soon", wantErr: accepted},
		{name: "zero days", text: "0d", wantErr: accepted},
		{name: "negative days", text: "-3d", wantErr: accepted},
		{name: "signed days", text: "+3d", wantErr: accepted},
		{name: "fractional days", text: "1.5d", wantErr: accepted},
		{name: "negative duration", text: "-5m", wantErr: accepted},
		{name: "overflowing days", text: "99999999999d", wantErr: accepted},
		{name: "overflowing duration", text: "99999999999999h", wantErr: accepted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseInterval(tc.text)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseInterval(%q) = %v, %v; want error containing %q", tc.text, got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ParseInterval(%q) = %v, %v; want %v", tc.text, got, err, tc.want)
			}
		})
	}
}

func TestFormatInterval(t *testing.T) {
	t.Parallel()
	cases := map[time.Duration]string{
		7 * 24 * time.Hour:  "7d",
		15 * 24 * time.Hour: "15d",
		24 * time.Hour:      "1d",
		90 * time.Minute:    "1h30m0s",
		time.Minute:         "1m0s",
		36 * time.Hour:      "36h0m0s",
	}
	for duration, want := range cases {
		if got := FormatInterval(duration); got != want {
			t.Errorf("FormatInterval(%v) = %q, want %q", duration, got, want)
		}
	}
}

func TestMessageNamesIdIntervalAndPrompt(t *testing.T) {
	t.Parallel()
	got := Message(fleetdb.Reminder{ID: 12, Interval: 15 * 24 * time.Hour, Prompt: "check the nightly build"})
	const want = "⏰ pfm reminder 12 (every 15d): check the nightly build"
	if got != want {
		t.Fatalf("Message = %q, want %q", got, want)
	}
}
