package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

// phaseOff is a time 100 ms into an odd half-second: the blink's off-phase.
var phaseOff = time.UnixMilli(1_000_000_600)

func TestEveryStatusMapsToItsOwnIndicatorInBothPhases(t *testing.T) {
	cases := []struct {
		status string
		on     map[string]string
		off    map[string]string
	}{
		{"idle", nil, nil},
		{
			"working",
			map[string]string{IndicatorWorking: GlyphWorking},
			map[string]string{IndicatorWorking: GlyphWorking},
		},
		{"blocked", map[string]string{IndicatorBlocked: GlyphBlocked}, nil},
		{"done", map[string]string{IndicatorDone: GlyphDone}, map[string]string{IndicatorDone: GlyphDone}},
		{
			"unknown",
			map[string]string{IndicatorUnknown: GlyphUnknown},
			map[string]string{IndicatorUnknown: GlyphUnknown},
		},
	}
	for _, tc := range cases {
		for _, phase := range []struct {
			name string
			at   time.Time
			want map[string]string
		}{{"on", phaseOn, tc.on}, {"off", phaseOff, tc.off}} {
			t.Run(tc.status+"/"+phase.name, func(t *testing.T) {
				// Every indicator token is present to start with, and another
				// source's token sits beside them: only the four may change.
				stale := func() map[string]string {
					return map[string]string{
						IndicatorWorking: GlyphWorking, IndicatorBlocked: GlyphBlocked,
						IndicatorDone: GlyphDone, IndicatorUnknown: GlyphUnknown, "jj_status": "2 changes",
					}
				}
				herdr := &fakeIndicatorHerdr{
					panes:  []*fakeTarget{{ID: "w1:p1", Workspace: "w1", Status: tc.status, Tokens: stale()}},
					spaces: []*fakeTarget{{ID: "w1", Status: tc.status, Tokens: stale()}},
				}
				painter, _, stderr := newTestPainter(t, herdr, phase.at)
				blocked, _, err := painter.paint(context.Background())
				if err != nil {
					t.Fatalf("paint = %v; stderr %q", err, stderr)
				}
				if blocked != (tc.status == "blocked") {
					t.Fatalf("paint reported blocked = %v for status %q", blocked, tc.status)
				}
				want := maps.Clone(phase.want)
				if want == nil {
					want = map[string]string{}
				}
				want["jj_status"] = "2 changes"
				for kind, target := range map[string]*fakeTarget{"pane": herdr.panes[0], "workspace": herdr.spaces[0]} {
					if !maps.Equal(target.Tokens, want) {
						t.Errorf("%s tokens = %v, want %v", kind, target.Tokens, want)
					}
				}
			})
		}
	}
}

func TestAnUnrecognisedStatusPaintsUnknownAndIsNamedOnStderrOnce(t *testing.T) {
	herdr := &fakeIndicatorHerdr{
		panes: []*fakeTarget{
			{ID: "w1:p1", Workspace: "w1", Status: "sleeping"},
			{ID: "w1:p2", Workspace: "w1", Status: ""},
		},
		spaces: []*fakeTarget{{ID: "w1", Status: "sleeping"}},
	}
	painter, _, stderr := newTestPainter(t, herdr, phaseOn)
	for range 2 {
		if _, _, err := painter.paint(context.Background()); err != nil {
			t.Fatalf("paint = %v", err)
		}
	}
	for _, target := range append(slices.Clone(herdr.panes), herdr.spaces...) {
		if want := map[string]string{IndicatorUnknown: GlyphUnknown}; !maps.Equal(target.Tokens, want) {
			t.Errorf("%s tokens = %v, want the unknown token %v", target.ID, target.Tokens, want)
		}
	}
	if got := strings.Count(stderr.String(), `"sleeping"`); got != 1 {
		t.Fatalf("stderr names the unrecognised status %d times, want once across two paints: %q", got, stderr)
	}
	if !strings.Contains(stderr.String(), `""`) {
		t.Fatalf("stderr %q does not name the empty status", stderr)
	}
}

func TestPaintWritesOnlyWhatChanged(t *testing.T) {
	herdr := &fakeIndicatorHerdr{
		panes: []*fakeTarget{
			{ID: "w1:p1", Workspace: "w1", Status: "working"},
			{ID: "w1:p2", Workspace: "w1", Status: "done", Tokens: map[string]string{IndicatorDone: GlyphDone}},
			{
				ID:        "w1:p3",
				Workspace: "w1",
				Status:    "idle",
				Tokens:    map[string]string{IndicatorWorking: "x", "other": "keep"},
			},
		},
		spaces: []*fakeTarget{{ID: "w1", Status: "working", Tokens: map[string]string{IndicatorWorking: GlyphWorking}}},
	}
	painter, _, _ := newTestPainter(t, herdr, phaseOn)
	if _, _, err := painter.paint(context.Background()); err != nil {
		t.Fatalf("paint = %v", err)
	}
	got := map[string]string{}
	for _, write := range herdr.writes {
		got[write.Target] = write.Tokens
	}
	want := map[string]string{"w1:p1": `{"pfm_working":"●"}`, "w1:p3": `{"pfm_working":null}`}
	if !maps.Equal(got, want) || len(herdr.writes) != 2 {
		t.Fatalf("writes = %+v, want one write for p1 and p3 only: %v", herdr.writes, want)
	}
	herdr.writes = nil
	if _, _, err := painter.paint(context.Background()); err != nil || len(herdr.writes) != 0 {
		t.Fatalf("second paint = %v with writes %+v, want none once everything matches", err, herdr.writes)
	}
}

func TestAGlyphThatDiffersFromTheTokensIsRewritten(t *testing.T) {
	herdr := &fakeIndicatorHerdr{
		panes: []*fakeTarget{
			{ID: "w1:p1", Workspace: "w1", Status: "done", Tokens: map[string]string{IndicatorDone: "x"}},
		},
	}
	painter, _, _ := newTestPainter(t, herdr, phaseOn)
	if _, _, err := painter.paint(context.Background()); err != nil {
		t.Fatalf("paint = %v", err)
	}
	if got := herdr.paneWrites("w1:p1"); !slices.Equal(got, []string{`{"pfm_done":"✓"}`}) {
		t.Fatalf("writes = %q, want the glyph restored", got)
	}
}

func TestAnEmptyHerdrIsNotPaintedAndIsNotBlocked(t *testing.T) {
	herdr := &fakeIndicatorHerdr{}
	painter, _, _ := newTestPainter(t, herdr, phaseOn)
	blocked, _, err := painter.paint(context.Background())
	if blocked || err != nil || len(herdr.writes) != 0 {
		t.Fatalf("paint = blocked %v, %v, writes %+v, want none of them", blocked, err, herdr.writes)
	}
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run on an empty Herdr = %v, want nil", err)
	}
}

func TestARefusedTargetIsLoggedAndThePaintGoesOn(t *testing.T) {
	for _, kind := range []string{targetPane, targetWorkspace} {
		t.Run(kind, func(t *testing.T) {
			id := "w1:p1"
			if kind == targetWorkspace {
				id = "w1"
			}
			herdr := &fakeIndicatorHerdr{
				panes: []*fakeTarget{
					{ID: "w1:p1", Workspace: "w1", Status: "working"},
					{ID: "w1:p2", Workspace: "w1", Status: "working"},
				},
				spaces: []*fakeTarget{{ID: "w1", Status: "working"}, {ID: "w2", Status: "working"}},
				failTo: map[string]error{
					id: &Refusal{Request: kind + ".report_metadata", Code: kind + "_not_found", Message: "gone"},
				},
			}
			painter, _, stderr := newTestPainter(t, herdr, phaseOn)
			if _, _, err := painter.paint(context.Background()); err != nil {
				t.Fatalf("paint = %v, want a missing target to be logged only", err)
			}
			if !strings.Contains(stderr.String(), id) || !strings.Contains(stderr.String(), kind+"_not_found") {
				t.Fatalf("stderr = %q, want the missing target and Herdr's code", stderr)
			}
			if len(herdr.panes[1].Tokens) != 1 || len(herdr.spaces[1].Tokens) != 1 {
				t.Fatalf("p2 %v and w2 %v must still be painted", herdr.panes[1].Tokens, herdr.spaces[1].Tokens)
			}
		})
	}
}

func TestMetadataRefusalsFailPaint(t *testing.T) {
	for _, kind := range []string{targetPane, targetWorkspace} {
		codes := []string{
			"metadata_token_limit", "metadata_sequence_source_limit", "invalid_metadata_source",
			"invalid_metadata_token", "invalid_metadata_ttl", "invalid_metadata_request",
		}
		otherNotFound := "pane_not_found"
		if kind == targetPane {
			otherNotFound = "workspace_not_found"
		}
		for _, code := range append(codes, otherNotFound) {
			t.Run(kind+"/"+code, func(t *testing.T) {
				id := "w1:p1"
				if kind == targetWorkspace {
					id = "w1"
				}
				herdr := &fakeIndicatorHerdr{
					panes:  []*fakeTarget{{ID: "w1:p1", Workspace: "w1", Status: "working"}},
					spaces: []*fakeTarget{{ID: "w1", Status: "working"}},
					failTo: map[string]error{
						id: &Refusal{Request: kind + ".report_metadata", Code: code, Message: "refused"},
					},
				}
				painter, _, _ := newTestPainter(t, herdr, phaseOn)
				_, _, err := painter.paint(context.Background())
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != code || !strings.Contains(err.Error(), kind+" "+id) {
					t.Fatalf("paint = %v, want %s refusal with %s %s context", err, code, kind, id)
				}
			})
		}
	}
}

func TestAWriteThatCannotReachHerdrFailsThePaintWithItsContext(t *testing.T) {
	herdr := &fakeIndicatorHerdr{
		panes:  []*fakeTarget{{ID: "w1:p1", Workspace: "w1", Status: "working"}},
		failTo: map[string]error{"w1:p1": errors.New("write: broken pipe")},
	}
	painter, _, _ := newTestPainter(t, herdr, phaseOn)
	_, _, err := painter.paint(context.Background())
	if err == nil || !strings.Contains(err.Error(), "w1:p1") || !strings.Contains(err.Error(), "broken pipe") {
		t.Fatalf("paint = %v, want the pane and the cause", err)
	}
}

func TestMalformedListsAreErrors(t *testing.T) {
	for name, tc := range map[string]struct{ panes, spaces, want string }{
		"pane type":       {`{"type":"nope","panes":[]}`, `{"type":"workspace_list","workspaces":[]}`, "pane_list"},
		"workspace type":  {`{"type":"pane_list","panes":[]}`, `{"type":"x","workspaces":[]}`, "workspace_list"},
		"pane not json":   {`[1`, `{"type":"workspace_list","workspaces":[]}`, "pane.list"},
		"pane without id": {`{"type":"pane_list","panes":[{"agent_status":"idle"}]}`, `{"type":"workspace_list","workspaces":[]}`, "pane_id"},
		"space without id": {
			`{"type":"pane_list","panes":[]}`, `{"type":"workspace_list","workspaces":[{"agent_status":"idle"}]}`, "workspace_id",
		},
	} {
		t.Run(name, func(t *testing.T) {
			targets, err := decodeIndicatorTargets(json.RawMessage(tc.panes), json.RawMessage(tc.spaces))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decode = %v, %v, want an error naming %q", targets, err, tc.want)
			}
		})
	}
}

func TestDecodeReadsPanesThenWorkspacesWithAbsentTokensEmpty(t *testing.T) {
	targets, err := decodeIndicatorTargets(
		json.RawMessage(
			`{"type":"pane_list","panes":[{"pane_id":"w1:p1","workspace_id":"w1","agent_status":"blocked","tokens":{"pfm_blocked":"●"},"agent":"claude"}]}`,
		),
		json.RawMessage(
			`{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"x","agent_status":"done"}]}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []indicatorTarget{
		{kind: "pane", id: "w1:p1", status: "blocked", tokens: map[string]string{IndicatorBlocked: GlyphBlocked}},
		{kind: "workspace", id: "w1", status: "done"},
	}
	if len(targets) != 2 || targets[0].kind != "pane" || targets[0].id != "w1:p1" || targets[0].status != "blocked" ||
		!maps.Equal(targets[0].tokens, want[0].tokens) || targets[1].kind != "workspace" || targets[1].id != "w1" ||
		targets[1].status != "done" || len(targets[1].tokens) != 0 {
		t.Fatalf("targets = %+v, want %+v", targets, want)
	}
}

func TestTokenReportLinesAreExactlyHerdrsRequests(t *testing.T) {
	set, unset := "●", (*string)(nil)
	for _, tc := range []struct {
		report tokenReport
		want   string
	}{
		{
			tokenReport{indicatorTarget{kind: "pane", id: "w1:p1"}, map[string]*string{IndicatorWorking: &set, IndicatorBlocked: unset}, 7},
			`{"id":"pfm-7","method":"pane.report_metadata","params":{"pane_id":"w1:p1","source":"pfm-indicators","tokens":{"pfm_blocked":null,"pfm_working":"●"}}}`,
		},
		{
			tokenReport{indicatorTarget{kind: "workspace", id: "w2"}, map[string]*string{IndicatorDone: unset}, 8},
			`{"id":"pfm-8","method":"workspace.report_metadata","params":{"workspace_id":"w2","source":"pfm-indicators","tokens":{"pfm_done":null}}}`,
		},
	} {
		line, err := tc.report.Line()
		if err != nil || string(line) != tc.want {
			t.Errorf("Line = %s, %v\nwant   %s", line, err, tc.want)
		}
	}
	for _, report := range []tokenReport{
		{indicatorTarget{kind: "pane"}, map[string]*string{IndicatorDone: &set}, 1},
		{indicatorTarget{kind: "pane", id: "w1:p1"}, nil, 1},
		{indicatorTarget{kind: "tab", id: "t1"}, map[string]*string{IndicatorDone: &set}, 1},
	} {
		if line, err := report.Line(); err == nil {
			t.Errorf("Line(%+v) = %s, want an error for a missing id, no tokens or an unknown kind", report, line)
		}
	}
}
