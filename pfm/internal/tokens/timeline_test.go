package tokens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTimelineUnits(t *testing.T) {
	half := 0.5
	cases := []struct{ got, want string }{
		{timelineTokens(999), "999"},
		{timelineTokens(1500), "1.5K"},
		{timelineTokens(2.5e6), "2.5M"},
		{timelineCash(nil), "n/a"},
		{timelineCash(&half), "$0.5000"},
		{seconds(1500), "1.5s"},
		{seconds(0), "0.0s"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d = %q, want %q", i, c.got, c.want)
		}
	}
}

func TestTimelinePrintsEachFileAndFailsOnAnUnreadableOne(t *testing.T) {
	dir := t.TempDir()
	priced := writeTranscript(t, filepath.Join(dir, "-w-demo", "s1.jsonl"),
		assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1000, 100, 2000, 400))
	empty := writeTranscript(t, filepath.Join(dir, "-w-demo", "s2.jsonl"),
		`{"type":"user","timestamp":"2026-09-21T05:00:00Z","message":{"role":"user","content":"hi"}}`)
	unreadable := filepath.Join(dir, "-w-demo", "s3.jsonl")
	if err := os.Mkdir(unreadable, 0o755); err != nil {
		t.Fatal(err)
	}
	prices := fixture(t, "model-cost.json")

	code, stdout, stderr := auditWith(t, Options{}, "--prices", prices, "--timeline", priced, "--timeline", empty)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q, want 0", code, stderr)
	}
	for _, part := range []string{"TIMELINE main · sonnet-5", " · 1 calls · ", "$0.0044 · " + priced, "NO CALLS — " + empty} {
		if !strings.Contains(stdout, part) {
			t.Errorf("stdout lacks %q:\n%s", part, stdout)
		}
	}

	code, stdout, _ = auditWith(t, Options{}, "--prices", prices, "--timeline", unreadable, "--timeline", priced)
	if code != 1 || !strings.Contains(stdout, "UNREADABLE — "+unreadable+": ") ||
		!strings.Contains(stdout, "TIMELINE main") {
		t.Errorf("exit %d, want 1 with the unreadable file named and the next file still printed:\n%s", code, stdout)
	}
}

func TestTimelineMarksTheAdvisorPartOfACallsDollars(t *testing.T) {
	file := writeTranscript(t, filepath.Join(t.TempDir(), "-w-demo", "s1.jsonl"),
		advisorAssistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", "claude-opus-5-5"))
	code, stdout, stderr := auditWith(t, Options{}, "--prices", fixture(t, "model-cost.json"), "--timeline", file)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q, want 0", code, stderr)
	}
	total, advisor := "$"+fixed(advisorExecutorUSD+advisorAdvisorUSD, 4), "(advisor $"+fixed(advisorAdvisorUSD, 4)+")"
	for _, part := range []string{total + " " + advisor + " · " + file, " · " + total + " " + advisor + " · (no tools)"} {
		if !strings.Contains(stdout, part) {
			t.Errorf("timeline lacks %q:\n%s", part, stdout)
		}
	}
	// an executor model the table cannot price still shows the advisor dollars its call booked
	file = writeTranscript(t, filepath.Join(t.TempDir(), "-w-demo", "s2.jsonl"),
		advisorAssistant("m1", "claude-nonesuch-9", "2026-09-21T05:00:00Z", "claude-opus-5-5"))
	code, stdout, stderr = auditWith(t, Options{}, "--prices", fixture(t, "model-cost.json"), "--timeline", file)
	if code != 0 || !strings.Contains(stdout, " · "+notPriced+" "+advisor+" · (no tools)") {
		t.Errorf("exit %d stderr %q; want the unpriced call's row to carry %q:\n%s", code, stderr, advisor, stdout)
	}
	// an advisor the table cannot price marks its call "n/a", never "$0"
	file = writeTranscript(t, filepath.Join(t.TempDir(), "-w-demo", "s3.jsonl"),
		advisorAssistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", "claude-nonesuch-9"))
	code, stdout, stderr = auditWith(t, Options{}, "--prices", fixture(t, "model-cost.json"), "--timeline", file)
	unpriced := " · $" + fixed(advisorExecutorUSD, 4) + " (advisor " + notPriced + ") · (no tools)"
	if code != 0 || !strings.Contains(stdout, unpriced) || strings.Contains(stdout, "(advisor $") {
		t.Errorf("exit %d stderr %q; want the row to carry %q:\n%s", code, stderr, unpriced, stdout)
	}
}

// A response that billed only its advisor is no call, but its dollars are the
// run's: the timeline shows the consultation on a row of its own.
func TestTimelineShowsAConsultationWithoutACall(t *testing.T) {
	file := writeTranscript(t, filepath.Join(t.TempDir(), "-w-demo", "s1.jsonl"),
		assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1000, 100, 2000, 400),
		advisorOnlyAssistant("m2", "2026-09-21T05:01:00Z"))
	code, stdout, stderr := auditWith(t, Options{}, "--prices", fixture(t, "model-cost.json"), "--timeline", file)
	row := "  advisor 05:01:00 · $" + fixed(advisorAdvisorUSD, 4) + " · (no tools)"
	if code != 0 || stderr != "" || !strings.Contains(stdout, row) || !strings.Contains(stdout, " · 1 calls · ") {
		t.Errorf("exit %d stderr %q; want one call and the consultation's row %q:\n%s", code, stderr, row, stdout)
	}
}
