package hookentry

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/paths"
)

type fakeTmuxTitleRenudger struct {
	titles   map[string]string
	readErrs map[string]error
	nudgeErr map[string]error
	nudges   map[string]string
}

func (fake *fakeTmuxTitleRenudger) ShowGlobalOption(
	_ context.Context,
	socket, _ string,
) (string, error) {
	return fake.titles[socket], fake.readErrs[socket]
}

func (fake *fakeTmuxTitleRenudger) NudgeTitlesString(
	_ context.Context,
	socket, value string,
) error {
	if err := fake.nudgeErr[socket]; err != nil {
		return err
	}
	fake.nudges[socket] = value
	return nil
}

func listenOnTestSocket(t *testing.T, directory, name string) {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close unix socket %s: %v", name, err)
		}
	})
}

func callTmuxTitleRenudge(
	t *testing.T,
	tmuxDir string,
	fake *fakeTmuxTitleRenudger,
) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	code := tmuxTitleRenudgeWith(
		nil,
		&stderr,
		config.Runtime{Paths: paths.Values{TmuxDir: tmuxDir}},
		fake,
	)
	return code, stderr.String()
}

func newFakeTmuxTitleRenudger() *fakeTmuxTitleRenudger {
	return &fakeTmuxTitleRenudger{
		titles:   make(map[string]string),
		readErrs: make(map[string]error),
		nudgeErr: make(map[string]error),
		nudges:   make(map[string]string),
	}
}

func TestTmuxTitleRenudgeMissingDirectoryIsSuccess(t *testing.T) {
	fake := newFakeTmuxTitleRenudger()
	code, stderr := callTmuxTitleRenudge(t, filepath.Join(t.TempDir(), "missing"), fake)
	if code != 0 || stderr != "" || len(fake.nudges) != 0 {
		t.Fatalf("missing directory: code=%d stderr=%q nudges=%v", code, stderr, fake.nudges)
	}
}

func TestTmuxTitleRenudgeUsesCanonicalValueAndIgnoresNonSockets(t *testing.T) {
	tmuxDir := t.TempDir()
	listenOnTestSocket(t, tmuxDir, "cc-live")
	if err := os.WriteFile(filepath.Join(tmuxDir, "not-a-socket"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := newFakeTmuxTitleRenudger()
	fake.titles["cc-live"] = "on"

	code, stderr := callTmuxTitleRenudge(t, tmuxDir, fake)
	if code != 0 || stderr != "" {
		t.Fatalf("enabled server: code=%d stderr=%q", code, stderr)
	}
	if len(fake.nudges) != 1 || fake.nudges["cc-live"] != config.TmuxTitlesString {
		t.Fatalf("nudges=%v, want cc-live=%q", fake.nudges, config.TmuxTitlesString)
	}
}

func TestTmuxTitleRenudgeAllHostOwnedReturnsThree(t *testing.T) {
	tmuxDir := t.TempDir()
	listenOnTestSocket(t, tmuxDir, "cc-host")
	fake := newFakeTmuxTitleRenudger()
	fake.titles["cc-host"] = "off"

	code, stderr := callTmuxTitleRenudge(t, tmuxDir, fake)
	if code != 3 ||
		!strings.Contains(stderr, "all 1 live servers") ||
		!strings.Contains(stderr, "host-owned titles") {
		t.Fatalf("host-owned server: code=%d stderr=%q", code, stderr)
	}
}

func TestTmuxTitleRenudgeSkipsStaleSocket(t *testing.T) {
	tmuxDir := t.TempDir()
	listenOnTestSocket(t, tmuxDir, "cc-stale")
	fake := newFakeTmuxTitleRenudger()
	fake.readErrs["cc-stale"] = errors.Join(errors.New("probe failed"), gather.ErrServerGone)

	code, stderr := callTmuxTitleRenudge(t, tmuxDir, fake)
	if code != 0 || stderr != "" || len(fake.nudges) != 0 {
		t.Fatalf("stale socket: code=%d stderr=%q nudges=%v", code, stderr, fake.nudges)
	}
}

func TestTmuxTitleRenudgeReportsRealFailureWithSocketContext(t *testing.T) {
	tmuxDir := t.TempDir()
	listenOnTestSocket(t, tmuxDir, "cx-broken")
	fake := newFakeTmuxTitleRenudger()
	fake.readErrs["cx-broken"] = errors.New("permission denied")

	code, stderr := callTmuxTitleRenudge(t, tmuxDir, fake)
	if code != 1 || !strings.Contains(stderr, "cx-broken") || !strings.Contains(stderr, "permission denied") {
		t.Fatalf("probe failure: code=%d stderr=%q", code, stderr)
	}
}

func TestTmuxTitleRenudgeReportsNudgeFailureWithSocketContext(t *testing.T) {
	tmuxDir := t.TempDir()
	listenOnTestSocket(t, tmuxDir, "cc-broken")
	fake := newFakeTmuxTitleRenudger()
	fake.titles["cc-broken"] = "on"
	fake.nudgeErr["cc-broken"] = errors.New("write failed")

	code, stderr := callTmuxTitleRenudge(t, tmuxDir, fake)
	if code != 1 || !strings.Contains(stderr, "cc-broken") || !strings.Contains(stderr, "write failed") {
		t.Fatalf("nudge failure: code=%d stderr=%q", code, stderr)
	}
}

func TestTmuxTitleRenudgeRejectsArgumentsAndNamesReadDirFailures(t *testing.T) {
	fake := newFakeTmuxTitleRenudger()
	var stderr bytes.Buffer
	runtime := config.Runtime{Paths: paths.Values{TmuxDir: t.TempDir()}}
	if code := tmuxTitleRenudgeWith([]string{"extra"}, &stderr, runtime, fake); code != 2 ||
		!strings.Contains(stderr.String(), "usage: pfm internal tmux-title-renudge") {
		t.Fatalf("positional argument: code=%d stderr=%q", code, stderr.String())
	}

	notDirectory := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDirectory, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, message := callTmuxTitleRenudge(t, notDirectory, fake)
	if code != 1 || !strings.Contains(message, "read tmux directory "+notDirectory) {
		t.Fatalf("read-dir failure: code=%d stderr=%q", code, message)
	}
}
