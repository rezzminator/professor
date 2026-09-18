package nudge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

func TestBandStartsAtTheConfiguredPercentAndStepsFromThere(t *testing.T) {
	for _, test := range []struct{ percent, start, step, band int }{
		{0, 35, 10, 0},
		{34, 35, 10, 0},
		{35, 35, 10, 35},
		{44, 35, 10, 35},
		{45, 35, 10, 45},
		{94, 35, 10, 85},
		{95, 35, 10, 95},
		{100, 35, 10, 95},
		{140, 35, 10, 95},
		{49, 50, 20, 0},
		{50, 50, 20, 50},
		{69, 50, 20, 50},
		{70, 50, 20, 70},
		{90, 50, 20, 90},
		{100, 50, 20, 90},
	} {
		if got := Band(test.percent, test.start, test.step); got != test.band {
			t.Fatalf("Band(%d, start %d, step %d)=%d, want %d", test.percent, test.start, test.step, got, test.band)
		}
	}
}

// One reminder per band per climb: a chat that hovers inside a band hears
// nothing more, a chat that compacted (context fell) is reminded again on the
// way back up, and nothing fires below the first band.
func TestDecideRemindsOncePerBandAndRearmsAfterACompact(t *testing.T) {
	dir := t.TempDir()
	steps := []struct {
		percent int
		band    int
		nudge   bool
	}{
		{20, 0, false},
		{36, 35, true},
		{38, 35, false},
		{47, 45, true},
		{47, 45, false},
		{68, 65, true},
		{30, 0, false},
		{36, 35, true},
	}
	for index, step := range steps {
		band, nudge, err := Decide(dir, "session-a", step.percent, 35, 10)
		if err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
		if band != step.band || nudge != step.nudge {
			t.Fatalf(
				"step %d (%d%%): band=%d nudge=%t, want band=%d nudge=%t",
				index,
				step.percent,
				band,
				nudge,
				step.band,
				step.nudge,
			)
		}
	}
}

func TestDecideKeepsSessionsApart(t *testing.T) {
	dir := t.TempDir()
	if _, nudge, err := Decide(dir, "session-a", 50, 35, 10); err != nil || !nudge {
		t.Fatalf("session-a first crossing: nudge=%t err=%v", nudge, err)
	}
	if _, nudge, err := Decide(dir, "session-b", 50, 35, 10); err != nil || !nudge {
		t.Fatalf("session-b must not inherit session-a's band: nudge=%t err=%v", nudge, err)
	}
}

// TestDecideRecordsATransition: Decide walks the state door (spec §
// Middleware, `state`) — comp=state, kind=nudge, watching to held or nudged —
// never the percentage-derived reminder text itself.
func TestDecideRecordsATransition(t *testing.T) {
	_, recorder := obs.Test(t)
	dir := t.TempDir()
	if _, nudge, err := Decide(dir, "session-a", 50, 35, 10); err != nil || !nudge {
		t.Fatalf("Decide() nudge=%t err=%v", nudge, err)
	}
	var next string
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "nudge" {
			continue
		}
		value, _ := record.Field("next")
		next, _ = value.(string)
	}
	if next != "nudged" {
		t.Fatalf("Decide() next state = %q, want nudged: %s", next, recorder.Raw())
	}
}

func TestContextSampleRoundTripsAndNamesAbsence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sid")
	if _, found, err := ReadContext(dir, "session-a"); err != nil || found {
		t.Fatalf("missing sample: found=%t err=%v, want found=false with no error", found, err)
	}
	if err := RecordContext(dir, "session-a", 47); err != nil {
		t.Fatal(err)
	}
	percent, found, err := ReadContext(dir, "session-a")
	if err != nil || !found || percent != 47 {
		t.Fatalf("sample = %d found=%t err=%v, want 47", percent, found, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nudge-ctx-session-a"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadContext(dir, "session-a"); err == nil {
		t.Fatal("a corrupt sample must be an error, never a silent zero")
	}
}

func TestTextIsAReminderNotAnOrderAndNamesNoOtherCommand(t *testing.T) {
	text := Text(47, 45, 10)
	for _, want := range []string{"47%", "chat_self_compact", "ONE steer", "Not an order", "next reminder at 55%"} {
		if !strings.Contains(text, want) {
			t.Fatalf("nudge text lacks %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"/handoff", "/reload", "must compact"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("nudge text must not carry %q:\n%s", forbidden, text)
		}
	}
	last := Text(96, 95, 10)
	if strings.Contains(last, "next reminder") || !strings.Contains(last, "last reminder") {
		t.Fatalf("top band must say it is the last reminder:\n%s", last)
	}
}
