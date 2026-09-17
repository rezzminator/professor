package gather

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestNativeProcFSSmokeOwnProcessOnly drives whichever reader this platform
// actually uses — /proc on Linux, sysctl and lsof on macOS — against the one
// process every platform lets a caller read in full: its own. Asking for the
// native reader rather than naming RealProcFS is the point; it is what makes
// this a contract both implementations must satisfy.
func TestNativeProcFSSmokeOwnProcessOnly(t *testing.T) {
	proc := NewProcFS("")
	pid := os.Getpid()

	cmdline, err := proc.Cmdline(pid)
	if err != nil {
		t.Fatalf("Cmdline(own pid) error = %v", err)
	}
	if len(cmdline) == 0 {
		t.Fatal("Cmdline(own pid) is empty")
	}
	if _, err := proc.Environ(pid); err != nil {
		t.Fatalf("Environ(own pid) error = %v", err)
	}
	if _, err := proc.FDLinks(pid); err != nil {
		t.Fatalf("FDLinks(own pid) error = %v", err)
	}
	stat, err := proc.Stat(pid)
	if err != nil {
		t.Fatalf("Stat(own pid) error = %v", err)
	}
	if stat.ParentPID <= 0 || stat.StartTime == 0 {
		t.Fatalf("Stat(own pid) = %+v, want parent and start time", stat)
	}
	birther, ok := proc.(ProcBirth)
	if !ok {
		t.Fatal("native ProcFS does not report a birth time")
	}
	birth, err := birther.Birth(pid)
	if err != nil {
		t.Fatalf("Birth(own pid) error = %v", err)
	}
	if birth <= 0 || birth > time.Now().Unix() {
		t.Fatalf("Birth(own pid) = %d, want a past epoch second", birth)
	}
	memory, ok := proc.(ProcMemory)
	if !ok {
		t.Fatal("native ProcFS does not report resident memory")
	}
	resident, err := memory.RSSKB(pid)
	if err != nil {
		t.Fatalf("RSSKB(own pid) error = %v", err)
	}
	if resident <= 0 {
		t.Fatalf("RSSKB(own pid) = %d, want a positive size", resident)
	}
}

func TestDetectCodexRootMetadataAndAncestorMatching(t *testing.T) {
	codexHome := t.TempDir()
	first := filepath.Join(codexHome, "sessions", "2026", "rollout-first.jsonl")
	second := filepath.Join(codexHome, "sessions", "2026", "rollout-second.jsonl")
	writeRolloutMeta(t, first, "user", "")
	writeRolloutMeta(t, second, "subagent", "first")
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		200: {stat: ProcStat{ParentPID: 100}},
		300: {stat: ProcStat{ParentPID: 200}},
		400: {
			cmdline: []string{"/usr/bin/codex"},
			fdLinks: []FDLink{
				{FD: 8, Target: second},
				{FD: 3, Target: first},
				{FD: 2, Target: "/outside/rollout-ignore.jsonl"},
			},
			stat: ProcStat{ParentPID: 300},
		},
		401: {
			cmdline: []string{"/usr/bin/not-codex"},
			fdLinks: []FDLink{{FD: 1, Target: first}},
		},
	}}
	panes := []ProbePane{{Socket: "cx-1-2-3", PaneID: "%4", PID: 100}}

	got, err := DetectCodex(proc, codexHome, panes)
	if err != nil {
		t.Fatalf("DetectCodex() error = %v", err)
	}
	want := []LiveCodex{{
		PID:         400,
		PanePID:     100,
		Socket:      "cx-1-2-3",
		PaneID:      "%4",
		RolloutPath: first,
		ThreadID:    "first",
		RolloutHeld: true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectCodex() = %#v, want %#v", got, want)
	}
}

func TestDetectAgentsAndCache1H(t *testing.T) {
	home := "/jail/home"
	sessionOne := "01234567-89ab-cdef-0123-456789abcdef"
	sessionTwo := "fedcba98-7654-3210-fedc-ba9876543210"
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		500: {stat: ProcStat{ParentPID: 1}},
		700: {
			cmdline: []string{
				"/opt/claude",
				"--session-id",
				sessionOne,
				"--resume",
				"/transcripts/" + sessionTwo + ".jsonl",
			},
			environ: map[string]string{
				"CLAUDE_CONFIG_DIR":        "/jail/home/.cc/2",
				"ENABLE_PROMPT_CACHING_1H": "1",
			},
			stat: ProcStat{ParentPID: 500, StartTime: 99},
		},
		701: {
			cmdline: []string{"/opt/claude", "--resume", sessionOne},
			environ: map[string]string{
				"CLAUDE_CONFIG_DIR":        filepath.Join(home, ".claude"),
				"ENABLE_PROMPT_CACHING_1H": "1",
			},
			stat: ProcStat{ParentPID: 500, StartTime: 100},
		},
		702: {
			cmdline: []string{"/opt/claude", "--resume", "../../not-a-uuid"},
			environ: map[string]string{
				"CLAUDE_CONFIG_DIR": "/jail/home/.cc/3",
			},
			stat: ProcStat{ParentPID: 500},
		},
	}}
	panes := []ProbePane{{Socket: "cc-1-2-3", PaneID: "%5", PID: 500}}

	agents, err := DetectAgents(proc, home, panes)
	if err != nil {
		t.Fatalf("DetectAgents() error = %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("DetectAgents() = %#v, want two strict IDs", agents)
	}
	if agents[0].SessionID != sessionOne ||
		agents[1].SessionID != sessionTwo ||
		agents[0].ConfigDir != "/jail/home/.cc/2" ||
		agents[0].Socket != "cc-1-2-3" ||
		agents[0].StartTime != 99 {
		t.Fatalf("DetectAgents() = %#v", agents)
	}

	cacheSockets, err := DetectCache1H(proc, panes)
	if err != nil {
		t.Fatalf("DetectCache1H() error = %v", err)
	}
	if !reflect.DeepEqual(cacheSockets, []string{"cc-1-2-3"}) {
		t.Fatalf("DetectCache1H() = %q, want cc socket", cacheSockets)
	}

	claudeProcesses, err := DetectClaudeProcesses(proc, panes)
	if err != nil {
		t.Fatalf("DetectClaudeProcesses() error = %v", err)
	}
	if len(claudeProcesses) != 3 {
		t.Fatalf(
			"DetectClaudeProcesses() = %#v, want every Claude process",
			claudeProcesses,
		)
	}
	for _, process := range claudeProcesses {
		if process.Socket != "cc-1-2-3" ||
			process.PaneID != "%5" ||
			process.PanePID != 500 {
			t.Fatalf("DetectClaudeProcesses() process = %#v", process)
		}
	}
}

// TestCache1HBadgeFollowsTheOptOut fixtures the OUTCOME the picker draws — the
// ⚡ badge on or off — for each way a live Claude can be born. Since Claude Code
// 2.1.215 the 1h window is the harness DEFAULT, so FORCE_PROMPT_CACHING_5M=1 is
// the only thing that turns the badge off.
func TestCache1HBadgeFollowsTheOptOut(t *testing.T) {
	tests := []struct {
		name    string
		environ map[string]string
		noEnv   bool
		badge   bool
	}{
		{
			name:  "flagless elder born before the force-5m rewire",
			badge: true,
		},
		{
			name:    "explicitly armed 1h",
			environ: map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"},
			badge:   true,
		},
		{
			name:    "deliberately born 5m",
			environ: map[string]string{"FORCE_PROMPT_CACHING_5M": "1"},
			badge:   false,
		},
		{
			name: "both flags — the 5m opt-out still wins",
			environ: map[string]string{
				"ENABLE_PROMPT_CACHING_1H": "1",
				"FORCE_PROMPT_CACHING_5M":  "1",
			},
			badge: false,
		},
		{
			name:    "force-5m set to something other than 1",
			environ: map[string]string{"FORCE_PROMPT_CACHING_5M": "0"},
			badge:   true,
		},
		{
			name:  "environment unreadable — same answer as flagless",
			noEnv: true,
			badge: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proc := &fakeProcFS{processes: map[int]fakeProcess{
				500: {stat: ProcStat{ParentPID: 1}},
				700: {
					cmdline:    []string{"/opt/claude"},
					environ:    test.environ,
					environErr: test.noEnv,
					stat:       ProcStat{ParentPID: 500},
				},
			}}
			panes := []ProbePane{{Socket: "cc-1-2-3", PaneID: "%5", PID: 500}}
			sockets, err := DetectCache1H(proc, panes)
			if err != nil {
				t.Fatalf("DetectCache1H() error = %v", err)
			}
			want := []string{}
			if test.badge {
				want = []string{"cc-1-2-3"}
			}
			if !reflect.DeepEqual(sockets, want) {
				t.Fatalf("DetectCache1H() = %q, want %q", sockets, want)
			}
		})
	}
}

// A socket hosting several Claude processes is 5m as soon as ONE of them was
// born that way, whichever order the scan reaches them in — the badge must
// never promise a cheaper window than the chat actually has.
func TestCache1HSharedSocketTakesTheColdestBirth(t *testing.T) {
	for _, forced := range []int{700, 900} {
		processes := map[int]fakeProcess{
			500: {stat: ProcStat{ParentPID: 1}},
			700: {
				cmdline: []string{"/opt/claude"},
				environ: map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"},
				stat:    ProcStat{ParentPID: 500},
			},
			900: {
				cmdline: []string{"/opt/claude"},
				environ: map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"},
				stat:    ProcStat{ParentPID: 500},
			},
		}
		cold := processes[forced]
		cold.environ = map[string]string{"FORCE_PROMPT_CACHING_5M": "1"}
		processes[forced] = cold
		sockets, err := DetectCache1H(
			&fakeProcFS{processes: processes},
			[]ProbePane{{Socket: "cc-1-2-3", PaneID: "%5", PID: 500}},
		)
		if err != nil {
			t.Fatalf("DetectCache1H() error = %v", err)
		}
		if len(sockets) != 0 {
			t.Fatalf("pid %d forced 5m but sockets = %q", forced, sockets)
		}
	}
}

func TestWindowConvergenceClipsRunesOnlyHere(t *testing.T) {
	longName := strings.Repeat("界", 25)
	panes := []ProbePane{{
		Socket:      "cx-1-2-3",
		SessionName: "cx-session",
		WindowID:    "@1",
		WindowName:  "old",
		PaneID:      "%1",
	}}
	codex := []LiveCodex{{
		Socket:      "cx-1-2-3",
		PaneID:      "%1",
		RolloutPath: "/codex/rollout.jsonl",
	}}

	renames := computeWindowRenames(
		panes,
		codex,
		nil,
		func(string) string { return longName },
		nil,
	)
	if len(renames) != 1 {
		t.Fatalf("computeWindowRenames() = %#v, want one", renames)
	}
	if got, want := renames[0].TargetName, strings.Repeat("界", 24); got != want {
		t.Fatalf("TargetName = %q, want %q", got, want)
	}
	if !utf8.ValidString(renames[0].TargetName) {
		t.Fatalf("TargetName is invalid UTF-8: %x", renames[0].TargetName)
	}
}

// TestRealProcFSBirthIsBootTimePlusStartTicksNotTheProcDirMtime pins Birth to
// /proc/stat btime + the /proc/<pid>/stat start tick. The fixture's pid
// directory carries a deliberately wrong, recent mtime: the lazily
// instantiated procfs inode the old implementation read as the birth.
func TestRealProcFSBirthIsBootTimePlusStartTicksNotTheProcDirMtime(t *testing.T) {
	root := t.TempDir()
	pidDir := filepath.Join(root, "4242")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "stat"),
		[]byte("cpu  1 2 3 4\nbtime 1700000000\nprocesses 9\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	// A comm holding ") " pins the last-paren parse; field 22 (starttime) is 12345 ticks.
	stat := "4242 (tmux: a) b) S 1 4242 4242 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 12345 0 0\n"
	if err := os.WriteFile(filepath.Join(pidDir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	lazy := time.Unix(1_800_000_000, 0)
	if err := os.Chtimes(pidDir, lazy, lazy); err != nil {
		t.Fatal(err)
	}
	got, err := RealProcFS{Root: root}.Birth(4242)
	if want := int64(1_700_000_000 + 12345/100); err != nil || got != want {
		t.Fatalf(
			"Birth = %d, %v; want %d (btime + starttime/USER_HZ), not the pid dir mtime %d",
			got,
			err,
			want,
			lazy.Unix(),
		)
	}

	// No btime to count from: an error, never a zero that reads as "unknown
	// but fine" or a guess.
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte("cpu  1 2 3 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := (RealProcFS{Root: root}).Birth(4242); err == nil {
		t.Fatalf("Birth without btime = %d, nil; want an error", got)
	}
}

// Image names the file a process EXECUTES by device and inode — the identity
// an install's rename-over changes and a running process keeps.
func TestRealProcFSImageIsTheExecutablesFileID(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "pfm")
	if err := os.WriteFile(binary, []byte("image"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "proc", "7"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binary, filepath.Join(root, "proc", "7", "exe")); err != nil {
		t.Fatal(err)
	}
	table, ok := NewProcFS(filepath.Join(root, "proc")).(ProcImage)
	if !ok {
		t.Fatal("RealProcFS does not report process images")
	}
	got, err := table.Image(7)
	if err != nil {
		t.Fatal(err)
	}
	want, err := FileIDOf(binary)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || want.Inode == 0 {
		t.Fatalf("Image = %+v, want %+v", got, want)
	}
}
