package stale

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"hostops/pfm/internal/gather"
)

// fixture is a process table on disk — <root>/<pid>/{cmdline,exe} — the
// shape gather.RealProcFS reads, so the sweep's classification runs against
// real file identities with no live process touched.
type fixture struct {
	t      *testing.T
	root   string
	binary string
	old    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	fixture := &fixture{
		t:      t,
		root:   filepath.Join(root, "proc"),
		binary: filepath.Join(root, "bin", "pfm"),
		old:    filepath.Join(root, "bin", "pfm.replaced"),
	}
	for _, path := range []string{fixture.binary, fixture.old} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		// Distinct files are distinct inodes: the install renamed a new file
		// over the path, and the old image lives on only in its processes.
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture *fixture) process(pid int, image string, argv ...string) {
	fixture.t.Helper()
	directory := filepath.Join(fixture.root, strconv.Itoa(pid))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "cmdline"),
		[]byte(strings.Join(argv, "\x00")+"\x00"),
		0o600,
	); err != nil {
		fixture.t.Fatal(err)
	}
	if image != "" {
		if err := os.Symlink(image, filepath.Join(directory, "exe")); err != nil {
			fixture.t.Fatal(err)
		}
	}
}

// signaler plays the kernel: a TERM or KILL a pid honours removes its
// directory (the process exited), signal 0 answers whether it still exists.
func (fixture *fixture) signaler(ignoreTerm, ignoreKill map[int]bool, sent *[]string) Signaler {
	return func(pid int, signal syscall.Signal) error {
		directory := filepath.Join(fixture.root, strconv.Itoa(pid))
		if _, err := os.Stat(directory); err != nil {
			return syscall.ESRCH
		}
		if signal == 0 {
			return nil
		}
		*sent = append(*sent, signal.String()+" "+strconv.Itoa(pid))
		if (signal == syscall.SIGTERM && ignoreTerm[pid]) || (signal == syscall.SIGKILL && ignoreKill[pid]) {
			return nil
		}
		return os.RemoveAll(directory)
	}
}

func TestFindNamesOnlyPfmProcessesRunningAReplacedBinary(t *testing.T) {
	fixture := newFixture(t)
	fixture.process(101, fixture.old, fixture.binary, "mcp", "chat", "serve")
	fixture.process(102, fixture.binary, fixture.binary, "mcp", "serve")
	fixture.process(103, fixture.old, "/usr/bin/sleep", "60")
	fixture.process(104, fixture.old, "pfm")
	var sent []string
	scan, err := Find(gather.NewProcFS(fixture.root), fixture.binary, fixture.signaler(nil, nil, &sent))
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
	fixture := newFixture(t)
	fixture.process(201, "", fixture.binary, "ls")
	var sent []string
	scan, err := Find(gather.NewProcFS(fixture.root), fixture.binary, fixture.signaler(nil, nil, &sent))
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Unreadable) != 1 || !strings.Contains(scan.Unreadable[0], "201") {
		t.Fatalf("unreadable = %v, want pid 201 named", scan.Unreadable)
	}
}

// A table that cannot say which file a process runs is refused outright: the
// sweep would otherwise classify every process as fresh and report clean.
func TestFindRefusesATableThatCannotReadImages(t *testing.T) {
	fixture := newFixture(t)
	var sent []string
	_, err := Find(imageless{gather.NewProcFS(fixture.root)}, fixture.binary, fixture.signaler(nil, nil, &sent))
	if err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("err = %v, want a refusal naming the missing capability", err)
	}
}

type imageless struct{ gather.ProcFS }

func TestSweepTermsThenKillsThenProvesNoneLeft(t *testing.T) {
	fixture := newFixture(t)
	fixture.process(301, fixture.old, fixture.binary, "ls")
	fixture.process(302, fixture.old, fixture.binary, "mcp", "chat", "serve")
	fixture.process(303, fixture.binary, fixture.binary, "mcp", "serve")
	var sent []string
	var stdout bytes.Buffer
	err := Sweep(
		gather.NewProcFS(fixture.root),
		fixture.binary,
		fixture.signaler(map[int]bool{302: true}, nil, &sent),
		&stdout,
		10*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("sweep: %v\n%s", err, stdout.String())
	}
	if got := strings.Join(sent, ","); got != "terminated 301,terminated 302,killed 302" {
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

func TestSweepFailsLoudOnASurvivor(t *testing.T) {
	fixture := newFixture(t)
	fixture.process(401, fixture.old, fixture.binary, "ls")
	var sent []string
	var stdout bytes.Buffer
	err := Sweep(
		gather.NewProcFS(fixture.root),
		fixture.binary,
		fixture.signaler(map[int]bool{401: true}, map[int]bool{401: true}, &sent),
		&stdout,
		10*time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want the survivor named", err)
	}
}

func TestRunReportsNoneAndStaleDistinctly(t *testing.T) {
	fixture := newFixture(t)
	fixture.process(501, fixture.binary, fixture.binary, "mcp", "serve")
	var sent []string
	var stdout, stderr bytes.Buffer
	signal := fixture.signaler(nil, nil, &sent)
	if code := run(
		nil,
		&stdout,
		&stderr,
		gather.NewProcFS(fixture.root),
		fixture.binary,
		signal,
	); code != 0 ||
		!strings.Contains(stdout.String(), "stale: none") {
		t.Fatalf("code=%d stdout=%q stderr=%q, want none", code, stdout.String(), stderr.String())
	}
	fixture.process(502, fixture.old, fixture.binary, "ls")
	stdout.Reset()
	if code := run(nil, &stdout, &stderr, gather.NewProcFS(fixture.root), fixture.binary, signal); code != 0 ||
		!strings.Contains(stdout.String(), "STALE pid=502") || !strings.Contains(stdout.String(), "make sweep-stale") {
		t.Fatalf("code=%d stdout=%q, want pid 502 listed with the sweep named", code, stdout.String())
	}
	if code := run(
		[]string{"--bogus"},
		&stdout,
		&stderr,
		gather.NewProcFS(fixture.root),
		fixture.binary,
		signal,
	); code != 2 {
		t.Fatalf("code=%d, want a usage refusal", code)
	}
}

// kill(2) reads pid 0 as the caller's process group and -1 as every process
// the user owns: the sweep must never hand either to a signal.
func TestDeliverRefusesPidsThatNameMoreThanOneProcess(t *testing.T) {
	var sent []int
	record := func(pid int, _ syscall.Signal) error { sent = append(sent, pid); return nil }
	for _, pid := range []int{0, -1, -42} {
		deliver(record, pid, syscall.SIGKILL)
	}
	deliver(record, 7, syscall.SIGTERM)
	if len(sent) != 1 || sent[0] != 7 {
		t.Fatalf("signalled %v, want only pid 7", sent)
	}
}
