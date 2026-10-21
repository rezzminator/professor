package schedule

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
	got := Message(fleetdb.Schedule{ID: 12, Interval: 15 * 24 * time.Hour, Prompt: "check the nightly build"})
	const want = "⏰ pfm schedule 12 (every 15d): check the nightly build\n\n" +
		"[pfm-schedule id=12 fire=0001-01-01T00:00:00Z]"
	if got != want {
		t.Fatalf("Message = %q, want %q", got, want)
	}
}

func TestParseAt(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("test", 2*3600)
	now := time.Date(2026, 10, 11, 14, 30, 0, 0, zone)
	for _, test := range []struct {
		text string
		want time.Time
		err  string
	}{
		{text: "18:00", want: time.Date(2026, 10, 11, 18, 0, 0, 0, zone)},
		{text: "09:15", want: time.Date(2026, 10, 12, 9, 15, 0, 0, zone)},
		{text: "14:30", want: time.Date(2026, 10, 12, 14, 30, 0, 0, zone)},
		{text: "2026-10-20 08:00", want: time.Date(2026, 10, 20, 8, 0, 0, 0, zone)},
		{text: "2026-10-20T08:00", want: time.Date(2026, 10, 20, 8, 0, 0, 0, zone)},
		{text: "2026-10-20T08:00:00Z", want: time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)},
		{text: "2026-10-11 14:30", err: "less than 1m from now"},
		{text: "2026-01-01 00:00", err: "less than 1m from now"},
		{text: "tomorrow", err: "HH:MM"},
		{text: "", err: "HH:MM"},
	} {
		got, err := ParseAt(test.text, now)
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("ParseAt(%q) = %v, %v; want an error naming %q", test.text, got, err, test.err)
			}
			continue
		}
		if err != nil || !got.Equal(test.want) {
			t.Errorf("ParseAt(%q) = %v, %v; want %v", test.text, got, err, test.want)
		}
	}
}

func TestScheduleWhenNamesExactlyOneTiming(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 11, 14, 30, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		when     scheduleWhen
		once     bool
		interval time.Duration
		next     time.Time
		err      string
	}{
		{name: "every", when: scheduleWhen{every: "2h"}, interval: 2 * time.Hour},
		{name: "in", when: scheduleWhen{in: "90m"}, once: true, interval: 90 * time.Minute},
		{
			name: "at", when: scheduleWhen{at: "16:00"}, once: true, interval: 90 * time.Minute,
			next: time.Date(2026, 10, 11, 16, 0, 0, 0, time.UTC),
		},
		{name: "none", err: "exactly one"},
		{name: "two", when: scheduleWhen{every: "2h", in: "1h"}, err: "exactly one"},
		{name: "in too short", when: scheduleWhen{in: "30s"}, err: "--in"},
		{name: "at unreadable", when: scheduleWhen{at: "noon"}, err: "--at"},
	} {
		got, err := test.when.schedule(now)
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("%s: %+v, %v; want an error naming %q", test.name, got, err, test.err)
			}
			continue
		}
		if err != nil || got.Once != test.once || got.Interval != test.interval ||
			!got.NextFire.Equal(test.next) || !got.Created.Equal(now) {
			t.Errorf(
				"%s: %+v, %v; want once=%v interval=%v next=%v",
				test.name,
				got,
				err,
				test.once,
				test.interval,
				test.next,
			)
		}
	}
}

func TestMessageNamesAOneTimeSchedule(t *testing.T) {
	t.Parallel()
	got := Message(fleetdb.Schedule{ID: 4, Once: true, Interval: time.Hour, Prompt: "check it"})
	if want := "⏰ pfm schedule 4 (once): check it\n\n[pfm-schedule id=4 fire=0001-01-01T00:00:00Z]"; got != want {
		t.Fatalf("Message = %q, want %q", got, want)
	}
}
