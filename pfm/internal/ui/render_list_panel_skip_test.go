package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"hostops/pfm/internal/compose"
)

// TestRenderSkipsListPanelBuildOnLimitsStatsAndCosmos pins the fix in
// render(): before it, `body := model.renderListPanel(...)` ran on EVERY
// frame and was then discarded on Stats/Limits/Cosmos — a full fleet-row
// layout built and thrown away on every frame those tabs draw, including
// every one of the Limits tab's own idle-backoff ticks (statsCadence, see
// model.go). renderListPanel is the ONLY render function that indexes
// model.rows through model.filtered; poisoning filtered with an
// out-of-range index turns "was it built" into an observable panic instead
// of something only a profiler could show.
func TestRenderSkipsListPanelBuildOnLimitsStatsAndCosmos(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.NoSky = true
	model := NewModel(snapshot)
	if len(model.rows) == 0 {
		t.Fatal("fixture has no rows — test cannot poison a real index")
	}
	model.filtered = []int{len(model.rows) + 5}

	for _, tab := range []Tab{TabStats, TabLimits, TabCosmos} {
		model.tab = tab
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("tab %v: render() panicked — renderListPanel was still built: %v", tab, r)
				}
			}()
			_ = model.render()
		}()
	}

	// Sanity check: TabChats DOES build the list panel and must panic on
	// this poisoned state, proving the fixture actually exercises
	// renderListPanel rather than passing vacuously.
	model.tab = TabChats
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal(
					"TabChats render() did not panic on a poisoned filtered index — fixture no longer exercises renderListPanel",
				)
			}
		}()
		_ = model.render()
	}()
}

// TestOpenCodeRowShowsUnmeasuredSizeNotZeroBytes pins the size badge: an
// OpenCode session has no file size of its own, so its Row.Size is always 0
// — never a measurement. formatSize(0) would print "0B", which claims a
// byte count nothing ever measured; the picker must show "—" instead, while
// an ordinary Claude/Codex row with a real zero-byte size still shows "0B".
func TestOpenCodeRowShowsUnmeasuredSizeNotZeroBytes(t *testing.T) {
	rows := []compose.Row{
		{
			Kind: compose.ResumeOpenCode, ID: "oc-1", Name: "OC session",
			Project: "alpha", CWD: "/work/alpha", PromptCount: 1,
			ActivityNS: fixtureNowNS - int64(time.Minute),
		},
		{
			Kind: compose.ResumeClaude, ID: "cc-1", Name: "CC session",
			Project: "alpha", CWD: "/work/alpha", PromptCount: 1, Size: 0,
			ActivityNS: fixtureNowNS - int64(time.Minute),
		},
	}
	snapshot := fixtureSnapshot(160)
	snapshot.Rows = rows
	snapshot.MergeNewChat = false
	model := NewModel(snapshot)
	view := ansi.Strip(model.View().Content)

	if !strings.Contains(view, "OC session") || !strings.Contains(view, "—") {
		t.Fatalf("OpenCode row did not show the unmeasured-size dash:\n%s", view)
	}
	if !strings.Contains(view, "CC session") || !strings.Contains(view, "0B") {
		t.Fatalf("a real zero-byte Claude row lost its honest 0B size:\n%s", view)
	}
}
