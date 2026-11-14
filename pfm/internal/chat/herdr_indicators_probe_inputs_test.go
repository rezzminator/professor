package chat

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/headless"
)

// TestTabInputsDiffNamesWhatAReadReinspects pins the probe's diff: a pane's
// state or a chat's engine or transcript moving lets the next read reuse the
// fleet scan, re-inspecting just those; any other input moving, or no
// baseline, needs the fleet read again.
func TestTabInputsDiffNamesWhatAReadReinspects(t *testing.T) {
	render := func(paneState, paneRows, engine, transcript, mode string, extraPane bool) string {
		var out strings.Builder
		writeTabInput(&out, "mode", "", "", mode)
		writeTabInput(&out, "pane", `"w1:p1"`, paneState, paneRows)
		if extraPane {
			writeTabInput(&out, "pane", `"w1:p2"`, `1 "idle" false`, `"term2" "w1:t2" null`)
		}
		writeTabInput(&out, "engine", `"seat1" "s1"`, engine, "")
		writeTabInput(&out, "transcript", `"seat1" "s1"`, transcript, "")
		return out.String()
	}
	const state, rows, mode = `1 "idle" true`, `"term1" "w1:t1" null`, `"emoji" glyphs ""`
	before := render(state, rows, "true", "10 1", mode, false)
	dirty := func(partial bool, panes, chats []string) tabDirty {
		want := tabDirty{partial: partial, panes: map[string]bool{}, chats: map[string]bool{}}
		for _, pane := range panes {
			want.panes[pane] = true
		}
		for _, chat := range chats {
			want.chats[chat] = true
		}
		return want
	}
	for _, tc := range []struct {
		name          string
		before, after string
		want          tabDirty
	}{
		{"nothing moved", before, before, dirty(true, nil, nil)},
		{
			"the pane's state moved", before, render(`2 "working" true`, rows, "true", "10 1", mode, false),
			dirty(true, []string{`"w1:p1"`}, nil),
		},
		{
			"the pane's terminal moved", before, render(state, `"term9" "w1:t1" null`, "true", "10 1", mode, false),
			dirty(false, nil, nil),
		},
		{
			"the engine left the foreground", before, render(state, rows, "false", "10 1", mode, false),
			dirty(true, nil, []string{`"seat1" "s1"`}),
		},
		{
			"the transcript grew", before, render(state, rows, "true", "20 2", mode, false),
			dirty(true, nil, []string{`"seat1" "s1"`}),
		},
		{
			"the mode changed", before, render(state, rows, "true", "10 1", `"text" glyphs ""`, false),
			dirty(false, nil, nil),
		},
		{"a pane was added", before, render(state, rows, "true", "10 1", mode, true), dirty(false, nil, nil)},
		{"no baseline", "", before, dirty(false, nil, []string{`"seat1" "s1"`})},
	} {
		if got := tabInputsDiff(tc.before, tc.after); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: tabInputsDiff = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// TestTabProbeForgetsTheEngineReadsOfClosedPanes pins that the foreground
// memo keeps only the engines the last enumeration probes.
func TestTabProbeForgetsTheEngineReadsOfClosedPanes(t *testing.T) {
	probe := &tabProbe{
		engines: []headless.Chat{{Pane: "w1:p1", Engine: "claude"}},
		engineReads: map[string]tabEngineRead{
			"w1:p1\x00claude": {alive: true}, "w1:p9\x00claude": {alive: true},
		},
	}
	probe.forgetEngineReads()
	if want := map[string]tabEngineRead{"w1:p1\x00claude": {alive: true}}; !reflect.DeepEqual(probe.engineReads, want) {
		t.Fatalf("engineReads = %v, want %v", probe.engineReads, want)
	}
}
