package gather

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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

func TestDetectAgentsAndClaudeProcesses(t *testing.T) {
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

	agents, _, err := DetectAgents(proc, home, panes)
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

// A Claude argv names a session only in the forms Claude takes one; an id
// elsewhere in argv claims nothing, so the stray sweep never kills on it.
func TestClaudeSessionIDs(t *testing.T) {
	const id = "01234567-89ab-cdef-0123-456789abcdef"
	const other = "fedcba98-7654-3210-fedc-ba9876543210"
	for _, test := range []struct {
		name    string
		cmdline []string
		want    []string
	}{
		{name: "session-id word", cmdline: []string{"claude", "--session-id", id}, want: []string{id}},
		{name: "session-id equals", cmdline: []string{"claude", "--session-id=" + id}, want: []string{id}},
		{name: "resume word", cmdline: []string{"claude", "--resume", id}, want: []string{id}},
		{name: "resume equals", cmdline: []string{"claude", "--resume=" + id}, want: []string{id}},
		{name: "short resume", cmdline: []string{"claude", "-r", id}, want: []string{id}},
		{name: "resume transcript path", cmdline: []string{"claude", "--resume", "/tx/" + id + ".jsonl"}, want: []string{id}},
		{name: "both flags", cmdline: []string{"claude", "--session-id", id, "-r", other}, want: []string{id, other}},
		{name: "named twice", cmdline: []string{"claude", "--session-id", id, "--resume", id}, want: []string{id}},
		{name: "id inside a prompt", cmdline: []string{"claude", "-p", "resume " + id}, want: []string{}},
		{name: "id inside a path argument", cmdline: []string{"claude", "--add-dir", "/work/" + id}, want: []string{}},
		{name: "id as the binary", cmdline: []string{id, "--resume"}, want: []string{}},
		{name: "not a uuid", cmdline: []string{"claude", "--resume", "../../not-a-uuid"}, want: []string{}},
		{name: "flag without value", cmdline: []string{"claude", "--resume"}, want: []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ClaudeSessionIDs(test.cmdline); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ClaudeSessionIDs(%q) = %q, want %q", test.cmdline, got, test.want)
			}
		})
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
		func(string) naming.CodexName { return naming.CodexName{Name: longName, User: true} },
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

func TestProcExecutablePathUsesKernelPathNotArgv(t *testing.T) {
	for _, test := range []struct {
		raw, want string
		bad       bool
	}{
		{"\x01\x00\x00\x00/env/python\x00\x00spoofed\x00", "/env/python", false},
		{"\x00\x00", "", true},
		{"\x01\x00\x00\x00relative\x00", "", true},
		{"\x01\x00\x00\x00/env/python", "", true},
	} {
		path, err := procExecutablePath([]byte(test.raw))
		if (err != nil) != test.bad || path != test.want {
			t.Errorf("kernel path=%q %v want=%q bad=%t", path, err, test.want, test.bad)
		}
	}
}

// A refused kern.procargs2 read is told apart by the independent probe and
// the kernel's identity of the process: a vanished process or a zombie is
// absent, another uid's (kill 0 answers EPERM) or a setuid one the owner
// started (another effective uid) is withheld, foreign as on Linux, so the
// caller lookup and the chat-call guard pass over it; a live process of the
// owner's own stays a failed look naming what it runs.
func TestProcessReadErrorUsesIndependentExistence(t *testing.T) {
	euid := uint32(os.Geteuid())
	for _, tc := range []struct {
		name          string
		probe         error
		identity      ProcessIdentity
		identityErr   error
		gone, foreign bool
		want          string
	}{
		{
			"same uid EINVAL", nil,
			ProcessIdentity{EffectiveUID: euid, Command: "sleep"},
			nil, false, false,
			fmt.Sprintf(`invalid argument (command "sleep", effective uid %d)`, euid),
		},
		{
			"other uid EINVAL", syscall.EPERM,
			ProcessIdentity{},
			nil, false, true,
			"gather: process environment withheld: another uid's, non-dumpable or privileged: invalid argument " +
				"(probe: operation not permitted)",
		},
		{
			"setuid EINVAL", nil,
			ProcessIdentity{EffectiveUID: euid + 1, Command: "sudo"},
			nil, false, true,
			fmt.Sprintf("gather: process environment withheld: another uid's, non-dumpable or privileged: "+
				"invalid argument (effective uid %d: permission denied)", euid+1),
		},
		{
			"zombie EINVAL", nil,
			ProcessIdentity{EffectiveUID: euid, Zombie: true},
			nil, true, false,
			"invalid argument (a zombie: file does not exist)",
		},
		{"identity unread", nil, ProcessIdentity{}, syscall.EIO, false, false, "invalid argument\ninput/output error"},
		{
			"vanished EINVAL", syscall.ESRCH,
			ProcessIdentity{},
			nil, true, false,
			"process 42 disappeared after invalid argument: no such process: file does not exist",
		},
		{
			"unknown probe", syscall.EIO,
			ProcessIdentity{},
			nil, false, false,
			"invalid argument\nprobe process 42 existence: input/output error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := processReadError(42, syscall.EINVAL, tc.probe)
			environ := environWithheld(err, tc.probe, tc.identity, tc.identityErr)
			if errors.Is(environ, fs.ErrNotExist) != tc.gone || errors.Is(environ, ErrForeignProcess) != tc.foreign ||
				errors.Is(environ, fs.ErrPermission) != tc.foreign || !tc.gone && !errors.Is(environ, syscall.EINVAL) ||
				environ.Error() != tc.want {
				t.Fatalf("environment read error=%q, want %q (gone %t, foreign and permission %t, EINVAL kept)",
					environ, tc.want, tc.gone, tc.foreign)
			}
		})
	}
}

// OpenProcFS: "" is the kernel's own table, a jail directory the Linux-shaped
// reader on either OS, and a jail root never staged ErrNoProcRoot — never the
// live table, so a caller keeps its own answer for "no table".
//
// The system root is the native table too, never stat'ed as a jail: macOS has
// no /proc, and a missing one read as "no process" let pfm chat call append
// into a live chat.
func TestOpenProcFSSelectsTheTable(t *testing.T) {
	for _, root := range []string{"", paths.SystemProcRoot()} {
		native, err := OpenProcFS(root)
		if err != nil {
			t.Fatalf("OpenProcFS(%q) error = %v", root, err)
		}
		if _, isJail := native.(RealProcFS); isJail {
			t.Fatalf("OpenProcFS(%q) = %#v, want the native table", root, native)
		}
	}
	jail := t.TempDir()
	if err := os.MkdirAll(filepath.Join(jail, "4242"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeProcFixture(t, filepath.Join(jail, "4242", "stat"), "4242 (sh) S 1"+strings.Repeat(" 0", 50)+"\n")
	table, err := OpenProcFS(jail)
	if err != nil || table != (RealProcFS{Root: jail}) {
		t.Fatalf("OpenProcFS(%s) = %#v, %v; want RealProcFS{Root: %s}", jail, table, err, jail)
	}
	missing := filepath.Join(jail, "missing-proc")
	table, err = OpenProcFS(missing)
	if table != nil || !errors.Is(err, ErrNoProcRoot) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("OpenProcFS(%s) = %#v, %v; want ErrNoProcRoot wrapping fs.ErrNotExist", missing, table, err)
	}
}

// Stat finds the parent after the LAST ") ", since a command name may hold a
// closing parenthesis, and names a truncated record instead of panicking.
func TestRealProcFSStatParsesAfterTheCommandName(t *testing.T) {
	root := t.TempDir()
	for pid, record := range map[string]string{
		"4242": "4242 (worker ) helper) S 41 4242 4242 0 -1" + strings.Repeat(" 0", 47) + "\n",
		"4243": "4243 (fixture) S\n",
	} {
		if err := os.MkdirAll(filepath.Join(root, pid), 0o700); err != nil {
			t.Fatal(err)
		}
		writeProcFixture(t, filepath.Join(root, pid, "stat"), record)
	}
	proc := RealProcFS{Root: root}
	if got, err := proc.Stat(4242); err != nil || got.ParentPID != 41 {
		t.Fatalf("Stat(4242) = %+v, %v; want parent 41", got, err)
	}
	if _, err := proc.Stat(4243); err == nil || err.Error() != "short proc stat for pid 4243" {
		t.Fatalf("Stat(4243) error = %v, want %q", err, "short proc stat for pid 4243")
	}
}

// A refused environment of a process that can never be the caller's pane
// (another uid's, or a terminal-less one holding capabilities the caller
// lacks, as systemd --user) is ErrForeignProcess; a process with a terminal
// keeps its plain read failure however privileged it is.
func TestRealProcFSEnvironWithheldIsForeign(t *testing.T) {
	root := t.TempDir()
	proc := RealProcFS{Root: root}
	for _, tc := range []struct {
		name               string
		pid                int
		capabilities, stat string
		otherOwner         bool
		foreign            bool
	}{
		{"user manager", 4242, "0000000800000000", "4242 (systemd) S 1 4242 4242 0 -1", false, true},
		{"unprivileged", 4243, "0000000000000000", "4243 (sh) S 1 4243 4243 0 -1", false, false},
		{"privileged on a terminal", 4244, "0000000800000000", "4244 (claude) S 1 4244 4244 34816 4244", false, false},
		{"privileged, terminal unread", 4245, "0000000800000000", "", false, false},
		{"another uid's", 4246, "0000000000000000", "4246 (sh) S 1 4246 4246 34816 4246", true, true},
	} {
		dir := filepath.Join(root, strconv.Itoa(tc.pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeProcFixture(t, filepath.Join(dir, "status"), "Name:\tfixture\nCapEff:\t"+tc.capabilities+"\n")
		if tc.stat != "" {
			writeProcFixture(t, filepath.Join(dir, "stat"), tc.stat+"\n")
		}
		environ := filepath.Join(dir, "environ")
		writeProcFixture(t, environ, "A=1\x00")
		if tc.otherOwner {
			ownByAnotherUID(t, environ)
		}
		denied := &os.PathError{Op: "open", Path: environ, Err: syscall.EACCES}
		err := proc.withheld(tc.pid, denied)
		if !errors.Is(err, fs.ErrPermission) || errors.Is(err, ErrForeignProcess) != tc.foreign {
			t.Errorf("%s: pid %d refused environ = %v, want foreign %t (fixture %s)",
				tc.name, tc.pid, err, tc.foreign, dir)
		}
	}
}

// ownByAnotherUID makes path's owner differ from this process's effective uid:
// root chowns it; any other uid points it at a root-owned file.
func ownByAnotherUID(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 4246, 4246); err != nil {
			t.Fatalf("chown %s: %v", path, err)
		}
		return
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/", path); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxEnvironTagsForeignProcess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every environment")
	}
	if _, err := os.ReadFile(filepath.Join(paths.SystemProcRoot(), "1", "environ")); !errors.Is(err, os.ErrPermission) {
		t.Skipf("pid 1's environment is not refused here: %v", err)
	}
	_, err := RealProcFS{}.Environ(1)
	if !errors.Is(err, ErrForeignProcess) || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("pid 1 (root) was not tagged foreign: %v", err)
	}
}

func TestLinuxEnvironKeepsOwnRefusedRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "4242"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "4242", "environ"), []byte("A=1\x00"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := (RealProcFS{Root: root}).Environ(4242)
	if !errors.Is(err, os.ErrPermission) || errors.Is(err, ErrForeignProcess) {
		t.Fatalf("a refused read of the caller's own file must stay a read failure: %v", err)
	}
}

func writeProcFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
