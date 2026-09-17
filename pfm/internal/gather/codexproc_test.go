package gather

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hostops/pfm/internal/resolve"
)

// A Codex session that writes no rollout file holds no rollout descriptor
// either, so identity has to come from the state store. Without a resolver
// the process stays invisible, which is the pre-0.146 behavior every other
// caller still gets.
func TestDetectCodexThreadsIdentifiesSessionsWithoutRolloutFiles(t *testing.T) {
	codexRoot := t.TempDir()
	declared := filepath.Join(
		codexRoot,
		"sessions",
		"2026",
		"rollout-2026-01-01T00-00-00-paginated.jsonl",
	)
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		400: {
			cmdline: []string{"/usr/bin/codex"},
			environ: map[string]string{resolve.CodexThreadEnv: "paginated"},
			stat:    ProcStat{ParentPID: 100},
			birth:   1700,
		},
	}}
	panes := []Pane{{
		Socket:      "cx-1-2-3",
		PaneID:      "%4",
		PID:         100,
		CurrentPath: "/work/paginated",
	}}

	if invisible, err := DetectCodex(proc, codexRoot, panes); err != nil ||
		len(invisible) != 0 {
		t.Fatalf("DetectCodex() = %#v, error = %v; want no rollout-less rows", invisible, err)
	}

	var gotExported, gotCWD string
	var gotBirth int64
	got, err := DetectCodexThreads(
		proc,
		codexRoot,
		panes,
		func(exported, cwd string, birth int64, _, _ string) (string, string) {
			gotExported, gotCWD, gotBirth = exported, cwd, birth
			return "paginated", declared
		},
	)
	if err != nil {
		t.Fatalf("DetectCodexThreads() error = %v", err)
	}
	want := []LiveCodex{{
		PID:         400,
		PanePID:     100,
		Socket:      "cx-1-2-3",
		PaneID:      "%4",
		RolloutPath: declared,
		ThreadID:    "paginated",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectCodexThreads() = %#v, want %#v", got, want)
	}
	if gotExported != "paginated" ||
		gotCWD != "/work/paginated" ||
		gotBirth != 1700 {
		t.Fatalf(
			"resolver received (%q, %q, %d), want the exported identity, pane directory, and birth",
			gotExported,
			gotCWD,
			gotBirth,
		)
	}
}

// A pane resumed through the picker holds no rollout descriptor and exports
// no thread id, and its thread was created hours before the process — nothing
// a clock or a directory can reach. Its argv states the thread outright, and
// that is enough to make it a live chat with no state store consulted at all.
func TestDetectCodexThreadsIdentifiesResumedSessionsByArgv(t *testing.T) {
	const resumed = "019feb1b-1215-7f30-b18b-e227e5ca26e5"
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		400: {
			cmdline: []string{
				"/opt/codex",
				"--dangerously-bypass-approvals-and-sandbox",
				"resume",
				resumed,
			},
			stat:  ProcStat{ParentPID: 100},
			birth: 1786403951,
		},
	}}
	panes := []Pane{{
		Socket:      "cx-1-2-3",
		PaneID:      "%4",
		PID:         100,
		CurrentPath: "/work/proja",
	}}

	live, err := DetectCodex(proc, "/jail/codex", panes)
	if err != nil {
		t.Fatalf("DetectCodex() error = %v", err)
	}
	want := []LiveCodex{{
		PID:      400,
		PanePID:  100,
		Socket:   "cx-1-2-3",
		PaneID:   "%4",
		ThreadID: resumed,
	}}
	if !reflect.DeepEqual(live, want) {
		t.Fatalf("DetectCodex() = %#v, want %#v", live, want)
	}
}

// CODEX_THREAD_ID is INHERITED: a `codex resume` launched from inside another
// Codex session's shell carries the parent's thread in its environment and its
// own in its argv. The argv is what this process is actually running.
func TestDetectCodexThreadsPrefersResumeArgvOverAnInheritedEnvironment(t *testing.T) {
	const resumed = "019feb1b-1215-7f30-b18b-e227e5ca26e5"
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		400: {
			cmdline: []string{"/opt/codex", "resume", resumed},
			environ: map[string]string{resolve.CodexThreadEnv: "the-parent-thread"},
			stat:    ProcStat{ParentPID: 100},
		},
	}}
	panes := []Pane{{Socket: "cx-1-2-3", PaneID: "%4", PID: 100}}

	var asked string
	live, err := DetectCodexThreads(
		proc,
		"/jail/codex",
		panes,
		func(exported, _ string, _ int64, _, _ string) (string, string) {
			asked = exported
			return exported, ""
		},
	)
	if err != nil {
		t.Fatalf("DetectCodexThreads() error = %v", err)
	}
	if asked != resumed {
		t.Fatalf("resolver received %q, want the argv thread %q", asked, resumed)
	}
	if len(live) != 1 || live[0].ThreadID != resumed {
		t.Fatalf("DetectCodexThreads() = %#v, want the argv thread", live)
	}
}

// `codex resume --last` and `codex resume <name>` name no thread here, and a
// bare `resume` at the end of argv names nothing either.
func TestCodexResumeArgvTakesOnlyAUUID(t *testing.T) {
	const resumed = "019feb1b-1215-7f30-b18b-e227e5ca26e5"
	tests := []struct {
		name    string
		cmdline []string
		want    string
	}{
		{name: "no arguments", cmdline: []string{"/opt/codex"}},
		{name: "last", cmdline: []string{"/opt/codex", "resume", "--last"}},
		{name: "by name", cmdline: []string{"/opt/codex", "resume", "BUILDER_WF"}},
		{name: "trailing resume", cmdline: []string{"/opt/codex", "resume"}},
		{
			name:    "uuid",
			cmdline: []string{"/opt/codex", "resume", resumed},
			want:    resumed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codexResumeArgv(test.cmdline); got != test.want {
				t.Fatalf("codexResumeArgv() = %q, want %q", got, test.want)
			}
		})
	}
}

// An unidentifiable codex process — the app-server daemon, for instance —
// never becomes a live chat row.
func TestDetectCodexThreadsSkipsUnidentifiedProcesses(t *testing.T) {
	codexRoot := t.TempDir()
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		400: {
			cmdline: []string{"/usr/bin/codex"},
			stat:    ProcStat{ParentPID: 100},
			birth:   1700,
		},
	}}
	panes := []Pane{{Socket: "cx-1-2-3", PaneID: "%4", PID: 100}}

	live, err := DetectCodexThreads(
		proc,
		codexRoot,
		panes,
		func(string, string, int64, string, string) (string, string) { return "", "" },
	)
	if err != nil {
		t.Fatalf("DetectCodexThreads() error = %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("DetectCodexThreads() = %#v, want no rows", live)
	}
}

// A process-held rollout is current identity. A compact/reset continuation
// can rotate it while argv and CODEX_THREAD_ID still name the old thread, so
// the file must win without consulting the state-store resolver.
func TestDetectCodexThreadsPrefersCurrentRolloutOverInheritedIdentity(t *testing.T) {
	codexRoot := t.TempDir()
	rollout := filepath.Join(codexRoot, "sessions", "2026", "rollout-live.jsonl")
	writeRolloutMeta(t, rollout, "user", "")
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		400: {
			cmdline: []string{"/usr/bin/codex"},
			environ: map[string]string{resolve.CodexThreadEnv: "live-thread"},
			fdLinks: []FDLink{{FD: 3, Target: rollout}},
			stat:    ProcStat{ParentPID: 100},
		},
	}}
	panes := []Pane{{Socket: "cx-1-2-3", PaneID: "%4", PID: 100}}

	live, err := DetectCodexThreads(
		proc,
		codexRoot,
		panes,
		func(string, string, int64, string, string) (string, string) {
			t.Fatal("the resolver was consulted for a session holding a rollout descriptor")
			return "", ""
		},
	)
	if err != nil {
		t.Fatalf("DetectCodexThreads() error = %v", err)
	}
	want := []LiveCodex{{
		PID:         400,
		PanePID:     100,
		Socket:      "cx-1-2-3",
		PaneID:      "%4",
		RolloutPath: rollout,
		ThreadID:    "live",
		RolloutHeld: true,
	}}
	if !reflect.DeepEqual(live, want) {
		t.Fatalf("DetectCodexThreads() = %#v, want %#v", live, want)
	}
}

// RolloutHeld is the whole point of the fix: only a rollout the process
// itself has open right now may claim to be that process's current
// conversation. A rollout-less session resolved through the state-store
// resolver still gets a RolloutPath — the resolver's answer, for display and
// lineage — but it must never be marked held, because fleet.ObserveCodexPanes uses
// RolloutHeld to decide whether this identity is allowed to override the
// pane's own screen (ls_pipeline.go). Blanket-true here would silently restore
// the defect this field exists to prevent.
func TestDetectCodexThreadsMarksOnlyAnFDHeldRolloutAsHeld(t *testing.T) {
	codexRoot := t.TempDir()
	heldRollout := filepath.Join(codexRoot, "sessions", "2026", "rollout-held.jsonl")
	writeRolloutMeta(t, heldRollout, "user", "")
	resolvedRollout := filepath.Join(codexRoot, "sessions", "2026", "rollout-resolved-paginated.jsonl")
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		400: {
			cmdline: []string{"/usr/bin/codex"},
			fdLinks: []FDLink{{FD: 3, Target: heldRollout}},
			stat:    ProcStat{ParentPID: 100},
		},
		101: {stat: ProcStat{ParentPID: 1}},
		401: {
			cmdline: []string{"/usr/bin/codex"},
			environ: map[string]string{resolve.CodexThreadEnv: "paginated"},
			stat:    ProcStat{ParentPID: 101},
		},
	}}
	panes := []Pane{
		{Socket: "cx-1-2-3", PaneID: "%4", PID: 100},
		{Socket: "cx-4-5-6", PaneID: "%5", PID: 101},
	}

	live, err := DetectCodexThreads(
		proc,
		codexRoot,
		panes,
		func(exported, _ string, _ int64, _, _ string) (string, string) {
			return exported, resolvedRollout
		},
	)
	if err != nil {
		t.Fatalf("DetectCodexThreads() error = %v", err)
	}
	want := []LiveCodex{
		{
			PID:         400,
			PanePID:     100,
			Socket:      "cx-1-2-3",
			PaneID:      "%4",
			RolloutPath: heldRollout,
			ThreadID:    "held",
			RolloutHeld: true,
		},
		{
			PID:         401,
			PanePID:     101,
			Socket:      "cx-4-5-6",
			PaneID:      "%5",
			RolloutPath: resolvedRollout,
			ThreadID:    "paginated",
			RolloutHeld: false,
		},
	}
	if !reflect.DeepEqual(live, want) {
		t.Fatalf("DetectCodexThreads() = %#v, want %#v", live, want)
	}
}

func TestCodexRolloutIDAcceptsTimestampedAndUntimestampedFiles(t *testing.T) {
	tests := map[string]string{
		"/codex/sessions/rollout-2026-01-02T03-04-05-thread-id.jsonl": "thread-id",
		"/codex/sessions/rollout-unusual.jsonl":                       "unusual",
		"/codex/sessions/not-a-rollout.jsonl":                         "",
		"":                                                            "",
	}
	for path, want := range tests {
		if got := CodexRolloutID(path); got != want {
			t.Fatalf("CodexRolloutID(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestDetectCodexThreadsMatchesRolloutsUnderEveryConfiguredRoot(t *testing.T) {
	roots := []string{t.TempDir(), t.TempDir()}
	rollout := filepath.Join(roots[1], "sessions", "2026", "rollout-second.jsonl")
	writeRolloutMeta(t, rollout, "user", "")
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		400: {
			cmdline: []string{"/usr/bin/codex"},
			fdLinks: []FDLink{{FD: 9, Target: rollout}},
			stat:    ProcStat{ParentPID: 100},
		},
	}}
	panes := []Pane{{Socket: "cx-1-2-3", PaneID: "%4", PID: 100}}
	live, err := DetectCodexThreadsInRoots(proc, roots, panes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].RolloutPath != rollout {
		t.Fatalf("multi-root live Codex=%#v, want rollout %q", live, rollout)
	}
}

func TestHeldCodexRootOutranksSubagentDescriptor(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(sessions, "rollout-child.jsonl")
	parent := filepath.Join(sessions, "rollout-root.jsonl")
	for path, text := range map[string]string{
		child:  `{"type":"session_meta","payload":{"id":"child","source":{"subagent":{"thread_spawn":{"parent_thread_id":"root"}}}}}` + "\n",
		parent: `{"type":"session_meta","payload":{"id":"root","source":"cli"}}` + "\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		400: {
			cmdline: []string{"/usr/bin/codex"},
			stat:    ProcStat{ParentPID: 100},
			fdLinks: []FDLink{{FD: 3, Target: child}, {FD: 7, Target: parent}},
		},
	}}
	live, err := DetectCodexThreads(proc, root, []Pane{{Socket: "cx-1-2-3", PaneID: "%0", PID: 100}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].ThreadID != "root" || !live[0].RolloutHeld {
		t.Fatalf("wrong live root: %#v", live)
	}
}
