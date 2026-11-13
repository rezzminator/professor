package statusline

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// fixtureAgent is one sub-agent's files as Claude Code leaves them: its meta
// file ("" = none written), its transcript, how long ago each was last written,
// and the lines its own transcript carries for the agents it spawned.
type fixtureAgent struct {
	meta      string
	metaAge   time.Duration
	lines     []string
	quietFor  time.Duration
	noJournal bool
}

// agentDir writes the session transcript and every agent's files under a
// fresh session, the mtimes set back from now, and returns the session
// transcript path.
func agentDir(t *testing.T, now time.Time, session []string, agents map[string]fixtureAgent) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(session, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for id, agent := range agents {
		journal := filepath.Join(dir, "agent-"+id+".jsonl")
		if !agent.noJournal {
			if err := os.WriteFile(journal, []byte(strings.Join(agent.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(journal, now.Add(-agent.quietFor), now.Add(-agent.quietFor)); err != nil {
				t.Fatal(err)
			}
		}
		if agent.meta == "" {
			continue
		}
		meta := filepath.Join(dir, "agent-"+id+".meta.json")
		if err := os.WriteFile(meta, []byte(agent.meta), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(meta, now.Add(-agent.metaAge), now.Add(-agent.metaAge)); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// endNotice is a task notification as Claude Code queues it into the spawner's
// transcript when an agent ends.
func endNotice(id, status string) string {
	return `{"type":"queue-operation","operation":"enqueue","content":"<task-notification>\n<task-id>` + id +
		`</task-id>\n<tool-use-id>toolu_1</tool-use-id>\n<status>` + status + `</status>\n</task-notification>"}`
}

// endNoticeAt is endNotice on a line stamped at, as Claude Code stamps its
// queue operations.
func endNoticeAt(id, status string, at time.Time) string {
	return strings.Replace(endNotice(id, status), `"operation"`,
		`"timestamp":"`+at.UTC().Format(time.RFC3339Nano)+`","operation"`, 1)
}

const (
	backgroundMeta = `{"agentType":"flights:lander","description":"land it","requestShape":"background"}`
	finishedMeta   = `{"agentType":"flights:lander","description":"land it","finished":true}`
	stoppedMeta    = `{"agentType":"flights:lander","description":"land it","stoppedByUser":true}`
	childMeta      = `{"agentType":"general-purpose","description":"child","parentAgentId":"p"}`
)

func TestRunningAgents(t *testing.T) {
	now := subagentNow
	cases := []struct {
		name    string
		session []string
		agents  map[string]fixtureAgent
		want    []string
	}{
		{
			name:   "a background agent with no end runs",
			agents: map[string]fixtureAgent{"a": {meta: backgroundMeta}},
			want:   []string{"a"},
		},
		{
			name:   "a finished mark ends it",
			agents: map[string]fixtureAgent{"a": {meta: finishedMeta}, "b": {meta: backgroundMeta}},
			want:   []string{"b"},
		},
		{
			name:   "a user stop ends it",
			agents: map[string]fixtureAgent{"a": {meta: stoppedMeta}, "b": {meta: backgroundMeta}},
			want:   []string{"b"},
		},
		{
			name:    "a killed notice in the session's transcript ends it",
			session: []string{endNotice("a", "killed")},
			agents:  map[string]fixtureAgent{"a": {meta: backgroundMeta}, "b": {meta: backgroundMeta}},
			want:    []string{"b"},
		},
		{
			name:    "completed and failed notices end their agents only",
			session: []string{endNotice("a", "completed") + "\n" + endNotice("b", "failed")},
			agents: map[string]fixtureAgent{
				"a": {meta: backgroundMeta}, "b": {meta: backgroundMeta}, "c": {meta: backgroundMeta},
			},
			want: []string{"c"},
		},
		{
			name: "a nested agent's end is read in its parent's transcript, not the session's",
			agents: map[string]fixtureAgent{
				"p": {meta: backgroundMeta, metaAge: time.Minute, lines: []string{endNotice("c", "completed")}},
				"c": {meta: childMeta},
				"d": {meta: `{"agentType":"general-purpose","parentAgentId":"p"}`},
			},
			want: []string{"p", "d"},
		},
		{
			name: "quiet up to the bound still runs, a second past it no longer",
			agents: map[string]fixtureAgent{
				"edge": {meta: backgroundMeta, quietFor: agentQuietBound},
				"gone": {meta: backgroundMeta, quietFor: agentQuietBound + time.Second},
			},
			want: []string{"edge"},
		},
		{
			name: "a finished agent whose transcript grew past the slack was resumed and runs",
			agents: map[string]fixtureAgent{
				"edge":    {meta: finishedMeta, metaAge: agentResumeSlack},
				"resumed": {meta: finishedMeta, metaAge: agentResumeSlack + time.Second},
			},
			want: []string{"resumed"},
		},
		{
			name: "an agent ended by a stamped notice runs again once its transcript grew past the slack",
			session: []string{
				endNoticeAt("edge", "completed", now.Add(-agentResumeSlack)),
				endNoticeAt("resumed", "completed", now.Add(-agentResumeSlack-time.Second)),
				endNotice("unstamped", "killed"),
			},
			agents: map[string]fixtureAgent{
				"edge": {meta: backgroundMeta}, "resumed": {meta: backgroundMeta}, "unstamped": {meta: backgroundMeta},
			},
			want: []string{"resumed"},
		},
		{
			name: "a later notice of a resumed agent ends it again",
			session: []string{
				endNoticeAt("a", "completed", now.Add(-time.Hour)),
				endNoticeAt("a", "completed", now.Add(-time.Second)),
				endNotice("a", "completed"),
			},
			agents: map[string]fixtureAgent{"a": {meta: backgroundMeta}, "b": {meta: backgroundMeta}},
			want:   []string{"b"},
		},
		{
			name:   "an agent whose meta file is not written yet runs",
			agents: map[string]fixtureAgent{"a": {}},
			want:   []string{"a"},
		},
		{
			name:   "a meta file without its transcript is no agent",
			agents: map[string]fixtureAgent{"a": {meta: backgroundMeta, noJournal: true}},
			want:   nil,
		},
		{
			name: "oldest first by meta birth",
			agents: map[string]fixtureAgent{
				"young": {meta: backgroundMeta, metaAge: time.Minute},
				"old":   {meta: backgroundMeta, metaAge: time.Hour},
				"mid":   {meta: backgroundMeta, metaAge: 10 * time.Minute},
			},
			want: []string{"old", "mid", "young"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := agentDir(t, now, tc.session, tc.agents)
			var warn bytes.Buffer
			tasks, err := runningAgents(session, now, newTranscriptScans("", &warn))
			if err != nil {
				t.Fatalf("runningAgents(%s): %v", session, err)
			}
			var got []string
			for _, task := range tasks {
				got = append(got, task.ID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("running = %q, want %q (session %s, warn %q)", got, tc.want, session, warn.String())
			}
		})
	}
}

// End notices the render's budget left unread end nothing: their agents count
// as running, and the warning names the transcript left unread.
func TestRunningAgentsPastTheScanBudget(t *testing.T) {
	session := agentDir(t, subagentNow, []string{endNotice("a", "killed")},
		map[string]fixtureAgent{"a": {meta: backgroundMeta}})
	var warn bytes.Buffer
	spent := transcriptScans{deadline: time.Now().Add(-time.Minute), warn: &warn}
	tasks, err := runningAgents(session, subagentNow, spent)
	if err != nil || len(tasks) != 1 || tasks[0].ID != "a" {
		t.Fatalf("runningAgents(%s) = %+v, %v; want agent a running", session, tasks, err)
	}
	want := agentRowsWho + ": end notifications of " + session + " not read to its end within the budget " +
		"(an agent ended past that point counts as running)\n"
	if warn.String() != want {
		t.Fatalf("warn = %q, want %q", warn.String(), want)
	}
}

func TestRunningAgentsReadsItsMetaFile(t *testing.T) {
	session := agentDir(t, subagentNow, nil, map[string]fixtureAgent{"a": {
		meta: `{"agentType":"flights:lander","name":"lander","description":"land it",` +
			`"model":"sonnet","pickedEffortModel":"claude-opus-5-5","effort":"high"}`,
		metaAge: 90 * time.Second,
	}})
	tasks, err := runningAgents(session, subagentNow, newTranscriptScans("", &bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	want := []subagentTask{{
		ID: "a", Name: "lander", Label: "land it", Model: "sonnet", Effort: "high",
		StartTime: subagentNow.Add(-90 * time.Second).UnixMilli(),
	}}
	if !reflect.DeepEqual(tasks, want) {
		t.Fatalf("tasks = %+v, want %+v", tasks, want)
	}
}

func TestRunningAgentsWithoutAnAgentsDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, session, wantErr string
	}{
		{name: "no session transcript", session: ""},
		{name: "no subagents directory", session: filepath.Join(dir, "none.jsonl")},
		{
			name:    "a subagents path that is no directory is a failure to look",
			session: filepath.Join(dir, "file.jsonl"),
			wantErr: "list " + filepath.Join(dir, "file", "subagents") + ": ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Join(dir, "file"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "file", "subagents"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			tasks, err := runningAgents(tc.session, subagentNow, newTranscriptScans("", &bytes.Buffer{}))
			if tc.wantErr == "" {
				if err != nil || tasks != nil {
					t.Fatalf("runningAgents(%q) = %v, %v; want nil, nil", tc.session, tasks, err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
				t.Fatalf("runningAgents(%q) error = %v, want it to open %q", tc.session, err, tc.wantErr)
			}
		})
	}
}

// The rows sit below the main line, one per running agent, at most four, the
// fourth then counting the rest; none running leaves the main line as it is,
// and an engine whose agents keep no transcripts here draws none.
func TestRenderDrawsAgentRowsBelowTheMainLine(t *testing.T) {
	quiet := []string{`{"type":"user","timestamp":"2026-09-24T10:02:00Z","message":{"role":"user","content":"go"}}`}
	agents := func(ids ...string) map[string]fixtureAgent {
		out := map[string]fixtureAgent{}
		for index, id := range ids {
			out[id] = fixtureAgent{
				meta:    `{"agentType":"general-purpose","description":` + jsonText(id) + `}`,
				metaAge: time.Duration(len(ids)-index) * time.Minute,
				lines:   quiet,
			}
		}
		return out
	}
	row := func(age, label string) string {
		return "?/? Σ$0.00/0/0/0│general-purpose│running " + age + "│💾–│" + label
	}
	cases := []struct {
		name   string
		engine pfmengine.ID
		agents map[string]fixtureAgent
		want   []string
	}{
		{
			name:   "no agent running",
			engine: pfmengine.Claude,
			agents: map[string]fixtureAgent{"x": {meta: finishedMeta, lines: quiet}},
		},
		{name: "one running", engine: pfmengine.Claude, agents: agents("a1"), want: []string{row("1m:0s", "a1")}},
		{
			name:   "four running fill the four rows",
			engine: pfmengine.Claude,
			agents: agents("a1", "a2", "a3", "a4"),
			want:   []string{row("4m:0s", "a1"), row("3m:0s", "a2"), row("2m:0s", "a3"), row("1m:0s", "a4")},
		},
		{
			name:   "five running: three rows and the count of the rest",
			engine: pfmengine.Claude,
			agents: agents("a1", "a2", "a3", "a4", "a5"),
			want:   []string{row("5m:0s", "a1"), row("4m:0s", "a2"), row("3m:0s", "a3"), "+2 more agents"},
		},
		{name: "a Codex seat draws none", engine: pfmengine.Codex, agents: agents("a1")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := fableRuntime(t.TempDir(), 2, subagentNow)
			runtime.Engine = tc.engine
			runtime.Warn = &bytes.Buffer{}
			bare, err := Render(context.Background(), []byte(`{"session_id":"s1","cwd":"/work/repo"}`), runtime)
			if err != nil {
				t.Fatal(err)
			}
			session := agentDir(t, subagentNow, nil, tc.agents)
			payload := `{"session_id":"s1","cwd":"/work/repo","transcript_path":` + jsonText(session) + `}`
			got, err := Render(context.Background(), []byte(payload), runtime)
			if err != nil {
				t.Fatal(err)
			}
			base := strings.Split(strings.TrimSuffix(bare, "\n"), "\n")
			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			if len(lines) < len(base) {
				t.Fatalf("render has %d lines, fewer than the bare main line's %d: %q", len(lines), len(base), got)
			}
			var rows []string
			for _, line := range lines[len(base):] {
				rows = append(rows, stripANSICodes(line))
			}
			if !reflect.DeepEqual(rows, tc.want) {
				t.Fatalf("agent rows =\n  %q\nwant\n  %q\n(session %s, warn %q)", rows, tc.want, session, runtime.Warn)
			}
		})
	}
}
