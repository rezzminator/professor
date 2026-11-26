package statusline

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Agents answers for a whole session at once: every running agent's facts,
// oldest first, an empty list when none runs, or exactly the agents named,
// running or not, an id with no transcript, or one that can name no file,
// answered absent; a path it cannot read is the failure it is, never a
// session without agents.
func TestAgents(t *testing.T) {
	quiet := []string{`{"type":"user","timestamp":"2026-09-24T10:02:00Z","message":{"role":"user","content":"go"}}`}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "file"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file", "subagents"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		session func(t *testing.T) string
		ids     []string
		want    string // sessionId, then each agent's id and factLine (and "absent"), or the error
		warn    string // the warnings, compared whole when set
	}{
		{
			name: "no agent running is an empty list",
			session: func(t *testing.T) string {
				return agentDir(t, subagentNow, nil, map[string]fixtureAgent{"x": {meta: finishedMeta, lines: quiet}})
			},
			want: `session []`,
		},
		{
			name: "every running agent, oldest first",
			session: func(t *testing.T) string {
				return agentDir(t, subagentNow, nil, map[string]fixtureAgent{
					"a2": {meta: backgroundMeta, metaAge: time.Minute, lines: quiet},
					"a1": {meta: backgroundMeta, metaAge: 2 * time.Minute, lines: agentTranscriptLines},
				})
			},
			want: "session a1 Σ$0.01/2.0K/35/2│Opus 5.5│1 error│💾94%│⟲1 a2 Σ$0.00/0/0/0│💾–",
		},
		{
			name: "named ids answer exactly those agents, ended or running, in their order, each once",
			session: func(t *testing.T) string {
				return agentDir(t, subagentNow, nil, map[string]fixtureAgent{
					"a1":    {meta: backgroundMeta, metaAge: 2 * time.Minute, lines: agentTranscriptLines},
					"ended": {meta: finishedMeta, metaAge: time.Minute, lines: agentTranscriptLines},
					"other": {meta: backgroundMeta, metaAge: time.Minute, lines: quiet},
				})
			},
			ids:  []string{"ended", "a1", "ended"},
			want: "session ended Σ$0.01/2.0K/35/2│Opus 5.5│1 error│💾94%│⟲1 a1 Σ$0.01/2.0K/35/2│Opus 5.5│1 error│💾94%│⟲1",
		},
		{
			name: "the engine's model names an agent whose transcript and meta name none, the transcript's wins over it",
			session: func(t *testing.T) string {
				return agentDir(t, subagentNow, nil, map[string]fixtureAgent{
					"a1": {meta: backgroundMeta, metaAge: 2 * time.Minute, lines: agentTranscriptLines},
					"a2": {meta: backgroundMeta, metaAge: time.Minute, lines: quiet},
				})
			},
			ids:  []string{"a2=claude-opus-5-5", "a1=claude-sonnet-5-5", "a2=claude-haiku-4-5"},
			want: "session a2 Σ$0.00/0/0/0│Opus 5.5│💾– a1 Σ$0.01/2.0K/35/2│Opus 5.5│1 error│💾94%│⟲1",
		},
		{
			name: "an engine model of no model id's shape is dropped, the agent still answered",
			session: func(t *testing.T) string {
				return agentDir(t, subagentNow, nil, map[string]fixtureAgent{
					"a2": {meta: backgroundMeta, lines: quiet},
				})
			},
			ids:  []string{"a2=opus 5;rm", "a2="},
			want: "session a2 Σ$0.00/0/0/0│💾–",
			warn: `statusline: agents: a2: engine model "opus 5;rm" is no model id (letters, digits and ._:/@[]-); ` +
				"the agent is asked without it\n",
		},
		{
			name: "an absent agent and an unnamable one wear the engine's model",
			session: func(t *testing.T) string {
				return agentDir(
					t,
					subagentNow,
					nil,
					map[string]fixtureAgent{"a1": {meta: backgroundMeta, lines: quiet}},
				)
			},
			ids: []string{"gone=claude-fable-5-2", "lead@crew=claude-sonnet-5-5"},
			want: "session gone Σ$?/?/?/?│Fable 5.2│💾! absent lead@crew Σ$?/?/?/?│Sonnet 5.5│💾! absent " +
				`agent id "lead@crew" names no sub-agent transcript (letters, digits, _ and -)`,
		},
		{
			name: "a named id with no transcript on disk is answered, absent",
			session: func(t *testing.T) string {
				return agentDir(
					t,
					subagentNow,
					nil,
					map[string]fixtureAgent{"a1": {meta: backgroundMeta, lines: quiet}},
				)
			},
			ids:  []string{"gone"},
			want: "session gone Σ$?/?/?/?│💾! absent",
		},
		{
			name: "an id that cannot name a file is answered absent, the others as they stand",
			session: func(t *testing.T) string {
				return agentDir(t, subagentNow, nil, map[string]fixtureAgent{
					"a1": {meta: backgroundMeta, metaAge: 2 * time.Minute, lines: agentTranscriptLines},
				})
			},
			ids: []string{"lead@crew", "a1", "../a1"},
			want: "session lead@crew Σ$?/?/?/?│💾! absent " +
				`agent id "lead@crew" names no sub-agent transcript (letters, digits, _ and -) ` +
				"a1 Σ$0.01/2.0K/35/2│Opus 5.5│1 error│💾94%│⟲1 ../a1 Σ$?/?/?/?│💾! absent " +
				`agent id "../a1" names no sub-agent transcript (letters, digits, _ and -)`,
		},
		{
			name:    "a path that names no transcript",
			session: func(*testing.T) string { return filepath.Join(dir, "notes.txt") },
			want:    `error: session transcript "{dir}/notes.txt" is no .jsonl path`,
		},
		{
			name:    "a subagents path that is no directory is a failure to look",
			session: func(*testing.T) string { return filepath.Join(dir, "file.jsonl") },
			want:    "error: list {dir}/file/subagents: open {dir}/file/subagents: not a directory",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var warn bytes.Buffer
			runtime := Runtime{
				Now: func() time.Time { return subagentNow }, Home: t.TempDir(), CacheDir: t.TempDir(),
				Env: map[string]string{}, Prices: testPrices(t), Warn: &warn,
			}
			report, err := Agents(tc.session(t), tc.ids, runtime)
			got := "error: " + strings.ReplaceAll(errString(err), dir, "{dir}")
			if err == nil {
				parts := []string{report.SessionID}
				for _, facts := range report.Agents {
					parts = append(parts, facts.ID, factLine(facts))
					if facts.Absent {
						parts = append(parts, "absent")
						if !agentIDPattern.MatchString(facts.ID) { // no path in its cause
							parts = append(parts, facts.Error)
						}
					}
				}
				if len(report.Agents) == 0 { // the wire says [], never null
					encoded, _ := json.Marshal(report.Agents)
					parts = append(parts, string(encoded))
				}
				got = strings.Join(parts, " ")
				if report.At != subagentNow.UnixMilli() || report.Look.Palette["reset"] != reset {
					t.Errorf(
						"report at %d look %v, want %d and the main line's palette",
						report.At, report.Look, subagentNow.UnixMilli(),
					)
				}
			}
			if got != tc.want {
				t.Fatalf("Agents = %q, want %q (warn %q)", got, tc.want, warn.String())
			}
			if tc.warn != "" && warn.String() != tc.warn {
				t.Fatalf("Agents warned %q, want %q", warn.String(), tc.warn)
			}
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
