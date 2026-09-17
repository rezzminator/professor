package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hostops/pfm/internal/testjail"
)

// TestKillCLIVouchesEngineForUnindexedButVisibleRows reproduces the CLI kill
// bug: `pfm kill <id>` only accepted an id the store already indexed,
// because runKill never told kill.Manager which engine the caller was
// looking at (kill/manager.go's lookupTarget requires a non-empty voucher for
// an id neither the transcript nor the Codex lineage table knows). That
// silently refused ⌃X on exactly the rows the picker shows before the index
// catches up: a fresh agent's live session, and a live Codex pane identified
// only by its exported CODEX_THREAD_ID. Both shapes are reproduced here
// against a REAL tmux pane and REAL (jailed) /proc entries, driving
// `pfm kill` in-process exactly as the CLI does — no fake ProcFS or
// TmuxClient, since runKill's fix must resolve through the same compose pass
// the picker itself uses (scanFleet), not an injected stand-in.
func TestKillCLIVouchesEngineForUnindexedButVisibleRows(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)

	socket := "probe-killcli-" + strconv.Itoa(os.Getpid())
	session := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", "bg", "sleep", "120",
	)
	if output, err := session.CombinedOutput(); err != nil {
		t.Fatalf("start background pane: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	paneOutput, err := exec.Command(
		"tmux", "-L", socket, "list-panes", "-F", "#{pane_pid}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(string(paneOutput)))
	if err != nil {
		t.Fatalf("parse pane pid %q: %v", paneOutput, err)
	}

	const (
		agentID = "b3333333-3333-4333-8333-333333333333"
		codexID = "c4444444-4444-4444-8444-444444444444"
		unknown = "d5555555-5555-4555-8555-555555555555"
	)
	// A live agent: a Claude process on a non-primary config dir, naming
	// itself outright in argv. Its transcript is not indexed — often not even
	// written yet — so DetectAgents (not the index) is the only thing that
	// knows it exists.
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       90101,
		parentPID: panePID,
		comm:      "claude",
		cmdline:   []string{"/opt/claude", "--session-id", agentID},
		environ: map[string]string{
			"CLAUDE_CONFIG_DIR": filepath.Join(jail.home, ".cc", "2"),
		},
	})
	// A live Codex pane that holds no rollout file descriptor — the normal
	// shape of a paginated thread since Codex 0.146.1 — identified only by
	// the CODEX_THREAD_ID it exports into its own environment.
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       90102,
		parentPID: panePID,
		comm:      "codex",
		cmdline:   []string{"/usr/local/bin/codex"},
		environ:   map[string]string{"CODEX_THREAD_ID": codexID},
		withFD:    true,
	})

	// F1 regression: runChatKill now resolves EVERY target — uuid or name —
	// through pfmchat.Resolve first, and a resolved row that is Live with a
	// non-empty Socket and Pane is closed through runResolvedChatKill(...,
	// true, ...), not merely tombstoned. The old code short-circuited
	// resolution entirely for a well-formed uuid target (both agentID and
	// codexID are uuids) and only ever passed --exit when the CLI caller
	// asked for it — never here — so it answered a flat "killed <id>" while
	// the pane and its process kept running. The new output names the
	// mechanism precisely so that ambiguity cannot recur: "closing pane %N on
	// socket S" is proof the resolved chat's real socket and pane reached
	// kill.Request, not just its id.
	for _, id := range []string{agentID, codexID} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"chat", "kill", id}, &stdout, &stderr); code != 0 {
			t.Fatalf(
				"kill %s code=%d stdout=%q stderr=%q",
				id, code, stdout.String(), stderr.String(),
			)
		}
		prefix := "killed " + id + "\tclosing pane "
		suffix := " on socket " + socket + "\n"
		if !strings.HasPrefix(stdout.String(), prefix) || !strings.HasSuffix(stdout.String(), suffix) {
			t.Fatalf(
				"kill %s stdout=%q, want prefix %q and suffix %q (the resolved pane and socket named)",
				id, stdout.String(), prefix, suffix,
			)
		}
	}

	// A Codex tool shell has the thread id but no tmux environment. It still
	// resolves `self` to the live composed row instead of falling into the
	// tmux-only self identifier used by Claude hooks.
	var unkillOut, unkillErr bytes.Buffer
	if code := run([]string{"chat", "unkill", codexID}, &unkillOut, &unkillErr); code != 0 {
		t.Fatalf("unkill Codex fixture code=%d stderr=%q", code, unkillErr.String())
	}
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", codexID)
	var selfOut, selfErr bytes.Buffer
	if code := run([]string{"chat", "kill", "self"}, &selfOut, &selfErr); code != 0 {
		t.Fatalf(
			"Codex app-server kill self code=%d stdout=%q stderr=%q",
			code,
			selfOut.String(),
			selfErr.String(),
		)
	}
	if got, want := selfOut.String(), "killed "+codexID+"\tde-listed only, no live pane closed\n"; got != want {
		t.Fatalf("Codex app-server kill self stdout=%q, want %q", got, want)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "kill", unknown}, &stdout, &stderr); code != codeUnknownChat {
		t.Fatalf(
			"kill unknown code=%d stdout=%q stderr=%q",
			code, stdout.String(), stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "not indexed") {
		t.Fatalf("kill unknown stderr=%q, want a not-indexed refusal", stderr.String())
	}
}

type fakeProcessSpec struct {
	pid, parentPID int
	comm           string
	cmdline        []string
	environ        map[string]string
	withFD         bool
}

// writeFakeProcess fabricates one /proc/<pid> entry under a jailed
// PFM_PROC_ROOT: cmdline and environ as NUL-delimited records, and a
// stat line shaped exactly like the kernel's — RealProcFS parses all three
// verbatim, so a live process needs no more than these files to exist.
func writeFakeProcess(t *testing.T, procRoot string, spec fakeProcessSpec) {
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(spec.pid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "cmdline"),
		[]byte(strings.Join(spec.cmdline, "\x00")+"\x00"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	pairs := make([]string, 0, len(spec.environ))
	for key, value := range spec.environ {
		pairs = append(pairs, key+"="+value)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "environ"),
		[]byte(strings.Join(pairs, "\x00")+"\x00"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	// Fields after "comm) " are: state ppid pgrp session tty_nr tpgid flags
	// minflt cminflt majflt cmajflt utime stime cutime cstime priority nice
	// num_threads itrealvalue starttime — RealProcFS.Stat reads only ppid
	// (index 1) and starttime (index 19), so the rest are placeholders.
	stat := strconv.Itoa(spec.pid) + " (" + spec.comm + ") S " +
		strconv.Itoa(spec.parentPID) +
		" 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 20 0 100\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	if spec.withFD {
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

type killCLIJail struct {
	root     string
	home     string
	procRoot string
}

// newKillCLIJail wires the PFM_* env exactly like jailTest, except the
// tmux directories agree with each other (TMUX_TMPDIR's parent equals
// PFM_TMUX_DIR, tmux's own "tmux-<uid>" convention) so a REAL
// backgrounded tmux session this test starts is the one ProbeTmux finds —
// jailTest's own tmux/t split is fine for its callers, which never start a
// real tmux server, but this test does.
func newKillCLIJail(t *testing.T) *killCLIJail {
	t.Helper()
	root := testjail.ShortRoot(t)
	home := filepath.Join(root, "home")
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	sidDir := filepath.Join(root, "sid")
	claudeRoot := filepath.Join(root, "claude")
	codexHome := filepath.Join(root, "codex")
	procRoot := filepath.Join(root, "proc")
	for _, directory := range []string{
		home, tmuxDir, sidDir, claudeRoot, codexHome, procRoot,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeJailedCodexAuth(t, root)
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SID_DIR", sidDir)
	t.Setenv("PFM_CLAUDE_ROOTS", claudeRoot)
	t.Setenv("PFM_CODEX_ROOT", codexHome)
	t.Setenv("PFM_TMUX_DIR", tmuxDir)
	t.Setenv("PFM_PROC_ROOT", procRoot)
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	return &killCLIJail{root: root, home: home, procRoot: procRoot}
}
