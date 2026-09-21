package mockengine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

func TestJailBindingsAreWhatGatherAndReloadRead(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession})
	socket := filepath.Join(fix.root, "tmux-"+strconv.Itoa(os.Getuid()), "cc-1700000000-4242-7")
	session := fix.startTUI("claude", claudeArgs(), map[string]string{"TMUX": socket + ",4242,0", "TMUX_PANE": "%0"})
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })

	proc := gather.RealProcFS{Root: fix.procRoot}
	pid := os.Getpid()
	cmdline, err := proc.Cmdline(pid)
	if err != nil {
		t.Fatalf("gather could not read the mock's proc entry: %v", err)
	}
	if !gather.IsClaudeCommand(cmdline) || !strings.Contains(strings.Join(cmdline, " "), "--settings") {
		t.Fatalf("cmdline = %q, want a Claude argv gather.IsClaudeCommand accepts", cmdline)
	}
	stat, err := proc.Stat(pid)
	if err != nil || stat.ParentPID != os.Getppid() || stat.StartTime == 0 {
		t.Fatalf("stat = %+v err=%v, want the real parent pid and a start tick", stat, err)
	}
	environment, err := proc.Environ(pid)
	if err != nil || environment["CLAUDE_CONFIG_DIR"] != fix.configDir {
		t.Fatalf("environ = %v err=%v, want CLAUDE_CONFIG_DIR", environment, err)
	}
	id, transcriptPath, err := reload.SessionFromCrumb(fix.sidDir, "cc-1700000000-4242-7", "%0")
	if err != nil || id != fixtureSession || transcriptPath != fix.claudeTranscript(fixtureSession) {
		t.Fatalf("crumb = (%q, %q, %v), want the session and its transcript path", id, transcriptPath, err)
	}
	for _, name := range []string{"cc-1700000000-4242-7", "cc-1700000000-4242-7.%0"} {
		if _, err := os.Stat(filepath.Join(fix.sidDir, name)); err != nil {
			t.Fatalf("crumb %s missing: %v", name, err)
		}
	}

	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(fix.procRoot, strconv.Itoa(pid))); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the proc entry outlived the engine — a dead chat would keep looking alive")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCodexJailEntryLinksTheRolloutAsAnOpenDescriptor(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes})
	session := fix.startTUI("codex", codexArgs(), map[string]string{"TMUX": "/x/cx-1700000000-4242-8,1,0"})
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "›") })
	proc := gather.RealProcFS{Root: fix.procRoot}
	cmdline, err := proc.Cmdline(os.Getpid())
	if err != nil || !gather.IsCodexCommand(cmdline) {
		t.Fatalf("cmdline = %q err=%v, want a Codex argv", cmdline, err)
	}
	links, err := proc.FDLinks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	rollout := fix.rolloutPath()
	found := false
	for _, link := range links {
		found = found || link.Target == rollout
	}
	if !found {
		t.Fatalf(
			"fd links = %+v, want one open on %s (gather/codexproc.go reads the rollout off the descriptor table)",
			links,
			rollout,
		)
	}
}
