package picker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	codexengine "github.com/rezzminator/professor/pfm/internal/engine/codex"
	opencodeengine "github.com/rezzminator/professor/pfm/internal/engine/opencode"
	"github.com/rezzminator/professor/pfm/internal/gather"
	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func init() {
	fleetindex.RegisterSource(pfmengine.Claude, claudeengine.Source{})
	fleetindex.RegisterSource(pfmengine.Codex, codexengine.Source{})
	fleetindex.RegisterSource(pfmengine.OpenCode, opencodeengine.Source{})
	gather.RegisterMatcher(pfmengine.Claude, claudeengine.Matcher{})
	gather.RegisterMatcher(pfmengine.Codex, codexengine.Matcher{})
	gather.RegisterMatcher(pfmengine.OpenCode, opencodeengine.Matcher{})
}

func jailTest(t *testing.T) string {
	t.Helper()
	return testjail.InstalledHome(t)
}

func startCodexStatusPane(t *testing.T, tmuxTmpDir, socket, statusLine string) {
	t.Helper()
	if err := os.MkdirAll(tmuxTmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"tmux",
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		socket,
		"-n",
		"codex",
		"printf '"+statusLine+"'; sleep 120",
	)
	command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jailed codex pane %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() {
		killCommand := exec.Command("tmux", "-L", socket, "kill-server")
		killCommand.Env = command.Env
		_ = killCommand.Run()
	})
	want := strings.TrimSpace(strings.TrimSuffix(statusLine, `\n`))
	waitForTmuxPaneText(t, socket, command.Env, want, 5*time.Second)
}

func waitForTmuxPaneText(t *testing.T, socket string, environment []string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		capture := exec.Command("tmux", "-L", socket, "capture-pane", "-p")
		capture.Env = environment
		output, err := capture.CombinedOutput()
		last = string(output)
		if err == nil && strings.Contains(last, want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("tmux pane did not paint %q within %s; last capture=%q", want, timeout, last)
}

func codexJailRollout(t *testing.T, database *store.Store, root, id string, promptCount int64) string {
	t.Helper()
	rolloutPath := filepath.Join(
		root,
		"codex",
		"sessions",
		"2030",
		"01",
		"02",
		"rollout-2030-01-02T03-04-05-"+id+".jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session_meta","payload":{"id":"` + id + `","thread_source":"user","cwd":"/work/example"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(
		context.Background(),
		store.Rollout{ID: id, Path: rolloutPath, CWD: "/work/example", UserThread: true, PromptCount: promptCount},
	); err != nil {
		t.Fatal(err)
	}
	return rolloutPath
}

func codexPane(socket, paneID string) gather.ProbePane {
	return gather.ProbePane{
		Socket:         socket,
		SessionName:    socket,
		WindowID:       "@1",
		PaneID:         paneID,
		CurrentCommand: "codex",
	}
}

type fakeProcessSpec struct {
	pid, parentPID int
	comm           string
	cmdline        []string
	environ        map[string]string
	withFD         bool
}

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
	stat := strconv.Itoa(
		spec.pid,
	) + " (" + spec.comm + ") S " + strconv.Itoa(
		spec.parentPID,
	) + " 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 20 0 100\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	if spec.withFD {
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}
