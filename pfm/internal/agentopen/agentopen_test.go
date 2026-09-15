package agentopen

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeCommands struct {
	mu          sync.Mutex
	rows        map[string][]byte
	queries     []string
	resumes     []string
	views       []string
	resumeErr   error
	queryErr    error
	entered     chan struct{}
	continueRun chan struct{}
}

func (c *fakeCommands) QueryAgents(_ context.Context, config string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, config)
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	if row, found := c.rows[config]; found {
		return row, nil
	}
	return []byte("[]"), nil
}

func (c *fakeCommands) Resume(_ context.Context, config, cwd, id string, _ bool) error {
	c.mu.Lock()
	c.resumes = append(c.resumes, config+"|"+cwd+"|"+id)
	entered, wait := c.entered, c.continueRun
	err := c.resumeErr
	c.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if wait != nil {
		<-wait
	}
	return err
}

func (c *fakeCommands) View(_ context.Context, config, cwd string) error {
	c.mu.Lock()
	c.views = append(c.views, config+"|"+cwd)
	c.mu.Unlock()
	return nil
}

type fakeProcesses struct {
	mu     sync.Mutex
	rows   []Process
	dead   map[int]bool
	term   []int
	parent string
}

func (p *fakeProcesses) Processes(context.Context) ([]Process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Process(nil), p.rows...), nil
}

func (p *fakeProcesses) Alive(pid int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.dead[pid]
}

func (p *fakeProcesses) Terminate(pid int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.term = append(p.term, pid)
	p.dead[pid] = true
	return nil
}

func (p *fakeProcesses) Kill(pid int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dead[pid] = true
	return nil
}
func (p *fakeProcesses) ParentComm(int) string { return p.parent }

type fakeTmux struct{ socket string }

func (t fakeTmux) SocketForPID(context.Context, int) (string, error) { return t.socket, nil }
func (t fakeTmux) Attach(context.Context, string) error              { return nil }

func newTestOpener(t *testing.T, commands Commands, processes Processes, tmux Tmux) *Opener {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	return New(Dependencies{
		SIDDir: filepath.Join(t.TempDir(), "sid"), Home: home,
		Accounts: []Account{
			{ID: 1},
			{ID: 2, ConfigDir: filepath.Join(home, ".cc", "2")},
			{ID: 3, ConfigDir: filepath.Join(home, ".cc", "3")},
		},
		Commands: commands, Processes: processes, Tmux: tmux, Stderr: &bytes.Buffer{},
		GracePeriod: 10 * time.Millisecond, PollInterval: time.Millisecond,
	})
}

func TestNewDoesNotInventAnAccountRoster(t *testing.T) {
	opener := New(Dependencies{
		Home: t.TempDir(), Commands: &fakeCommands{},
		Processes: &fakeProcesses{}, Tmux: fakeTmux{},
	})
	if len(opener.accounts) != 0 {
		t.Fatalf("New() accounts=%#v, want an empty config-owned roster", opener.accounts)
	}
	err := opener.Open(context.Background(), Request{ID: "fixture", CWD: "/work"})
	if err == nil || !strings.Contains(err.Error(), "configured account roster is empty") {
		t.Fatalf("Open() error=%v, want empty-roster failure", err)
	}
}

func TestOpenStaleIdleRegistryRowResumesCurrentPrimary(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	commands := &fakeCommands{
		rows: map[string][]byte{"": []byte(`[{"sessionId":"` + id + `","name":"worker","pid":42,"status":"idle"}]`)},
	}
	processes := &fakeProcesses{
		rows: []Process{{PID: 42, Argv: []string{"claude", "--resume", id}}},
		dead: map[int]bool{42: true},
	}
	opener := newTestOpener(t, commands, processes, fakeTmux{})
	if err := opener.Open(context.Background(), Request{ID: id, CWD: "/jail/project", PrimaryAccount: 2}); err != nil {
		t.Fatal(err)
	}
	if len(processes.term) != 0 {
		t.Fatalf("term=%v", processes.term)
	}
	if got := commands.resumes; len(got) != 1 || got[0] != filepath.Join(opener.home, ".cc", "2")+"|/jail/project|"+id {
		t.Fatalf("resumes=%v", got)
	}
}

func TestOpenStaleBusyRegistryRowOpensAgentViewWithoutResume(t *testing.T) {
	id := "22222222-2222-4222-8222-222222222222"
	commands := &fakeCommands{
		rows: map[string][]byte{"": []byte(`[{"sessionId":"` + id + `","name":"busy","pid":43,"status":"busy"}]`)},
	}
	processes := &fakeProcesses{
		rows: []Process{{PID: 43, Argv: []string{"claude", "--resume", id}}},
		dead: map[int]bool{43: true},
	}
	tmux := fakeTmux{}
	opener := newTestOpener(t, commands, processes, tmux)
	if err := opener.Open(context.Background(), Request{ID: id, CWD: "/jail"}); err != nil {
		t.Fatal(err)
	}
	if len(commands.views) != 1 || commands.views[0] != "|/jail" || len(commands.resumes) != 0 {
		t.Fatalf("commands resume=%v view=%v", commands.resumes, commands.views)
	}
}

func TestOpenTmuxResidentAgentAttaches(t *testing.T) {
	id := "44444444-4444-4444-8444-444444444444"
	commands := &fakeCommands{
		rows: map[string][]byte{"": []byte(`[{"sessionId":"` + id + `","name":"resident","pid":45,"status":"idle"}]`)},
	}
	processes := &fakeProcesses{
		rows: []Process{{PID: 45, Argv: []string{"claude", "--resume", id}}},
		dead: map[int]bool{},
	}
	opener := newTestOpener(t, commands, processes, fakeTmux{socket: "cc-probe-1"})
	if err := opener.Open(context.Background(), Request{ID: id, CWD: "/jail"}); err != nil {
		t.Fatal(err)
	}
	if len(commands.resumes) != 0 || len(commands.views) != 0 {
		t.Fatalf("commands resume=%v view=%v", commands.resumes, commands.views)
	}
}

func TestOpenLiveAgentOutsidePFMReturnsOneActionableLine(t *testing.T) {
	id := "66666666-6666-4666-8666-666666666666"
	commands := &fakeCommands{
		rows: map[string][]byte{
			"": []byte(`[{"sessionId":"` + id + `","name":"rough-seas","pid":46,"status":"busy"}]`),
		},
	}
	processes := &fakeProcesses{
		rows: []Process{{PID: 46, Argv: []string{"claude", "--resume", id}}},
		dead: map[int]bool{}, parent: "codex-app-server",
	}
	var stderr bytes.Buffer
	home := t.TempDir()
	opener := New(Dependencies{
		SIDDir: filepath.Join(home, "sid"), Home: home,
		Accounts: []Account{{ID: 1}},
		Commands: commands, Processes: processes, Tmux: fakeTmux{}, Stderr: &stderr,
	})
	err := opener.Open(context.Background(), Request{ID: id, CWD: "/jail"})
	want := "⚙ rough-seas: running outside pfm (pid 46, parent codex-app-server) — no pane to attach; kill 46 and open the row to resume it in a pane"
	if err == nil || err.Error() != want {
		t.Fatalf("Open() error=%q, want %q", err, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("outside-pfm path emitted extra lines: %q", stderr.String())
	}
	if len(commands.views) != 0 || len(commands.resumes) != 0 || len(processes.term) != 0 {
		t.Fatalf(
			"outside-pfm path mutated process: views=%v resumes=%v term=%v",
			commands.views,
			commands.resumes,
			processes.term,
		)
	}
}

func TestSocketForPIDSuppressesPerSocketProbeFailures(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"cc-dead-one", "cc-dead-two"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	tmux := RealTmux{Binary: binary, Dir: directory, Stderr: &stderr}
	socket, err := tmux.SocketForPID(context.Background(), 46)
	if err != nil || socket != "" {
		t.Fatalf("SocketForPID() socket=%q err=%v", socket, err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("dead socket probes leaked to operator: %q", stderr.String())
	}
}

func TestOpenPerUUIDMutexHasOneWinner(t *testing.T) {
	id := "33333333-3333-4333-8333-333333333333"
	entered := make(chan struct{}, 1)
	commands := &fakeCommands{rows: map[string][]byte{}, entered: entered, continueRun: make(chan struct{})}
	processes := &fakeProcesses{dead: map[int]bool{}}
	first := newTestOpener(t, commands, processes, fakeTmux{})
	second := &Opener{
		sidDir:       first.sidDir,
		home:         first.home,
		commands:     commands,
		processes:    processes,
		tmux:         fakeTmux{},
		stderr:       &bytes.Buffer{},
		gracePeriod:  time.Millisecond,
		pollInterval: time.Millisecond,
	}
	results := make(chan error, 2)
	go func() { results <- first.Open(context.Background(), Request{ID: id}) }()
	<-entered
	go func() { results <- second.Open(context.Background(), Request{ID: id}) }()
	var resultsSeen []error
	closed := false
	for len(resultsSeen) < 2 {
		select {
		case err := <-results:
			resultsSeen = append(resultsSeen, err)
			if errors.Is(err, ErrBusy) && !closed {
				close(commands.continueRun)
				closed = true
			}
		case <-time.After(time.Second):
			t.Fatal("mutex contenders did not settle")
		}
	}
	if !closed {
		close(commands.continueRun)
	}
	busy := false
	other := false
	for _, err := range resultsSeen {
		if errors.Is(err, ErrBusy) {
			busy = true
		}
		if err != nil && !errors.Is(err, ErrBusy) {
			other = true
		}
	}
	if !busy || other {
		t.Fatalf("mutex results busy=%v other=%v", busy, other)
	}
}

func TestParseAgentsRejectsMalformedRegistry(t *testing.T) {
	if _, err := ParseAgents([]byte("not-json")); err == nil {
		t.Fatal("malformed registry parsed successfully")
	}
}

func TestOpenRefusesFreshResumeWhenEveryRegistryQueryFails(t *testing.T) {
	id := "55555555-5555-4555-8555-555555555555"
	commands := &fakeCommands{
		rows:     map[string][]byte{},
		queryErr: errors.New("registry unavailable"),
	}
	processes := &fakeProcesses{dead: map[int]bool{}}
	opener := newTestOpener(t, commands, processes, fakeTmux{})
	err := opener.Open(context.Background(), Request{
		ID:             id,
		CWD:            "/jail/project",
		PrimaryAccount: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "no agent registry answered") {
		t.Fatalf("registry failure error = %v", err)
	}
	if len(commands.resumes) != 0 {
		t.Fatalf("registry failure was treated as absence; resumes=%v", commands.resumes)
	}
}
