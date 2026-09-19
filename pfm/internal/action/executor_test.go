package action

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/obs"
)

type fakeActionTmux struct {
	mutex        sync.Mutex
	panes        map[string][]ActionPane
	alive        map[string]bool
	killedPanes  []string
	killedServer []string
	sized        []string
	selected     []string
	created      []ChatServer
}

func (tmux *fakeActionTmux) ListPanes(
	_ context.Context,
	socket string,
) ([]ActionPane, error) {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	if !tmux.alive[socket] {
		return nil, errors.New("dead socket")
	}
	return append([]ActionPane(nil), tmux.panes[socket]...), nil
}

func (tmux *fakeActionTmux) SocketAlive(
	_ context.Context,
	socket string,
) bool {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	return tmux.alive[socket]
}

func (tmux *fakeActionTmux) KillPane(
	_ context.Context,
	socket, paneID string,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.killedPanes = append(tmux.killedPanes, socket+"\t"+paneID)
	panes := tmux.panes[socket]
	kept := panes[:0]
	for _, pane := range panes {
		if pane.PaneID != paneID {
			kept = append(kept, pane)
		}
	}
	tmux.panes[socket] = kept
	if len(kept) == 0 {
		tmux.alive[socket] = false
	}
	return nil
}

func (tmux *fakeActionTmux) KillServer(
	_ context.Context,
	socket string,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.killedServer = append(tmux.killedServer, socket)
	tmux.alive[socket] = false
	delete(tmux.panes, socket)
	return nil
}

func (tmux *fakeActionTmux) SetWindowSizeLatest(
	_ context.Context,
	socket string,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.sized = append(tmux.sized, socket)
	return nil
}

func (tmux *fakeActionTmux) SelectWindow(
	_ context.Context,
	socket string,
	windowIndex int,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.selected = append(
		tmux.selected,
		socket+"\t"+string(rune('0'+windowIndex)),
	)
	return nil
}

func (tmux *fakeActionTmux) CreateChatServer(
	_ context.Context,
	server ChatServer,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.created = append(tmux.created, server)
	tmux.alive[server.Socket] = true
	return nil
}

type fakeProcesses struct {
	processes  []Process
	terminated []int
	mutex      sync.Mutex
}

func (processes *fakeProcesses) Processes(
	context.Context,
) ([]Process, error) {
	processes.mutex.Lock()
	defer processes.mutex.Unlock()
	return append([]Process(nil), processes.processes...), nil
}

func (processes *fakeProcesses) Terminate(pid int) error {
	processes.mutex.Lock()
	defer processes.mutex.Unlock()
	processes.terminated = append(processes.terminated, pid)
	return nil
}

type fixedGate bool

func (gate fixedGate) Confirm(
	context.Context,
	GateRequest,
) (bool, error) {
	return bool(gate), nil
}

type captureRunner struct {
	name string
	args []string
}

func (runner *captureRunner) Run(
	_ context.Context,
	name string,
	args ...string,
) error {
	runner.name = name
	runner.args = append([]string(nil), args...)
	return nil
}

func TestSoloReapsPaneAloneServerAndStrayWithKeepTTY(t *testing.T) {
	jailAction(t)
	id := "33333333-3333-4333-8333-333333333333"
	sidDir := os.Getenv("PFM_SID_DIR")
	for name, content := range map[string]string{
		"cc-100-1-1":    "/tx/" + id + ".jsonl",
		"cc-200-1-1.%2": "/tx/" + id + ".jsonl",
		"cc-300-1-1":    "/tx/" + id + ".jsonl",
		"cc-400-1-1":    "/tx/" + id + ".jsonl",
		"cc-500-1-1":    "/tx/" + id + ".jsonl",
	} {
		writeActionFile(t, filepath.Join(sidDir, name), content, 0o600)
	}
	tmux := &fakeActionTmux{
		alive: map[string]bool{
			"cc-100-1-1": true,
			"cc-200-1-1": true,
			"cc-300-1-1": true,
			"cc-400-1-1": true,
			// A successful probe with no panes is the cleanup case; a
			// failed probe is covered separately and must preserve its crumb.
			"cc-500-1-1": true,
		},
		panes: map[string][]ActionPane{
			"cc-100-1-1": {{PaneID: "%1", TTY: "pts/1"}},
			"cc-200-1-1": {
				{PaneID: "%2", TTY: "pts/2"},
				{PaneID: "%9", TTY: "pts/9"},
			},
			"cc-300-1-1": {{PaneID: "%3", TTY: "pts/3"}},
			"cc-400-1-1": {
				{PaneID: "%4", TTY: "pts/4"},
				{PaneID: "%5", TTY: "pts/5"},
			},
		},
	}
	processes := &fakeProcesses{processes: []Process{
		{PID: 10, Argv: []string{"claude", "--resume", id}, TTY: "pts/1"},
		{PID: 11, Argv: []string{"claude", "--resume", id}, TTY: "pts/8"},
		{PID: 12, Argv: []string{"claude", "--bg-pty-host", id}, TTY: "pts/8"},
		{PID: 13, Argv: []string{"node", id}, TTY: "pts/8"},
	}}
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: processes,
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Solo(
		context.Background(),
		id,
		"cc-100-1-1",
		false,
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		tmux.killedPanes,
		[]string{"cc-200-1-1\t%2"},
	) {
		t.Fatalf("killed panes = %q", tmux.killedPanes)
	}
	if !reflect.DeepEqual(tmux.killedServer, []string{"cc-300-1-1"}) {
		t.Fatalf("killed servers = %q", tmux.killedServer)
	}
	if !reflect.DeepEqual(processes.terminated, []int{11}) {
		t.Fatalf("terminated = %v", processes.terminated)
	}
	for _, name := range []string{
		"cc-200-1-1.%2",
		"cc-300-1-1",
		"cc-400-1-1",
		"cc-500-1-1",
	} {
		if _, err := os.Stat(filepath.Join(sidDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("crumb %s remains: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sidDir, "cc-100-1-1")); err != nil {
		t.Fatalf("keep crumb removed: %v", err)
	}
}

// A failed pane probe is not an empty server. The socket may still be live
// but temporarily unreadable; removing its crumb here makes the next sweep
// treat a working chat as unowned. Cleanup is safe only after a successful
// probe that returned no panes.
func TestSoloPreservesCrumbWhenPaneProbeFails(t *testing.T) {
	jailAction(t)
	id := "55555555-5555-4555-8555-555555555555"
	socket := "cc-900-1-1"
	crumb := filepath.Join(os.Getenv("PFM_SID_DIR"), socket)
	writeActionFile(t, crumb, "/tx/"+id+".jsonl", 0o600)

	tmux := &fakeActionTmux{alive: map[string]bool{socket: false}}
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Solo(context.Background(), id, "", true); err != nil {
		t.Fatalf("Solo() error = %v", err)
	}
	if _, err := os.Stat(crumb); err != nil {
		t.Fatalf("Solo() removed crumb after a failed pane probe: %v", err)
	}
	// L1-F14: the pane probe failure has a sibling 30 lines below (the
	// keep-socket probe) that already logs; this one was silent.
	if !strings.Contains(stderr.String(), socket) || !strings.Contains(stderr.String(), "dead socket") {
		t.Fatalf("Solo() did not log the failed pane probe: stderr=%q", stderr.String())
	}
}

// The keep-socket probe also gates the stray-Claude sweep. If it fails, the
// target's tty cannot be proven safe to exclude; killing every matching
// process would be worse than leaving strays for a later pass.
func TestSoloSkipsStraySweepWhenKeepSocketProbeFails(t *testing.T) {
	jailAction(t)
	id := "66666666-6666-4666-8666-666666666666"
	socket := "cc-901-1-1"
	tmux := &fakeActionTmux{alive: map[string]bool{socket: false}}
	processes := &fakeProcesses{processes: []Process{{
		PID: 42, Argv: []string{"claude", "--resume", id}, TTY: "pts/1",
	}}}
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: processes,
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Solo(context.Background(), id, socket, false); err != nil {
		t.Fatalf("Solo() error = %v", err)
	}
	if len(processes.terminated) != 0 {
		t.Fatalf("Solo() killed matching Claude after keep-socket probe failed: %v", processes.terminated)
	}
}

// TestSoloRecordsATransition: Solo is a multi-state coordinator over the
// state door — one comp=state record on completion, never the crumb or
// process content it walked.
func TestSoloRecordsATransition(t *testing.T) {
	jailAction(t)
	ctx, recorder := obs.Test(t)
	executor, err := New(Dependencies{
		Tmux:      &fakeActionTmux{alive: map[string]bool{}},
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Solo(ctx, "99999999-9999-4999-8999-999999999999", "", true); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "action" {
			continue
		}
		found = true
	}
	if !found {
		t.Fatalf("Solo() wrote no comp=state record: %s", recorder.Raw())
	}
}

// Open reaches Solo with no keep socket along two routes, but only a
// ResumeClaude row is destructive: it is creating a fresh replacement seat,
// so every matching Claude process is a competing stray. Agent rows describe
// a live agent seat and set liveAgent=true, which returns before the process
// sweep. Live rows use prepareLive and always pass their current socket.
func TestOpenEmptyKeepSetIsDestructiveOnlyForResumeClaude(t *testing.T) {
	for _, test := range []struct {
		name           string
		kind           compose.Kind
		wantTerminated []int
	}{
		{
			name:           "resume replaces every competing seat",
			kind:           compose.ResumeClaude,
			wantTerminated: []int{42},
		},
		{
			name: "live agent preserves its process",
			kind: compose.Agent,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			jailAction(t)
			id := "77777777-7777-4777-8777-777777777777"
			processes := &fakeProcesses{processes: []Process{
				{PID: 42, Argv: []string{"claude", "--resume", id}, TTY: "pts/1"},
				{PID: 43, Argv: []string{"claude", "--resume", "another-id"}, TTY: "pts/2"},
			}}
			executor, err := New(Dependencies{
				Tmux:      &fakeActionTmux{alive: map[string]bool{}},
				Processes: processes,
				Gate:      fixedGate(false),
				Runner:    &captureRunner{},
				Stderr:    io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			line, err := openWithTestConfig(executor, context.Background(), Request{
				Row: compose.Row{
					Kind: test.kind,
					ID:   id,
					CWD:  "/work/resume",
				},
				PrimaryAccount: 1,
				Home:           "/home/test",
				FreshSocket:    "cc-902-1-1",
			})
			if err != nil {
				t.Fatal(err)
			}
			if line == "" {
				t.Fatal("Open() returned no fresh-seat command")
			}
			if !reflect.DeepEqual(processes.terminated, test.wantTerminated) {
				t.Fatalf(
					"terminated = %v, want %v",
					processes.terminated,
					test.wantTerminated,
				)
			}
		})
	}
}

// TestOpenRecordsATransition: Open walks the state door through
// internal/obs (spec § Middleware, `state`) — requested to opened on a
// successful open, comp=state, never the pane content it opened.
func TestOpenRecordsATransition(t *testing.T) {
	jailAction(t)
	ctx, recorder := obs.Test(t)
	id := "88888888-8888-4888-8888-888888888888"
	executor, err := New(Dependencies{
		Tmux:      &fakeActionTmux{alive: map[string]bool{}},
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	line, err := openWithTestConfig(executor, ctx, Request{
		Row: compose.Row{
			Kind: compose.Agent,
			ID:   id,
			CWD:  "/work/agent",
		},
		PrimaryAccount: 1,
		Home:           "/home/test",
		FreshSocket:    "cc-950-1-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if line == "" {
		t.Fatal("Open() returned no line")
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "action" {
			continue
		}
		if next, _ := record.Field("next"); next == "opened" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Open() wrote no action->opened transition: %s", recorder.Raw())
	}
}

func TestExecutorGateSelfSwitchDeadFallbackAndCodexPrepare(t *testing.T) {
	jailAction(t)
	tmux := &fakeActionTmux{
		alive: map[string]bool{"cc-100-1-1": true},
		panes: map[string][]ActionPane{
			"cc-100-1-1": {
				{PaneID: "%1", WindowIndex: 0, CurrentCommand: "bash"},
				{PaneID: "%2", WindowIndex: 3, CurrentCommand: "claude"},
			},
		},
	}
	runner := &captureRunner{}
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(true),
		Runner:    runner,
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "44444444-4444-4444-8444-444444444444"
	request := Request{
		Row: compose.Row{
			Kind:        compose.LiveClaude,
			ID:          id,
			Socket:      "cc-100-1-1",
			SessionName: "live-session",
			Name:        "Live",
			Account:     2,
			C1H:         false,
		},
		PrimaryAccount: 1,
		Cache1H:        true,
		Home:           "/home/test",
		FreshSocket:    "cc-900-1-1",
	}
	line, err := openWithTestConfig(executor, context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if line != "TMUX= tmux -L 'cc-100-1-1' attach -t 'live-session'" {
		t.Fatalf("live line = %q", line)
	}
	if runner.name != "pfm" || !reflect.DeepEqual(runner.args, []string{
		"chat", "reload", "--sock", "cc-100-1-1", "1", "--1h", "1",
	}) {
		t.Fatalf("reload command = %q %q", runner.name, runner.args)
	}

	request.CurrentTMUX = "/tmp/jail/cc-100-1-1,1,0"
	line, err = openWithTestConfig(executor, context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if line != "" || !reflect.DeepEqual(
		tmux.selected,
		[]string{"cc-100-1-1\t3"},
	) {
		t.Fatalf("selfswitch line=%q selected=%q", line, tmux.selected)
	}

	request.CurrentTMUX = ""
	request.Row.Socket = "cc-404-1-1"
	request.Row.CWD = "/work/dead"
	line, err = openWithTestConfig(executor, context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	// The resume is born through the one chat-server creator; the line only
	// attaches to the server the executor created.
	if line != "TMUX= tmux -L 'cc-900-1-1' attach -t 'cc-900-1-1'" || len(tmux.created) == 0 {
		t.Fatalf("dead fallback line = %q created = %#v", line, tmux.created)
	}
	if born := tmux.created[len(tmux.created)-1]; born.Socket != "cc-900-1-1" || born.Window != "Claude" ||
		!stringsContainsAll(born.Run, "claude", "--resume") {
		t.Fatalf("dead fallback server = %#v", born)
	}

	codexRequest := Request{
		Row: compose.Row{
			Kind: compose.ResumeCodex,
			ID:   "55555555-5555-4555-8555-555555555555",
			CWD:  "/work/codex",
		},
		PrimaryAccount: 1,
		Home:           "/home/test",
		FreshSocket:    "cx-901-1-1",
	}
	line, err = openWithTestConfig(executor, context.Background(), codexRequest)
	if err != nil {
		t.Fatal(err)
	}
	// Two servers born so far: the dead Claude row's fresh resume above, and
	// this Codex resume.
	if line != "TMUX= tmux -L 'cx-901-1-1' attach -t 'cx-901-1-1'" ||
		len(tmux.created) != 2 ||
		tmux.created[1].Socket != "cx-901-1-1" {
		t.Fatalf("Codex line=%q created=%#v", line, tmux.created)
	}
}

func TestExecutorCodexWindowVerificationAndDeadFallback(t *testing.T) {
	jailAction(t)
	tmux := &fakeActionTmux{
		alive: map[string]bool{
			"cx-renamed": true,
			"cx-live":    true,
		},
		panes: map[string][]ActionPane{
			"cx-renamed": {{
				SessionName: "codex-session",
				WindowName:  "renamed-away",
			}},
			"cx-live": {{
				SessionName: "codex-session",
				WindowName:  "Expected",
			}},
		},
	}
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Row: compose.Row{
			Kind:        compose.LiveCodex,
			ID:          "codex-id",
			Socket:      "cx-renamed",
			SessionName: "codex-session",
			WindowName:  "Expected",
			CWD:         "/work/codex",
		},
		PrimaryAccount: 1,
		FreshSocket:    "cx-fresh",
		Home:           "/home/test",
	}
	line, err := openWithTestConfig(executor, context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if line != "TMUX= tmux -L 'cx-renamed' attach -t 'codex-session'" {
		t.Fatalf("renamed-window attach = %q", line)
	}

	request.Row.Socket = "cx-live"
	line, err = openWithTestConfig(executor, context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if line != "TMUX= tmux -L 'cx-live' attach -t 'codex-session:Expected'" {
		t.Fatalf("verified-window attach = %q", line)
	}

	request.Row.Socket = "cx-dead"
	line, err = openWithTestConfig(executor, context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if line != "TMUX= tmux -L 'cx-fresh' attach -t 'cx-fresh'" ||
		!strings.Contains(stderr.String(), "disappeared; resuming") {
		t.Fatalf("dead fallback line=%q stderr=%q", line, stderr.String())
	}
	if born := tmux.created[len(tmux.created)-1]; born.Socket != "cx-fresh" || born.Window != "Codex" {
		t.Fatalf("dead fallback server = %#v", born)
	}
}

// TestVerifiedCodexWindowLogsAListPanesFailure (L1-F19): a tmux failure here
// folds into the same "" a genuinely absent window gets — falling back to an
// unverified attach stays the conservative choice either way — but the
// probe failure itself must not vanish silently.
func TestVerifiedCodexWindowLogsAListPanesFailure(t *testing.T) {
	jailAction(t)
	ctx, recorder := obs.Test(t)
	tmux := &fakeActionTmux{alive: map[string]bool{"cx-unreadable": false}}
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := executor.verifiedCodexWindow(ctx, "cx-unreadable", "Expected")
	if got != "" {
		t.Fatalf("verifiedCodexWindow() = %q, want \"\" on a probe failure", got)
	}
	found := false
	for _, record := range recorder.Records() {
		if record.Message == "verify codex window: list panes failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no log record for the list-panes failure: %s", recorder.Raw())
	}
}

// TestSelfSwitchLogsAListPanesFailure (L1-F19): SelfSwitch already refuses to
// nest — the conservative outcome — on either a genuinely empty pane list or
// a probe failure; only the failure's own cause was silently dropped before.
func TestSelfSwitchLogsAListPanesFailure(t *testing.T) {
	jailAction(t)
	ctx, recorder := obs.Test(t)
	tmux := &fakeActionTmux{alive: map[string]bool{"cc-unreadable": false}}
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !executor.SelfSwitch(ctx, "/tmp/jail/cc-unreadable,1,0", "cc-unreadable") {
		t.Fatal("SelfSwitch() did not recognize its own socket")
	}
	if !strings.Contains(stderr.String(), "refusing to nest") {
		t.Fatalf("stderr = %q, want the refuse-to-nest message", stderr.String())
	}
	found := false
	for _, record := range recorder.Records() {
		if record.Message == "self-switch: list panes failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no log record for the list-panes failure: %s", recorder.Raw())
	}
}

// TestOpenDeadLiveSplitWithNoResumableIDHardErrors covers the one live kind
// Open's dead-socket branch cannot demote: a LiveSplit row IS the several
// chats sharing one server, so it carries no single chat id a demotion could
// resume as ResumeClaude/ResumeCodex — unlike a dead LiveClaude or LiveCodex
// row (TestExecutorGateSelfSwitchDeadFallbackAndCodexPrepare,
// TestExecutorCodexWindowVerificationAndDeadFallback), which always has one.
// Open must hard error instead of reaching the demotion code beneath the
// id check.
func TestOpenDeadLiveSplitWithNoResumableIDHardErrors(t *testing.T) {
	jailAction(t)
	tmux := &fakeActionTmux{alive: map[string]bool{}}
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Row: compose.Row{
			Kind:   compose.LiveSplit,
			ID:     "",
			Socket: "cc-dead-split-1-1-1",
		},
		PrimaryAccount: 1,
		Home:           "/home/test",
		FreshSocket:    "cc-900-1-1",
	}
	line, err := openWithTestConfig(executor, context.Background(), request)
	if line != "" {
		t.Fatalf("dead split line = %q, want empty", line)
	}
	wantErr := `live socket "cc-dead-split-1-1-1" died and its split row has no resumable id`
	if err == nil || err.Error() != wantErr {
		t.Fatalf("dead split error = %v, want %q", err, wantErr)
	}
	// No demotion: the "disappeared; resuming" message and every tmux side
	// effect belong only to the demotion branch just past the id check, which
	// a LiveSplit row with no id must never reach.
	if stderr.Len() != 0 {
		t.Fatalf("dead split with no id wrote stderr: %q", stderr.String())
	}
	if len(tmux.selected) != 0 || len(tmux.created) != 0 ||
		len(tmux.killedPanes) != 0 || len(tmux.killedServer) != 0 ||
		len(tmux.sized) != 0 {
		t.Fatalf("dead split with no id caused tmux side effects: %#v", tmux)
	}
}

func jailAction(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{
		filepath.Join(root, "sid"),
		filepath.Join(root, "tmux"),
		filepath.Join(root, "home"),
		filepath.Join(root, "claude"),
		filepath.Join(root, "codex"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "tmp"))
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "claude"))
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
	// A chat server loads the user's ~/.tmux.conf in real life; a fixture must
	// not, or the machine it runs on steers the test.
	t.Setenv("PFM_TMUX_CONF", "/dev/null")
	t.Setenv("PFM_HOME", filepath.Join(root, "home"))
	return root
}

func stringsContainsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !bytes.Contains([]byte(value), []byte(fragment)) {
			return false
		}
	}
	return true
}
