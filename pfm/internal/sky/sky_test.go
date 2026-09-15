package sky

import (
	"regexp"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

const sec = int64(1_000_000_000)

func TestEveryEngineHasAStar(t *testing.T) {
	for _, id := range pfmengine.All() {
		if _, ok := starBase[id]; !ok {
			t.Errorf("engine %s has no star class", id)
		}
	}
}

// plain renders an uncolored frame at the design size unless overridden.
func plain(claude, codex int, tNS int64, events []Event, size ...int) []string {
	w, h := 18, 3
	if len(size) == 2 {
		w, h = size[0], size[1]
	}
	return Frame(Options{
		ClaudeCount: claude, CodexCount: codex,
		Width: w, Height: h, TimeNS: tNS, Events: events,
	})
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

func inked(lines []string) int {
	n := 0
	for _, l := range lines {
		n += len([]rune(l)) - strings.Count(l, " ")
	}
	return n
}

func TestDimensionsHonored(t *testing.T) {
	for _, sz := range [][2]int{{18, 3}, {24, 4}, {12, 3}} {
		lines := plain(3, 2, 1200*sec/1000, nil, sz[0], sz[1])
		if len(lines) != sz[1] {
			t.Fatalf("%dx%d: got %d lines, want %d", sz[0], sz[1], len(lines), sz[1])
		}
		for i, l := range lines {
			if got := len([]rune(l)); got != sz[0] {
				t.Fatalf("%dx%d: line %d is %d cells, want %d", sz[0], sz[1], i, got, sz[0])
			}
		}
	}
	if Frame(Options{Width: 4, Height: 1}) != nil {
		t.Error("a region too small to render should return nil")
	}
}

func TestOrbitMoves(t *testing.T) {
	times := []int64{0, 2 * sec, 4 * sec}
	frames := make([]string, len(times))
	for i, tm := range times {
		frames[i] = joined(plain(3, 3, tm, nil))
	}
	for i := 0; i < len(frames); i++ {
		for j := i + 1; j < len(frames); j++ {
			if frames[i] == frames[j] {
				t.Errorf("frames at t=%ds and t=%ds are identical — the orbit is not turning",
					times[i]/sec, times[j]/sec)
			}
		}
	}
}

func TestCountsScaleVisualWeight(t *testing.T) {
	const tm = 1200 * 1_000_000
	empty := inked(plain(0, 0, tm, nil))
	small := inked(plain(2, 2, tm, nil))
	big := inked(plain(8, 8, tm, nil))
	if empty >= small || small >= big {
		t.Errorf("inked cells must grow with counts: 0/0=%d, 2/2=%d, 8/8=%d", empty, small, big)
	}
	if got := joined(plain(0, 0, tm, nil)); strings.ContainsAny(got, "✦+*") {
		t.Error("an empty fleet must show no star cores or sparkles")
	}
}

func TestEnginesAreDistinctBodies(t *testing.T) {
	const tm = 1200 * 1_000_000
	color := func(c, x int) string {
		return joined(Frame(Options{
			ClaudeCount: c, CodexCount: x, Width: 18, Height: 3,
			TimeNS: tm, Colorize: true,
		}))
	}
	if color(5, 0) == color(0, 5) {
		t.Error("a Claude-only sky and a Codex-only sky rendered identically even in color")
	}
	if joined(plain(5, 3, tm, nil)) == joined(plain(3, 5, tm, nil)) {
		t.Error("swapping the two counts left the plain layout unchanged — mass is not shaping the orbit")
	}
}

func TestCometLifecycle(t *testing.T) {
	start := 3 * sec
	ev := []Event{{Kind: EventBirth, StartNS: start}}
	mid := start + CometDurNS/2
	if joined(plain(3, 2, mid, ev)) == joined(plain(3, 2, mid, nil)) {
		t.Error("a comet at half-life left no trace on the frame")
	}
	after := start + 2*sec
	if joined(plain(3, 2, after, ev)) != joined(plain(3, 2, after, nil)) {
		t.Error("an expired comet still affects the frame")
	}
	if !(Event{Kind: EventBirth, StartNS: start}).Expired(after) {
		t.Error("Expired must report true 2s after a birth")
	}
}

func TestEmberLifecycle(t *testing.T) {
	start := 5 * sec
	ev := []Event{{Kind: EventDeath, StartNS: start}}
	mid := start + EmberDurNS/2
	if joined(plain(3, 2, mid, ev)) == joined(plain(3, 2, mid, nil)) {
		t.Error("an ember at half-life left no trace on the frame")
	}
	after := start + 2*sec
	if joined(plain(3, 2, after, ev)) != joined(plain(3, 2, after, nil)) {
		t.Error("an expired ember still affects the frame")
	}
}

func TestFutureEventInvisible(t *testing.T) {
	now := 4 * sec
	ev := []Event{{Kind: EventBirth, StartNS: now + sec}}
	if joined(plain(3, 2, now, ev)) != joined(plain(3, 2, now, nil)) {
		t.Error("an event that has not started yet must not affect the frame")
	}
}

func TestColorizeEmitsANSI(t *testing.T) {
	lines := Frame(Options{
		ClaudeCount: 4, CodexCount: 4, Width: 18, Height: 3,
		TimeNS: 1200 * 1_000_000, Colorize: true,
	})
	if !strings.Contains(joined(lines), "\x1b[") {
		t.Error("colorized frame contains no ANSI escape sequences")
	}
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func TestSnapshotShape(t *testing.T) {
	cases := []struct {
		claude, codex int
		want          string
	}{
		{0, 0, "· ·"},
		{2, 0, "·2 ·"},
		{5, 3, "✦5 ✦3"},
		{9, 12, "✹9 ✹12"},
	}
	for _, c := range cases {
		got := stripANSI(Snapshot(c.claude, c.codex))
		if got != c.want {
			t.Errorf("Snapshot(%d,%d) = %q, want %q", c.claude, c.codex, got, c.want)
		}
	}
	if !strings.Contains(Snapshot(3, 3), "\x1b[") {
		t.Error("Snapshot must carry its colors")
	}
}

func TestSnapshotCountsIncludesEveryEngine(t *testing.T) {
	got := stripANSI(SnapshotCounts(map[pfmengine.ID]int{
		pfmengine.Claude:   2,
		pfmengine.Codex:    3,
		pfmengine.Opencode: 4,
	}))
	if got != "·2 ✦3 ✦4" {
		t.Fatalf("SnapshotCounts() = %q, want all three engine counts", got)
	}
}

func TestFrameRendersOpencodeOnlyFleet(t *testing.T) {
	const now = 1200 * 1_000_000
	empty := joined(Frame(Options{Width: 18, Height: 3, TimeNS: now}))
	opencode := joined(Frame(Options{
		Counts: map[pfmengine.ID]int{pfmengine.Opencode: 5},
		Width:  18, Height: 3, TimeNS: now,
	}))
	if opencode == empty || !strings.ContainsAny(opencode, "✦+*") {
		t.Fatalf("OpenCode-only fleet rendered as empty:\n%s", opencode)
	}
}
