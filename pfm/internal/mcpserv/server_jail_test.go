//go:build jail

package mcpserv

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMCPHandshakeAndAllToolsOverJailedStdio(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	jail := newStdioJail(t)
	binary := buildFleetBinary(t)
	command := exec.Command(
		binary,
		"--config",
		writeEnabledMCPConfig(t, jail.root),
		"mcp",
		"serve",
		"--stdio",
		"--family",
		"chat",
	)
	command.Env = jail.environment()
	var serverStderr bytes.Buffer
	command.Stderr = &serverStderr
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "stdio-test",
		Version: "test",
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.CommandTransport{
		Command:           command,
		TerminateDuration: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("stdio handshake: %v\nstderr:\n%s", err, serverStderr.String())
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()

	var toolNames []string
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		toolNames = append(toolNames, tool.Name)
	}
	sort.Strings(toolNames)
	wantTools := ToolNames()
	sort.Strings(wantTools)
	if !reflect.DeepEqual(toolNames, wantTools) {
		t.Fatalf("tools = %q, want %q", toolNames, wantTools)
	}

	listed := callTool[LSOutput](t, session, "ls", LSInput{All: true})
	if listed.Count != 1 {
		t.Fatalf("ls = %+v", listed)
	}
	// The presence is the host's own (a dash without Herdr, else what its
	// Herdr says); the stdio path must carry one, whatever it reads.
	if listed.Rows[0].Herdr == "" {
		t.Fatalf("ls row = %+v, want a herdr presence string over stdio", listed.Rows[0])
	}
	wantRow := ChatRow{
		Herdr:   listed.Rows[0].Herdr,
		Session: "fixture-session",
		ID:      "fixture",
		Engine:  "cc",
		State:   "working",
		Dir:     "/work/fixture",
		Project: "fixture",
		Name:    "Fixture Chat",
		Account: 0,
		Kind:    "live-claude",
		Socket:  jail.socket,
		Pane:    jail.pane,
	}
	if !reflect.DeepEqual(listed.Rows[0], wantRow) {
		t.Fatalf("ls row = %+v, want %+v", listed.Rows[0], wantRow)
	}

	resolved := callTool[ResolveOutput](t, session, "resolve", ResolveInput{
		Kind: "label",
		Name: "Fixture Label",
	})
	if resolved.Status != "ok" ||
		resolved.Code != 0 ||
		resolved.SocketPath != filepath.Join(jail.tmuxDir, jail.socket) ||
		resolved.Pane != jail.pane {
		t.Fatalf("resolve = %+v", resolved)
	}

	captured := callTool[CaptureOutput](t, session, "capture", CaptureInput{
		Target:    "Fixture Label",
		TailLines: captureInt(10),
	})
	if captured.Status != "ok" ||
		!strings.Contains(captured.Text, "🔖 Fixture Label") ||
		!strings.Contains(captured.Text, "❯") {
		t.Fatalf("capture = %+v", captured)
	}

	called := callTool[CallOutput](t, session, "call", CallInput{
		Target:  "Fixture Label",
		Message: "hello fixture",
	})
	if called.Status != "delivered" ||
		called.Code != 0 ||
		!called.Typed ||
		!strings.Contains(called.Proof, "USER:hello fixture") {
		t.Fatalf("call = %+v\nstderr:\n%s", called, serverStderr.String())
	}
	after := callTool[CaptureOutput](t, session, "capture", CaptureInput{
		Target: "Fixture Label",
	})
	if !strings.Contains(after.Text, "USER:hello fixture") {
		t.Fatalf("post-call capture = %+v", after)
	}

	// A broadcast reports each target on its own; one refusal never hides
	// the delivery beside it.
	broadcast := callTool[CallOutput](t, session, "call", CallInput{
		Targets: []string{"Fixture Label", jail.selectorSession, "Fixture Label"},
		Message: "hello broadcast",
	})
	if broadcast.Status != "partial" || broadcast.Code != 6 || len(broadcast.Results) != 2 ||
		broadcast.Results[0].Target != "Fixture Label" || broadcast.Results[0].Status != "delivered" ||
		!strings.Contains(broadcast.Results[0].Proof, "USER:hello broadcast") ||
		broadcast.Results[1].Target != jail.selectorSession || broadcast.Results[1].Status != "refused" ||
		!strings.Contains(broadcast.Message, "broadcast delivered 1/2; not delivered: "+jail.selectorSession) {
		t.Fatalf("broadcast = %+v\nstderr:\n%s", broadcast, serverStderr.String())
	}

	selectorBefore := callTool[CaptureOutput](t, session, "capture",
		CaptureInput{Target: jail.selectorSession})
	selector := callTool[CallOutput](t, session, "call", CallInput{
		Target:  jail.selectorSession,
		Message: "must not type",
	})
	selectorAfter := callTool[CaptureOutput](t, session, "capture",
		CaptureInput{Target: jail.selectorSession})
	if selector.Code != 6 ||
		selector.Typed ||
		selector.Status != "refused" ||
		selectorBefore.Text != selectorAfter.Text ||
		strings.Contains(selectorAfter.Text, "MISTYPED") {
		t.Fatalf(
			"selector guard result=%+v before=%q after=%q",
			selector,
			selectorBefore.Text,
			selectorAfter.Text,
		)
	}

	busy := callTool[CallOutput](t, session, "call", CallInput{
		Target:  jail.busySession,
		Message: "wait until idle",
	})
	busyAfter := callTool[CaptureOutput](t, session, "capture",
		CaptureInput{Target: jail.busySession})
	if busy.Code != 0 || busy.Status != "queued" ||
		!busy.Typed || !busy.Busy ||
		!strings.Contains(busyAfter.Text, "QUEUED:wait until idle") ||
		strings.Contains(busyAfter.Text, "MISTYPED") ||
		strings.Contains(busyAfter.Text, "CONTROL-S-ERROR") {
		t.Fatalf(
			"busy queue result=%+v after=%q",
			busy,
			busyAfter.Text,
		)
	}

	numberedDraft := callTool[CallOutput](
		t,
		session,
		"call",
		CallInput{
			Target:  jail.codexSession,
			Message: " next",
		},
	)
	if numberedDraft.Code != 6 || numberedDraft.Typed ||
		!strings.Contains(numberedDraft.Message, "draft that will not stash") {
		t.Fatalf("numbered Codex draft result=%+v", numberedDraft)
	}
	codexLongWithDraft := callTool[CallOutput](
		t,
		session,
		"call",
		CallInput{
			Target:  jail.codexSession,
			Message: strings.Repeat("x", 1501),
		},
	)
	if codexLongWithDraft.Code != 6 ||
		codexLongWithDraft.Typed ||
		codexLongWithDraft.Status != "refused" ||
		!strings.Contains(codexLongWithDraft.Message, "draft that will not stash") {
		t.Fatalf("Codex long-body draft guard result=%+v", codexLongWithDraft)
	}

	// A megabyte body now reaches the pane byte-exact through bracketed
	// paste instead of being replaced by an auto-file pointer: no file is
	// spilled, and the receipt/proof say PASTE rather than AUTO-FILE. See
	// TestCallBodyAboveFormerAbsoluteCapUsesPaste for the byte-exact unit
	// coverage; this end-to-end fixture proves the same contract over a
	// real jailed MCP + tmux round trip.
	oversizeCommand := exec.Command(
		binary,
		"--config",
		writeEnabledMCPConfig(t, jail.root),
		"mcp",
		"serve",
		"--stdio",
		"--family",
		"chat",
	)
	oversizeCommand.Env = append(jail.environment(),
		"CHAT_CALL_ENTER_SETTLE=0.4",
		"CHAT_CALL_PROOF_SETTLE=0.5",
	)
	var oversizeStderr bytes.Buffer
	oversizeCommand.Stderr = &oversizeStderr
	oversizeClient := mcp.NewClient(&mcp.Implementation{
		Name:    "stdio-oversize-test",
		Version: "test",
	}, nil)
	oversizeCtx, oversizeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer oversizeCancel()
	oversizeSession, err := oversizeClient.Connect(oversizeCtx, &mcp.CommandTransport{
		Command:           oversizeCommand,
		TerminateDuration: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("oversize stdio handshake: %v\nstderr:\n%s", err, oversizeStderr.String())
	}
	defer func() {
		if err := oversizeSession.Close(); err != nil {
			t.Errorf("close oversize session: %v", err)
		}
	}()
	oversizeBody := strings.Repeat("x", 1<<20)
	oversize := callTool[CallOutput](t, oversizeSession, "call", CallInput{
		Target:  "Fixture Label",
		Message: oversizeBody,
	})
	if oversize.Code != 0 || !oversize.Typed || oversize.Status != "delivered" ||
		oversize.AutoFilePath != "" ||
		strings.Contains(oversize.Message, "AUTO-FILE") ||
		!strings.Contains(oversize.Message, "PASTE") ||
		!strings.Contains(oversize.Proof, strings.Repeat("x", 40)) {
		t.Fatalf("oversize call = %+v", oversize)
	}
}

func newStdioJail(t *testing.T) *stdioJail {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "ccfm")
	if err != nil {
		t.Fatal(err)
	}
	jail := &stdioJail{
		root:            root,
		home:            filepath.Join(root, "h"),
		claude:          filepath.Join(root, "c"),
		codex:           filepath.Join(root, "x"),
		sid:             filepath.Join(root, "s"),
		proc:            filepath.Join(root, "p"),
		tmuxBase:        filepath.Join(root, "t"),
		database:        filepath.Join(root, "d", "pfm-cache.db"),
		socket:          "cc-1700000000-1-1",
		session:         "fixture-session",
		selectorSocket:  "cc-1700000001-1-2",
		selectorSession: "selector-session",
		busySocket:      "cc-1700000002-1-3",
		busySession:     "busy-session",
		codexSocket:     "cx-1700000003-1-4",
		codexSession:    "codex-draft-session",
	}
	jail.tmuxDir = filepath.Join(
		jail.tmuxBase,
		"tmux-"+strconv.Itoa(os.Getuid()),
	)
	project := filepath.Join(jail.claude, "project")
	for _, directory := range []string{
		jail.home,
		project,
		jail.codex,
		jail.sid,
		jail.proc,
		jail.tmuxDir,
		filepath.Dir(jail.database),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	jail.transcript = filepath.Join(project, "fixture.jsonl")
	writeJSONL(t, jail.transcript, []any{
		map[string]any{
			"type":        "custom-title",
			"customTitle": "Fixture Chat",
		},
		map[string]any{
			"type": "user", "cwd": "/work/fixture",
			"message": map[string]any{"content": "secret middle prompt"},
		},
		map[string]any{
			"type": "assistant",
			"message": map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "Assistant visible"},
				map[string]any{"type": "tool_result", "content": "killed tool"},
			}},
		},
		map[string]any{
			"type": "user", "cwd": "/work/fixture",
			"message": map[string]any{"content": "[Request interrupted by user]"},
		},
		map[string]any{
			"type": "user", "cwd": "/work/fixture",
			"message": map[string]any{"content": "Final prompt"},
		},
	})
	uiPath := filepath.Join(root, "ui.py")
	ui := `import os, sys, tty
tty.setraw(0)
mode = sys.argv[1]
label = sys.argv[2]
marker = sys.argv[3]
initial = sys.argv[4]
sys.stdout.write("🔖 " + label + " │ 🥇\n")
if mode == "selector":
    sys.stdout.write("❯ 1. Allow\n  2. Deny\n")
elif mode == "busy":
    sys.stdout.write("Working (2s · 9 tokens)\n" + marker + " ")
else:
    sys.stdout.write(marker + " " + initial)
sys.stdout.flush()
buf = bytearray(initial.encode("utf-8"))
while True:
    ch = os.read(0, 1)
    if not ch:
        break
    if mode == "selector":
        sys.stdout.write("\nMISTYPED:" + repr(ch) + "\n")
        sys.stdout.flush()
        continue
    if mode == "busy" and ch == b"\x13":
        sys.stdout.write("\nCONTROL-S-ERROR\n")
        sys.stdout.flush()
        continue
    if ch == b"\x13":
        if marker == "❯":
            buf.clear()
            sys.stdout.write("\r\x1b[2K" + marker + " ")
            sys.stdout.flush()
    elif ch in (b"\r", b"\n"):
        if buf:
            text = bytes(buf).decode("utf-8", "replace")
            if mode == "busy":
                sys.stdout.write("\nQUEUED:" + text + "\nPress up to edit queued messages\n" + marker + " ")
            else:
                sys.stdout.write("\nUSER:" + text + "\n" + marker + " ")
            sys.stdout.flush()
            buf.clear()
    elif ch == b"\x1b":
        buf.clear()
        sys.stdout.write("\r\x1b[2K" + marker + " ")
        sys.stdout.flush()
    else:
        buf.extend(ch)
        sys.stdout.buffer.write(ch)
        sys.stdout.flush()
`
	if err := testjail.WriteExecutable(uiPath, []byte(ui), 0o700); err != nil {
		t.Fatal(err)
	}
	jail.startUI(t, jail.socket, jail.session, uiPath, "normal", "Fixture Label", "❯", "")
	jail.startUI(
		t,
		jail.selectorSocket,
		jail.selectorSession,
		uiPath,
		"selector",
		"Selector Label",
		"❯",
		"",
	)
	jail.startUI(
		t,
		jail.busySocket,
		jail.busySession,
		uiPath,
		"busy",
		"Busy Label",
		"❯",
		"",
	)
	jail.startUI(
		t,
		jail.codexSocket,
		jail.codexSession,
		uiPath,
		"normal",
		"Codex Draft",
		"›",
		"1. draft",
	)
	paneCommand := exec.Command(
		"tmux",
		"-L", jail.socket,
		"display-message",
		"-p",
		"-t", jail.session,
		"#{pane_id}",
	)
	paneCommand.Env = jail.environment()
	output, err := paneCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	jail.pane = strings.TrimSpace(string(output))
	if err := os.WriteFile(
		filepath.Join(jail.sid, jail.socket+"."+jail.pane),
		[]byte(jail.transcript),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, socket := range jail.sockets {
			kill := exec.Command("tmux", "-L", socket, "kill-server")
			kill.Env = jail.environment()
			_ = kill.Run()
		}
		_ = os.RemoveAll(root)
	})
	return jail
}

func buildFleetBinary(t *testing.T) string {
	t.Helper()
	fleetBinaryOnce.Do(func() {
		var err error
		fleetBinaryDir, err = os.MkdirTemp(paths.TempRoot(), "pfm-mcpserv-bin-")
		if err != nil {
			fleetBinaryErr = fmt.Errorf("create shared pfm build directory: %w", err)
			return
		}
		// RunInJail moved this package into the jail, so the source is the launch directory's module.
		fleetBinaryPath, fleetBinaryErr = testjail.PFMBinary(testjail.ModuleRoot(), fleetBinaryDir)
	})
	if fleetBinaryErr != nil {
		t.Fatal(fleetBinaryErr)
	}
	return fleetBinaryPath
}

func TestBuildFleetBinaryUsesPrebuiltPFM(t *testing.T) {
	if want := os.Getenv("TEST_EXPECT_FLEET_BINARY"); want != "" {
		if got := buildFleetBinary(t); got != want {
			t.Fatalf("buildFleetBinary() = %q, want prebuilt %q", got, want)
		}
		return
	}
	goDir := t.TempDir()
	if err := testjail.WriteExecutable(filepath.Join(goDir, "go"), []byte("#!/bin/sh\nexit 41\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	prebuilt := filepath.Join(t.TempDir(), "pfm")
	if err := testjail.WriteExecutable(prebuilt, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestBuildFleetBinaryUsesPrebuiltPFM$")
	command.Env = append(os.Environ(),
		"PATH="+goDir, paths.EnvTestPFMBinary+"="+prebuilt, "TEST_EXPECT_FLEET_BINARY="+prebuilt,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run with prebuilt pfm: %v\n%s", err, output)
	}
	if _, err := os.Stat(prebuilt); err != nil {
		t.Fatalf("mcpserv TestMain removed prebuilt binary %q: %v", prebuilt, err)
	}
}

func writeEnabledMCPConfig(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "mcp-enabled.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"version":1,"mcp":{"servers":{"chat":{"enabled":true}}}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return path
}
