package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestReviewSplitRenameRetainsAmbiguityGuard(t *testing.T) {
	root := testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(values.TmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHAT_INJECT_POLL", "0.01")
	t.Setenv("CHAT_INJECT_ENTER_SETTLE", "0.02")
	t.Setenv("CHAT_INJECT_PROOF_SETTLE", "0.02")

	const renameUI = `import os, sys, tty
tty.setraw(0)
record = sys.argv[1]
buf = bytearray()
os.write(1, "❯ ".encode("utf-8"))
while True:
    ch = os.read(0, 1)
    if not ch:
        break
    if ch == b"\x13":
        continue
    if ch in (b"\r", b"\n"):
        if buf:
            with open(record, "ab") as stream:
                stream.write(bytes(buf) + b"\n")
            os.write(1, b"\r\nFIRED\r\n" + "❯ ".encode("utf-8"))
            buf.clear()
        continue
    buf.extend(ch)
    os.write(1, ch)
`
	script := filepath.Join(root, "rename-ui.py")
	if err := os.WriteFile(script, []byte(renameUI), 0o700); err != nil {
		t.Fatal(err)
	}

	type renameFixture struct {
		socketName string
		session    string
		panes      []string
		records    []string
		ids        []string
	}
	fixtureNumber := 0
	newFixture := func(t *testing.T, paneCount int, tmuxDirs ...string) renameFixture {
		t.Helper()
		fixtureNumber++
		fixture := renameFixture{
			socketName: fmt.Sprintf("cc-rename-review-%d", fixtureNumber),
			session:    fmt.Sprintf("rename-review-%d", fixtureNumber),
		}
		tmuxDir := values.TmuxDir
		if len(tmuxDirs) != 0 {
			tmuxDir = tmuxDirs[0]
		}
		if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
			t.Fatal(err)
		}
		socketPath := filepath.Join(tmuxDir, fixture.socketName)
		for index := 0; index < paneCount; index++ {
			id := fmt.Sprintf("%08d-1111-4111-8111-111111111111", fixtureNumber*10+index)
			record := filepath.Join(root, fmt.Sprintf("rename-%d-%d.log", fixtureNumber, index))
			seedClaudeChat(t, root, id, assistantSaid(fmt.Sprintf("pane %d", index)))
			fixture.ids = append(fixture.ids, id)
			fixture.records = append(fixture.records, record)
			var command *exec.Cmd
			if index == 0 {
				command = exec.Command(
					"tmux", "-S", socketPath, "-f", "/dev/null", "new-session", "-d",
					"-s", fixture.session, "python3", script, record,
				)
			} else {
				command = exec.Command(
					"tmux", "-S", socketPath, "split-window", "-d", "-t", fixture.session,
					"python3", script, record,
				)
			}
			if output, startErr := command.CombinedOutput(); startErr != nil {
				t.Fatalf("start rename pane %d: %v: %s", index, startErr, output)
			}
		}
		t.Cleanup(func() { _ = exec.Command("tmux", "-S", socketPath, "kill-server").Run() })
		output, err := exec.Command(
			"tmux", "-S", socketPath, "list-panes", "-t", fixture.session, "-F", "#{pane_id}",
		).Output()
		if err != nil {
			t.Fatal(err)
		}
		fixture.panes = strings.Fields(string(output))
		if len(fixture.panes) != paneCount {
			t.Fatalf("listed panes = %v, want %d", fixture.panes, paneCount)
		}
		for index, pane := range fixture.panes {
			transcript := filepath.Join(root, "claude", "project", fixture.ids[index]+".jsonl")
			if err := os.WriteFile(
				filepath.Join(values.SIDDir, fixture.socketName+"."+pane),
				[]byte(transcript+"\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(2 * time.Second)
		for _, pane := range fixture.panes {
			for {
				capture, captureErr := exec.Command(
					"tmux", "-S", socketPath, "capture-pane", "-p", "-t", pane,
				).Output()
				if captureErr == nil && strings.Contains(string(capture), "❯") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("rename pane %s never became ready: %v: %q", pane, captureErr, capture)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		return fixture
	}
	readRecord := func(t *testing.T, path string) string {
		t.Helper()
		content, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}

	t.Run("aggregate split refuses before delivery", func(t *testing.T) {
		fixture := newFixture(t, 2)
		code, detail, err := DeliverName(context.Background(), headless.Chat{
			Engine: "cc", Socket: fixture.socketName, Session: fixture.session, Live: true,
		}, "aggregate-name", nil)
		if err != nil || code != resolve.CodeAmbiguous {
			t.Errorf("DeliverName(aggregate) = code %d detail %q err %v, want ambiguity", code, detail, err)
		}
		if detail == "" {
			t.Error("DeliverName(aggregate) returned ambiguity without resolver detail")
		}
		for index, record := range fixture.records {
			if got := readRecord(t, record); got != "" {
				t.Errorf("aggregate rename reached pane %s: %q", fixture.panes[index], got)
			}
		}
	})

	t.Run("exact split pane receives rename alone", func(t *testing.T) {
		fixture := newFixture(t, 2)
		code, detail, err := DeliverName(context.Background(), headless.Chat{
			Engine: "cc", Socket: fixture.socketName, Session: fixture.session,
			Pane: fixture.panes[1], ID: fixture.ids[1], Live: true,
		}, "exact-name", nil)
		if err != nil || code != 0 {
			t.Fatalf("DeliverName(exact pane) = code %d detail %q err %v", code, detail, err)
		}
		if got := readRecord(t, fixture.records[0]); got != "" {
			t.Errorf("exact rename reached sibling pane %s: %q", fixture.panes[0], got)
		}
		if got := readRecord(t, fixture.records[1]); got != "/rename exact-name\n" {
			t.Errorf("exact pane record = %q, want one rename", got)
		}
	})

	t.Run("exact pane uses supplied runtime tmux directory", func(t *testing.T) {
		runtimeTmuxDir := filepath.Join(root, "runtime-tmux")
		fixture := newFixture(t, 1, runtimeTmuxDir)
		runtime := &pfmconfig.Runtime{Paths: values}
		runtime.Paths.TmuxDir = runtimeTmuxDir
		code, detail, err := DeliverName(context.Background(), headless.Chat{
			Engine: "cc", Socket: fixture.socketName, Session: fixture.session,
			Pane: fixture.panes[0], ID: fixture.ids[0], Live: true,
		}, "runtime-name", runtime)
		if err != nil || code != 0 {
			t.Fatalf("DeliverName(runtime pane) = code %d detail %q err %v", code, detail, err)
		}
		if got := readRecord(t, fixture.records[0]); got != "/rename runtime-name\n" {
			t.Errorf("runtime pane record = %q, want one rename", got)
		}
	})

	t.Run("ordinary unique socket still resolves", func(t *testing.T) {
		fixture := newFixture(t, 1)
		code, detail, err := DeliverName(context.Background(), headless.Chat{
			Engine: "cc", Socket: fixture.socketName, Session: fixture.session, Live: true,
		}, "ordinary-name", nil)
		if err != nil || code != 0 {
			t.Fatalf("DeliverName(unique socket) = code %d detail %q err %v", code, detail, err)
		}
		if got := readRecord(t, fixture.records[0]); got != "/rename ordinary-name\n" {
			t.Errorf("ordinary pane record = %q, want one rename", got)
		}
	})
}

func TestAmbientClaudeSplitSelfRetainsItsExactPane(t *testing.T) {
	root := testjail.Fleet(t)
	const (
		first  = "a1111111-1111-4111-8111-111111111111"
		second = "b2222222-2222-4222-8222-222222222222"
	)
	seedClaudeChat(t, root, first, assistantSaid("first"))
	seedClaudeChat(t, root, second, assistantSaid("second"))
	tmuxDir := filepath.Join(root, "tmux-"+fmt.Sprint(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	t.Setenv("TMUX_TMPDIR", root)
	socket := filepath.Join(tmuxDir, "cc-new-review-split")
	runTmux := func(args ...string) {
		t.Helper()
		command := exec.Command("tmux", append([]string{"-S", socket}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, output)
		}
	}
	runTmux("-f", "/dev/null", "new-session", "-d", "-s", "cc-new-review-split", "sleep 120")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	runTmux("split-window", "-d", "-t", "cc-new-review-split", "sleep 120")
	for pane, id := range map[string]string{"%0": first, "%1": second} {
		transcript := filepath.Join(root, "claude", "project", id+".jsonl")
		if err := os.WriteFile(
			filepath.Join(root, "sid", "cc-new-review-split."+pane),
			[]byte(transcript+"\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv(resolve.ClaudeSessionEnv, second)
	t.Setenv(resolve.CodexThreadEnv, "")
	self, err := Target(context.Background(), "self", nil)
	if err != nil {
		t.Fatal(err)
	}
	if self.ID != second || self.Socket != "cc-new-review-split" || self.Pane != "%1" || !self.Live {
		t.Fatalf("split self = %+v, want second transcript on exact pane %%1", self)
	}
}

func TestAmbientSelfWithoutExportedIDEnrichesFromItsExactSocket(t *testing.T) {
	root := testjail.Fleet(t)
	t.Setenv(resolve.ClaudeSessionEnv, "")
	t.Setenv(resolve.CodexThreadEnv, "")
	const id = "b1111111-1111-4111-8111-111111111111"
	seedClaudeChat(t, root, id, assistantSaid("answer"))
	tmuxDir := filepath.Join(root, "tmux-"+fmt.Sprint(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	t.Setenv("TMUX_TMPDIR", root)
	socket := filepath.Join(tmuxDir, "cc-new-review")
	command := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", "cc-new-review", "sleep 120",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	transcriptPath := filepath.Join(root, "claude", "project", id+".jsonl")
	if err := os.WriteFile(
		filepath.Join(root, "sid", "cc-new-review.%0"),
		[]byte(transcriptPath+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%0")
	self, err := Target(context.Background(), "self", nil)
	if err != nil || self.ID != id || self.Path != transcriptPath || self.Pane != "%0" {
		t.Fatalf("Target(self) = %+v err=%v, want enriched exact pane and transcript", self, err)
	}
}
