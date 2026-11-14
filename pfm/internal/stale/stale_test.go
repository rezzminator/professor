package stale

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/gather/gathertest"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const (
	// sweepUID is the user the fixture's processes and the sweep run as.
	sweepUID = 4100
	// sweepInstallMark is the fixture install's mark, carried by its processes.
	sweepInstallMark = "5eed5eed5eed5eed"
	sweepWait        = 10 * time.Millisecond
)

// fixture is a process table on disk — <root>/<pid>/{cmdline,environ,stat,exe}
// — the shape gather.RealProcFS reads, so the sweep's classification runs
// against real file identities with no live process touched. uid and exe
// override what the kernel would report as a process's owner and executable
// path.
type fixture struct {
	t      *testing.T
	root   string
	home   string
	binary string
	old    string
	uid    map[int]uint32
	exe    map[int]string
}

func (fixture *fixture) markerPath() string {
	return compatibleProxyMarker(fixture.home)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	fixture := &fixture{
		t:      t,
		root:   filepath.Join(root, "proc"),
		home:   filepath.Join(root, "home"),
		binary: filepath.Join(root, "bin", "pfm"),
		old:    filepath.Join(root, "bin", "pfm.replaced"),
		uid:    map[int]uint32{},
		exe:    map[int]string{},
	}
	for _, path := range []string{fixture.binary, fixture.old} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		// Distinct files are distinct inodes: the install renamed a new file
		// over the path, and the old image lives on only in its processes.
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

// process adds pid running image with argv, started at 1000+pid and carrying
// this install's mark.
func (fixture *fixture) process(pid int, image string, argv ...string) {
	fixture.t.Helper()
	directory := filepath.Join(fixture.root, strconv.Itoa(pid))
	if err := os.MkdirAll(filepath.Join(directory, "fd"), 0o700); err != nil {
		fixture.t.Fatal(err)
	}
	fixture.rewrite(pid, uint64(1000+pid), argv...)
	fixture.environ(pid, paths.EnvInstallMark+"="+sweepInstallMark)
	if image != "" {
		if err := os.Symlink(image, filepath.Join(directory, "exe")); err != nil {
			fixture.t.Fatal(err)
		}
	}
}

func (fixture *fixture) write(pid int, name, content string) {
	fixture.t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.root, strconv.Itoa(pid), name), []byte(content), 0o600); err != nil {
		fixture.t.Fatal(err)
	}
}

// rewrite sets pid's start time and argv in place: a later generation on the
// same pid when start changes, an exec when it does not.
func (fixture *fixture) rewrite(pid int, start uint64, argv ...string) {
	fixture.t.Helper()
	fixture.write(pid, "cmdline", strings.Join(argv, "\x00")+"\x00")
	fixture.write(
		pid,
		"stat",
		fmt.Sprintf("%d (pfm) S 1 %d 0 0 %d 0 0 0 0 0 0 0 0 0 20 0 1 0 %d 0 0", pid, pid, pid, start),
	)
}

// environ replaces pid's environment; no entries is a process without a mark.
func (fixture *fixture) environ(pid int, entries ...string) {
	fixture.t.Helper()
	var content strings.Builder
	for _, entry := range entries {
		content.WriteString(entry + "\x00")
	}
	fixture.write(pid, "environ", content.String())
}

func (fixture *fixture) markProxy(pid, fd int) {
	fixture.t.Helper()
	marker := fixture.markerPath()
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		fixture.t.Fatal(err)
	}
	target, err := filepath.EvalSymlinks(marker)
	if err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(fixture.root, strconv.Itoa(pid), "fd", strconv.Itoa(fd))); err != nil {
		fixture.t.Fatal(err)
	}
}

// fixtureTable reads the fixture as the kernel would. It is built on the
// fixture root directly, never gather.NewProcFS, which falls back to the live
// process table when the root does not exist.
type fixtureTable struct {
	gather.RealProcFS
	fixture *fixture
}

func (fixture *fixture) table() fixtureTable {
	return fixtureTable{RealProcFS: gather.RealProcFS{Root: fixture.root}, fixture: fixture}
}

// ProcessIdentity is pid's owner: the sweep's user unless uid says otherwise.
func (table fixtureTable) ProcessIdentity(pid int) (gather.ProcessIdentity, error) {
	if _, err := os.Stat(filepath.Join(table.fixture.root, strconv.Itoa(pid))); err != nil {
		return gather.ProcessIdentity{}, err
	}
	uid, found := table.fixture.uid[pid]
	if !found {
		uid = sweepUID
	}
	return gather.ProcessIdentity{EffectiveUID: uid, Command: "pfm"}, nil
}

// Executable is the path pid was executed from: the installed binary unless
// exe says otherwise, as the kernel still reports a replaced image's path.
func (table fixtureTable) Executable(pid int) (string, error) {
	if _, err := os.Stat(filepath.Join(table.fixture.root, strconv.Itoa(pid))); err != nil {
		return "", err
	}
	if executable, found := table.fixture.exe[pid]; found {
		return executable, nil
	}
	return table.fixture.binary, nil
}

// probe answers signal 0: ESRCH once pid's directory is gone.
func (fixture *fixture) probe(pid int) error {
	if _, err := os.Stat(filepath.Join(fixture.root, strconv.Itoa(pid))); err != nil {
		return syscall.ESRCH
	}
	return nil
}

// kernel plays the kernel: a pid whose directory is gone cannot be opened, and
// a TERM or KILL a pid honours removes its directory (the process exited).
func (fixture *fixture) kernel(ignoreTerm, ignoreKill map[int]bool) *gathertest.Kernel {
	return &gathertest.Kernel{
		OnOpen: fixture.probe,
		OnSignal: func(pid int, sig syscall.Signal) error {
			if (sig == syscall.SIGTERM && ignoreTerm[pid]) || (sig == syscall.SIGKILL && ignoreKill[pid]) {
				return nil
			}
			return os.RemoveAll(filepath.Join(fixture.root, strconv.Itoa(pid)))
		},
	}
}

// signalled is every signal kernel delivered, as "terminated 301,killed 302".
func signalled(kernel *gathertest.Kernel) string {
	delivered := make([]string, 0, len(kernel.Sent))
	for _, sent := range kernel.Sent {
		delivered = append(delivered, sent.Sig.String()+" "+strconv.Itoa(sent.PID))
	}
	return strings.Join(delivered, ",")
}

func (fixture *fixture) door(table gather.ProcFS, kernel *gathertest.Kernel) gather.Door {
	return gathertest.Door(table, kernel, sweepUID, os.Getpid())
}

func (fixture *fixture) install() gather.Install {
	return gather.Install{Mark: sweepInstallMark, Binary: fixture.binary}
}

// sweep sweeps the fixture through kernel against binary.
func (fixture *fixture) sweep(table gather.ProcFS, kernel *gathertest.Kernel, binary string, stdout io.Writer) error {
	return SweepStaleProcesses(
		fixture.door(
			table,
			kernel,
		),
		fixture.install(),
		binary,
		fixture.root,
		fixture.home,
		fixture.probe,
		stdout,
		sweepWait,
	)
}

func TestFindNamesOnlyPfmProcessesRunningAReplacedBinary(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(101, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	fixture.process(102, fixture.binary, fixture.binary, "mcp", "serve")
	fixture.process(103, fixture.old, "/usr/bin/sleep", "60")
	fixture.process(104, fixture.old, "pfm")
	scan, err := Find(fixture.table(), fixture.binary, fixture.probe)
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, process := range scan.Stale {
		pids = append(pids, process.PID)
	}
	if len(pids) != 2 || pids[0] != 101 || pids[1] != 104 || len(scan.Unreadable) != 0 {
		t.Fatalf("stale = %v unreadable = %v, want pids 101 and 104 only", scan.Stale, scan.Unreadable)
	}
	if !scan.Stale[0].MCP || scan.Stale[1].MCP {
		t.Fatalf("stale = %+v, want only pid 101 marked as a chat MCP server", scan.Stale)
	}
}

// "Could not look" is never "nothing there": a live pfm whose image cannot be
// read is named, and fails the scan, rather than passing as fresh.
func TestFindNamesAProcessItCouldNotRead(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(201, "", fixture.binary, "ls")
	scan, err := Find(fixture.table(), fixture.binary, fixture.probe)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.NewReplacer(fixture.root, "{root}", fixture.home, "{home}").
		Replace(staleUnreadableCommand.ReplaceAllString(strings.Join(scan.Unreadable, "\n"), "${1}{cmd}")),
		"pid=201  {cmd}: stat {root}/201/exe: no such file or directory"; got != want {
		t.Fatalf("unreadable = %q, want %q", got, want)
	}
}

// A table that cannot say which file a process runs is refused outright: the
// sweep would otherwise classify every process as fresh and report clean.
func TestFindRefusesATableThatCannotReadImages(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	_, err := Find(imageless{fixture.table()}, fixture.binary, fixture.probe)
	if got, want := fmt.Sprint(
		err,
	), "this process table cannot read which file a process executes, so stale processes cannot be told from fresh ones"; got != want {
		t.Fatalf("err = %q, want %q", got, want)
	}
}

type imageless struct{ gather.ProcFS }

type unreadableDescriptors struct {
	fixtureTable
	err error
}

func (table unreadableDescriptors) FDLinks(int) ([]gather.FDLink, error) { return nil, table.err }

func TestSweepTermsThenKillsThenProvesNoneLeft(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(301, fixture.old, fixture.binary, "ls")
	fixture.process(302, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	fixture.process(303, fixture.binary, fixture.binary, "mcp", "serve")
	kernel := fixture.kernel(map[int]bool{302: true}, nil)
	var stdout bytes.Buffer
	if err := fixture.sweep(fixture.table(), kernel, fixture.binary, &stdout); err != nil {
		t.Fatalf("sweep: %v\n%s", err, stdout.String())
	}
	if got := signalled(kernel); got != "terminated 301,terminated 302,killed 302" {
		t.Fatalf("signals = %q, want TERM to both stale, KILL to the one that ignored it", got)
	}
	for _, want := range []string{"sweep: TERM pid=301", "sweep: KILL pid=302", "reconnects it with /mcp", "sweep: done"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("sweep output lacks %q:\n%s", want, stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "303")); err != nil {
		t.Fatalf("the fresh daemon was touched: %v", err)
	}
}

// TestSweepSignalsOnlyThisInstallsIdentifiedProcesses: the sweep signals a
// stale pfm only when it is this install's — its mark, or no mark and this
// install's binary path — and only the generation it identified, re-read
// through the door right before each signal.
func TestSweepSignalsOnlyThisInstallsIdentifiedProcesses(t *testing.T) {
	t.Parallel()
	const (
		term = "sweep: TERM pid=301  pfm chat watch\n"
		done = "sweep: done — no obsolete pfm process runs a replaced binary\n"
		none = "sweep: none — no obsolete pfm process runs a replaced binary\n"
		keep = "sweep: KEEP pid=301 another install's process  pfm chat watch\n"
	)
	tests := []struct {
		name      string
		setup     func(fixture *fixture, kernel *gathertest.Kernel)
		oldBinary bool
		wantSent  string
		wantOut   string
		wantErr   string
	}{
		{name: "this install's stale process", wantSent: "terminated 301", wantOut: term + done},
		{
			name:     "unmarked process from the installed path",
			setup:    func(fixture *fixture, _ *gathertest.Kernel) { fixture.environ(301) },
			wantSent: "terminated 301",
			wantOut:  term + done,
		},
		{
			name: "unmarked process from another path",
			setup: func(fixture *fixture, _ *gathertest.Kernel) {
				fixture.environ(301)
				fixture.exe[301] = testjail.Place("/work/tree/bin/pfm")
			},
			wantOut: keep + none,
		},
		{
			name: "another install's mark",
			setup: func(fixture *fixture, _ *gathertest.Kernel) {
				fixture.environ(301, paths.EnvInstallMark+"=0123456789abcdef")
			},
			wantOut: keep + none,
		},
		{
			name:    "this install's mark on a build from another path",
			setup:   func(fixture *fixture, _ *gathertest.Kernel) { fixture.exe[301] = testjail.Place("/work/tree/bin/pfm") },
			wantOut: keep + none,
		},
		{
			name: "pid reused before TERM",
			setup: func(fixture *fixture, kernel *gathertest.Kernel) {
				kernel.OnOpen = func(pid int) error {
					fixture.rewrite(pid, 9301, "/usr/bin/sleep", "60")
					return nil
				}
			},
			wantOut: term + "sweep: GONE pid=301 exited or was replaced before TERM; not signalled\n" + done,
		},
		{
			name: "exec'd another program before TERM",
			setup: func(fixture *fixture, kernel *gathertest.Kernel) {
				kernel.OnOpen = func(pid int) error {
					fixture.rewrite(pid, 1301, "/usr/bin/vim")
					return nil
				}
			},
			wantOut: term + "sweep: SKIP pid=301 changed before TERM; not signalled\n" + done,
		},
		{
			name:    "another user's process",
			setup:   func(fixture *fixture, _ *gathertest.Kernel) { fixture.uid[301] = 0 },
			wantOut: term,
			wantErr: "SWEEP-INCOMPLETE: still running a replaced binary: 301 (pid 301: signal refused: runs as uid 0, not 4100)",
		},
		{
			name: "pid reused after TERM",
			setup: func(fixture *fixture, kernel *gathertest.Kernel) {
				kernel.OnSignal = func(pid int, _ syscall.Signal) error {
					fixture.rewrite(pid, 9301, "/usr/bin/sleep", "60")
					return nil
				}
			},
			wantSent: "terminated 301",
			wantOut:  term + done,
		},
		{
			name: "ignores TERM",
			setup: func(fixture *fixture, kernel *gathertest.Kernel) {
				*kernel = *fixture.kernel(map[int]bool{301: true}, nil)
			},
			wantSent: "terminated 301,killed 301",
			wantOut:  term + "sweep: KILL pid=301 (ignored TERM for 10ms)\n" + done,
		},
		{
			name:      "a build other than the installed one",
			oldBinary: true,
			wantErr: "sweep refused: {old} is not this install's pfm {binary}, " +
				"and only the installed binary tells this install's stale processes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fixture := newFixture(t)
			fixture.process(301, fixture.old, "pfm", "chat", "watch")
			kernel := fixture.kernel(nil, nil)
			if tt.setup != nil {
				tt.setup(fixture, kernel)
			}
			binary := fixture.binary
			if tt.oldBinary {
				binary = fixture.old
			}
			expand := strings.NewReplacer("{old}", fixture.old, "{binary}", fixture.binary)
			var stdout bytes.Buffer
			err := fixture.sweep(fixture.table(), kernel, binary, &stdout)
			if wantErr := expand.Replace(tt.wantErr); (err == nil && wantErr != "") ||
				(err != nil && err.Error() != wantErr) {
				t.Fatalf("sweep error = %v; want %q", err, wantErr)
			}
			if got := signalled(kernel); got != tt.wantSent {
				t.Fatalf("signals = %q; want %q", got, tt.wantSent)
			}
			if got, want := stdout.String(), expand.Replace(tt.wantOut); got != want {
				t.Fatalf("sweep output:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestSweepKeepsMarkedProxyAndTermsUnmarkedServer(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(
		311,
		fixture.old,
		fixture.binary,
		"--config",
		"/tmp/pfm.json",
		"mcp",
		"serve",
		"--stdio",
		"--family",
		"chat",
	)
	fixture.markProxy(311, 9)
	fixture.process(312, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	kernel := fixture.kernel(nil, nil)
	var stdout bytes.Buffer
	if err := fixture.sweep(fixture.table(), kernel, fixture.binary, &stdout); err != nil {
		t.Fatalf("sweep: %v\n%s", err, stdout.String())
	}
	if got := signalled(kernel); got != "terminated 312" {
		t.Fatalf("signals = %q, want only the unmarked server terminated", got)
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "311")); err != nil {
		t.Fatalf("marked proxy was swept: %v", err)
	}
	if output := stdout.String(); !strings.Contains(output, "sweep: KEEP pid=311 compatible stdio proxy") ||
		!strings.Contains(output, "reconnects it with /mcp") {
		t.Fatalf("output = %q, want KEEP and reconnect notices", output)
	}
}

func TestClassifyCompatibleProxies(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(313, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	fixture.markProxy(313, 9)
	fixture.process(314, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	table := fixture.table()
	scan, err := Find(table, fixture.binary, fixture.probe)
	if err != nil {
		t.Fatal(err)
	}
	obsolete, compatible, err := ClassifyCompatibleProxies(table, fixture.root, fixture.home, fixture.probe, scan.Stale)
	if err != nil {
		t.Fatal(err)
	}
	if len(obsolete) != 1 || obsolete[0].PID != 314 || len(compatible) != 1 || compatible[0].PID != 313 {
		t.Fatalf("obsolete=%+v compatible=%+v, want 314 obsolete and 313 compatible", obsolete, compatible)
	}
}

func TestClassifyCompatibleProxyCommandForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		argv       []string
		compatible bool
	}{
		{name: "chat family", argv: []string{"pfm", "mcp", "serve", "--stdio", "--family", "chat"}, compatible: true},
		{
			name:       "unknown family",
			argv:       []string{"pfm", "mcp", "serve", "--stdio", "--family", "other"},
			compatible: false,
		},
		{name: "unknown family", argv: []string{"pfm", "mcp", "serve", "--stdio", "--family", "unknown"}},
		{
			name: "stdio split config",
			argv: []string{
				"/opt/pfm",
				"--config",
				"/tmp/pfm.json",
				"mcp",
				"serve",
				"--stdio",
				"--family",
				"chat",
			},
			compatible: true,
		},
		{
			name:       "stdio joined config",
			argv:       []string{"pfm", "--config=/tmp/pfm.json", "mcp", "serve", "--stdio", "--family", "chat"},
			compatible: true,
		},
		{name: "daemon", argv: []string{"pfm", "mcp", "serve"}},
		{name: "daemon split config", argv: []string{"pfm", "--config", "/tmp/pfm.json", "mcp", "serve"}},
		{name: "daemon joined config", argv: []string{"pfm", "--config=/tmp/pfm.json", "mcp", "serve"}},
		{name: "list", argv: []string{"pfm", "mcp", "ls"}},
		{name: "chat enable", argv: []string{"pfm", "mcp", "chat", "enable"}},
		{name: "trailing argument", argv: []string{"pfm", "mcp", "serve", "--stdio", "extra"}},
		{name: "config after mcp", argv: []string{"pfm", "mcp", "serve", "--stdio", "--config", "/tmp/pfm.json"}},
		{
			name: "repeated split config",
			argv: []string{
				"pfm",
				"--config",
				"/tmp/one.json",
				"--config",
				"/tmp/two.json",
				"mcp",
				"serve",
				"--stdio",
				"--family",
				"chat",
			},
		},
		{
			name: "repeated joined config",
			argv: []string{
				"pfm",
				"--config=/tmp/one.json",
				"--config=/tmp/two.json",
				"mcp",
				"serve",
				"--stdio",
				"--family",
				"chat",
			},
		},
		{
			name: "empty split config",
			argv: []string{"pfm", "--config", "", "mcp", "serve", "--stdio", "--family", "chat"},
		},
		{
			name: "whitespace split config",
			argv: []string{"pfm", "--config", "   ", "mcp", "serve", "--stdio", "--family", "chat"},
		},
		{name: "missing split config", argv: []string{"pfm", "--config"}},
		{
			name: "empty joined config",
			argv: []string{"pfm", "--config=", "mcp", "serve", "--stdio", "--family", "chat"},
		},
		{
			name: "whitespace joined config",
			argv: []string{"pfm", "--config=   ", "mcp", "serve", "--stdio", "--family", "chat"},
		},
		{
			name: "unrelated leading flag",
			argv: []string{"pfm", "--verbose", "mcp", "serve", "--stdio", "--family", "chat"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.process(401, fixture.old, test.argv...)
			fixture.markProxy(401, 9)
			if test.compatible {
				fixture.process(402, fixture.old, test.argv...)
			}
			table := fixture.table()
			scan, err := Find(table, fixture.binary, fixture.probe)
			if err != nil {
				t.Fatal(err)
			}
			obsolete, compatible, err := ClassifyCompatibleProxies(
				table, fixture.root, fixture.home, fixture.probe, scan.Stale,
			)
			if err != nil {
				t.Fatal(err)
			}
			if test.compatible {
				if len(compatible) != 1 || compatible[0].PID != 401 || len(obsolete) != 1 || obsolete[0].PID != 402 {
					t.Fatalf(
						"obsolete=%+v compatible=%+v, want unmarked 402 obsolete and marked 401 compatible",
						obsolete,
						compatible,
					)
				}
			} else if len(compatible) != 0 || len(obsolete) != 1 || obsolete[0].PID != 401 {
				t.Fatalf("obsolete=%+v compatible=%+v, want marked invalid command 401 obsolete", obsolete, compatible)
			}
		})
	}
}

func TestClassifyCompatibleProxiesFailsClosedWhenDescriptorsAreUnreadable(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(315, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	table := fixture.table()
	scan, err := Find(table, fixture.binary, fixture.probe)
	if err != nil {
		t.Fatal(err)
	}
	probeErr := errors.New("descriptor probe denied")
	obsolete, compatible, err := ClassifyCompatibleProxies(
		unreadableDescriptors{fixtureTable: table, err: probeErr},
		fixture.root,
		fixture.home,
		fixture.probe,
		scan.Stale,
	)
	if got, want := strings.NewReplacer(fixture.root, "{root}", fixture.home, "{home}").
		Replace(staleCommand.ReplaceAllString(fmt.Sprint(err), "${1}{cmd}")),
		"inspect descriptors for pid=315 {cmd} against marker {home}/.local/state/pfm/mcp-proxy-compatible: descriptor probe denied"; !errors.Is(err, probeErr) ||
		got != want {
		t.Fatalf("err = %q, want %q", got, want)
	}
	if obsolete != nil || compatible != nil {
		t.Fatalf("obsolete=%+v compatible=%+v, want no asserted partition on error", obsolete, compatible)
	}
}

func TestClassifyCompatibleProxiesOmitsVanishedCandidate(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(316, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	table := fixture.table()
	scan, err := Find(table, fixture.binary, fixture.probe)
	if err != nil {
		t.Fatal(err)
	}
	obsolete, compatible, err := ClassifyCompatibleProxies(
		unreadableDescriptors{fixtureTable: table, err: errors.New("process vanished")},
		fixture.root,
		fixture.home,
		func(int) error { return syscall.ESRCH },
		scan.Stale,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(obsolete) != 0 || len(compatible) != 0 {
		t.Fatalf("obsolete=%+v compatible=%+v, want vanished candidate omitted", obsolete, compatible)
	}
}

func TestSweepDoesNotKeepMarkerFileWithoutHolder(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(321, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	marker := fixture.markerPath()
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	kernel := fixture.kernel(nil, nil)
	if err := fixture.sweep(fixture.table(), kernel, fixture.binary, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := signalled(kernel); got != "terminated 321" {
		t.Fatalf("signals = %q, want marker file without holder to preserve nothing", got)
	}
}

func TestSweepFailsBeforeSignalsWhenProxyDescriptorsAreUnreadable(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(331, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	fixture.process(332, fixture.old, fixture.binary, "ls")
	kernel := fixture.kernel(nil, nil)
	probeErr := errors.New("descriptor probe denied")
	err := fixture.sweep(
		unreadableDescriptors{fixtureTable: fixture.table(), err: probeErr},
		kernel,
		fixture.binary,
		io.Discard,
	)
	if got, want := strings.NewReplacer(fixture.root, "{root}", fixture.home, "{home}").
		Replace(staleCommand.ReplaceAllString(fmt.Sprint(err), "${1}{cmd}")),
		"inspect descriptors for pid=331 {cmd} against marker {home}/.local/state/pfm/mcp-proxy-compatible: descriptor probe denied"; !errors.Is(err, probeErr) ||
		got != want {
		t.Fatalf("err = %q, want %q", got, want)
	}
	if len(kernel.Sent) != 0 {
		t.Fatalf("signals = %v, want classification failure before the first signal", kernel.Sent)
	}
}

func TestSweepFailsClosedWhenOneDescriptorIsOmitted(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(336, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	if err := os.WriteFile(filepath.Join(fixture.root, "336", "fd", "8"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	kernel := fixture.kernel(nil, nil)
	err := fixture.sweep(fixture.table(), kernel, fixture.binary, io.Discard)
	if got, want := strings.NewReplacer(fixture.root, "{root}", fixture.home, "{home}").
		Replace(staleCommand.ReplaceAllString(fmt.Sprint(err), "${1}{cmd}")),
		"inspect descriptors for pid=336 {cmd}: descriptor 8 was not readable"; got != want {
		t.Fatalf("err = %q, want %q", got, want)
	}
	if len(kernel.Sent) != 0 {
		t.Fatalf("signals = %v, want incomplete descriptor census to fail before signalling", kernel.Sent)
	}
}

func TestSweepOmitsProxyThatExitsDuringDescriptorProbe(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(341, fixture.old, fixture.binary, "mcp", "serve", "--stdio", "--family", "chat")
	kernel := fixture.kernel(nil, nil)
	table := unreadableDescriptors{fixtureTable: fixture.table(), err: errors.New("process vanished")}
	vanished := func(pid int) error {
		if pid == 341 {
			return syscall.ESRCH
		}
		return nil
	}
	err := SweepStaleProcesses(
		fixture.door(table, kernel), fixture.install(), fixture.binary, fixture.root, fixture.home, vanished,
		io.Discard, sweepWait,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(kernel.Sent) != 0 {
		t.Fatalf("signals = %v, want vanished candidate omitted", kernel.Sent)
	}
}

func TestSweepFailsLoudOnASurvivor(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(401, fixture.old, fixture.binary, "ls")
	kernel := fixture.kernel(map[int]bool{401: true}, map[int]bool{401: true})
	var stdout bytes.Buffer
	err := fixture.sweep(fixture.table(), kernel, fixture.binary, &stdout)
	if err == nil || err.Error() != "SWEEP-INCOMPLETE: still running a replaced binary: 401" {
		t.Fatalf("err = %v, want the survivor named", err)
	}
	if got := signalled(kernel); got != "terminated 401,killed 401" {
		t.Fatalf("signals = %q, want TERM then KILL to the survivor", got)
	}
}

func TestRunReportsNoneAndStaleDistinctly(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.process(501, fixture.binary, fixture.binary, "mcp", "serve")
	var stdout, stderr bytes.Buffer
	run := func(args []string) int {
		door := fixture.door(fixture.table(), fixture.kernel(nil, nil))
		return runStale(
			args, &stdout, &stderr, door, fixture.install(), fixture.binary, fixture.root, fixture.home, fixture.probe,
		)
	}
	if code := run(nil); code != 0 || !strings.Contains(stdout.String(), "stale: none") {
		t.Fatalf("code=%d stdout=%q stderr=%q, want none", code, stdout.String(), stderr.String())
	}
	fixture.process(502, fixture.old, fixture.binary, "ls")
	stdout.Reset()
	if code := run(nil); code != 0 ||
		!strings.Contains(stdout.String(), "STALE pid=502") ||
		!strings.Contains(stdout.String(), "make sweep-stale") {
		t.Fatalf("code=%d stdout=%q, want pid 502 listed with the sweep named", code, stdout.String())
	}
	if code := run([]string{"--bogus"}); code != 2 {
		t.Fatalf("code=%d, want a usage refusal", code)
	}
}

// staleCommand matches a process's command column, whose temp path the scan
// truncates at a width, so its length differs per run.
var staleCommand = regexp.MustCompile(`(pid=\d+ +)[^\s:]+`)

// staleUnreadableCommand matches the whole command column of an unreadable row, which ends at its
// colon: clipped at its width, the column ends inside the temp path or after the arguments, by the
// path's length in this run.
var staleUnreadableCommand = regexp.MustCompile(`(pid=\d+ +)[^:]+`)
