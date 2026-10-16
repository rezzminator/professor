package hookentry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakePane is a pane in the fake Herdr's table: its id, the terminal that
// outlives a move, the foreground process pids pane.process_info reports, and
// the pane's own shell (0: none reported).
type fakePane struct {
	id, terminal string
	pids         []int
	shell        int
}

type fakeAgent struct{ name, pane string }

// fakeHerdr is a Herdr socket: it records every request line and answers each
// from its pane table and agent list, with the configured error for a method
// (or for a rename to one name), and with pane_not_found (live Herdr 0.9.5's
// code) for a vacated pane id. As Herdr does, it serves ONE request per
// connection, then closes it.
type fakeHerdr struct {
	path         string
	mu           sync.Mutex
	lines        []string
	refuse       map[string]string
	refuseRename map[string]string
	panes        []fakePane
	vacated      map[string]bool
	agents       []fakeAgent
}

func newFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	server := &fakeHerdr{
		path:         filepath.Join(testjail.ShortRoot(t), "h.sock"),
		refuse:       map[string]string{},
		refuseRename: map[string]string{},
		vacated:      map[string]bool{},
	}
	listener, err := net.Listen("unix", server.path)
	if err != nil {
		t.Fatalf("listen fake herdr socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return server
}

func (server *fakeHerdr) addPane(id, terminal string, pids ...int) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.panes = append(server.panes, fakePane{id: id, terminal: terminal, pids: pids})
}

// addShellPane adds a pane whose own shell is pid shell.
func (server *fakeHerdr) addShellPane(id, terminal string, shell int, pids ...int) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.panes = append(server.panes, fakePane{id: id, terminal: terminal, pids: pids, shell: shell})
}

// runIn makes pid a foreground process of the pane with id.
func (server *fakeHerdr) runIn(id string, pid int) {
	server.mu.Lock()
	defer server.mu.Unlock()
	for index := range server.panes {
		if server.panes[index].id == id {
			server.panes[index].pids = append(server.panes[index].pids, pid)
		}
	}
}

// foreground replaces the foreground processes of the pane with id: a nested
// shell's next command takes the foreground from it.
func (server *fakeHerdr) foreground(id string, pids ...int) {
	server.mu.Lock()
	defer server.mu.Unlock()
	for index := range server.panes {
		if server.panes[index].id == id {
			server.panes[index].pids = pids
		}
	}
}

// move is Herdr's pane.move: the terminal keeps living under a new pane id and
// the old id answers pane_not_found.
func (server *fakeHerdr) move(oldID, newID string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	for index := range server.panes {
		if server.panes[index].id == oldID {
			server.panes[index].id = newID
		}
	}
	server.vacated[oldID] = true
}

// reassign gives a vacated pane id to another terminal.
func (server *fakeHerdr) reassign(id, terminal string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	delete(server.vacated, id)
	server.panes = append(server.panes, fakePane{id: id, terminal: terminal})
}

func (server *fakeHerdr) setAgents(agents ...fakeAgent) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.agents = agents
}

func (server *fakeHerdr) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimSuffix(line, "\n")
	var request struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params struct {
			PaneID string `json:"pane_id"`
			Target string `json:"target"`
			Name   string `json:"name"`
			State  string `json:"state"`
		} `json:"params"`
	}
	_ = json.Unmarshal([]byte(line), &request)
	server.mu.Lock()
	server.lines = append(server.lines, line)
	code, result := server.answer(request.Method, request.Params.PaneID, request.Params.Target, request.Params.Name)
	// refuse also takes "{method} {state}": one report state refused alone.
	if refused := server.refuse[request.Method+" "+request.Params.State]; request.Params.State != "" && refused != "" {
		code = refused
	}
	server.mu.Unlock()
	if code != "" {
		fmt.Fprintf(conn, `{"id":%q,"error":{"code":%q,"message":"refused by fake herdr"}}`+"\n", request.ID, code)
		return
	}
	fmt.Fprintf(conn, `{"id":%q,"result":%s}`+"\n", request.ID, result)
}

// answer is called with the lock held.
func (server *fakeHerdr) answer(method, pane, target, name string) (code, result string) {
	if refused := server.refuse[method]; refused != "" {
		return refused, ""
	}
	if refused := server.refuseRename[name]; method == "agent.rename" && refused != "" {
		return refused, ""
	}
	for _, id := range []string{pane, target} {
		if id != "" && server.vacated[id] {
			return "pane_not_found", ""
		}
	}
	switch method {
	case "pane.list":
		type listed struct {
			PaneID     string `json:"pane_id"`
			TerminalID string `json:"terminal_id"`
		}
		panes := make([]listed, 0, len(server.panes))
		for _, pane := range server.panes {
			panes = append(panes, listed{pane.id, pane.terminal})
		}
		return "", mustJSON(map[string]any{"type": "pane_list", "panes": panes})
	case "agent.list":
		type listed struct {
			Name   *string `json:"name"`
			PaneID string  `json:"pane_id"`
		}
		agents := make([]listed, 0, len(server.agents))
		for _, agent := range server.agents {
			entry := listed{PaneID: agent.pane}
			if agent.name != "" {
				entry.Name = &agent.name
			}
			agents = append(agents, entry)
		}
		return "", mustJSON(map[string]any{"type": "agent_list", "agents": agents})
	case "pane.process_info":
		for _, candidate := range server.panes {
			if candidate.id != pane {
				continue
			}
			foreground := make([]map[string]any, 0, len(candidate.pids))
			for _, pid := range candidate.pids {
				foreground = append(foreground, map[string]any{"pid": pid, "name": "proc"})
			}
			info := map[string]any{"pane_id": pane, "foreground_processes": foreground}
			if candidate.shell != 0 {
				info["shell_pid"] = candidate.shell
			}
			return "", mustJSON(map[string]any{"type": "pane_process_info", "process_info": info})
		}
		return "pane_not_found", ""
	}
	return "", `{"type":"ok"}`
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func (server *fakeHerdr) requests() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string(nil), server.lines...)
}

var seqSuffix = regexp.MustCompile(`,"seq":\d+`)

// calls are the recorded requests as "method params", the request id and the
// seq (both time-derived) left out.
func (server *fakeHerdr) calls() []string {
	var calls []string
	for _, line := range server.requests() {
		var request struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			calls = append(calls, "undecodable: "+line)
			continue
		}
		calls = append(calls, request.Method+" "+seqSuffix.ReplaceAllString(string(request.Params), ""))
	}
	return calls
}

// fakeTmux plays the one chat server a hook sees: its session environment, the
// pid of the pane process, the attached client pids, the stored @pfm_herdr
// option and the chat's name.
type fakeTmux struct {
	mu       sync.Mutex
	session  string
	panePID  string
	env      string
	stored   string
	chatName string
	window   string
	clients  []string
	calls    []string
	failOn   string
	// beforeWrite runs before a write of @pfm_herdr, outside the fake's lock.
	beforeWrite func(value string)
}

func newFakeTmux(session string) *fakeTmux {
	return &fakeTmux{session: session, panePID: "50"}
}

func (tmux *fakeTmux) run(_ context.Context, socket string, args ...string) (string, error) {
	if args[0] == "set-option" && args[len(args)-2] == herdrStoredOption && tmux.beforeWrite != nil {
		tmux.beforeWrite(args[len(args)-1])
	}
	tmux.mu.Lock()
	defer tmux.mu.Unlock()
	tmux.calls = append(tmux.calls, strings.Join(append([]string{"socket=" + socket}, args...), " "))
	// A standalone ";" chains commands in one tmux call, as tmux reads it.
	var output strings.Builder
	start := 0
	for index := 0; index <= len(args); index++ {
		if index < len(args) && args[index] != ";" {
			continue
		}
		answer, err := tmux.command(args[start:index])
		if err != nil {
			return "", err
		}
		output.WriteString(answer)
		start = index + 1
	}
	return output.String(), nil
}

// command answers one tmux command of a call; the caller holds the lock.
func (tmux *fakeTmux) command(args []string) (string, error) {
	if tmux.failOn != "" && args[0] == tmux.failOn {
		return "", fmt.Errorf("fake tmux %s failed", args[0])
	}
	switch args[0] {
	case "display-message":
		return tmux.session + "\t" + tmux.panePID + "\n", nil
	case "show-environment":
		return tmux.env, nil
	case "set-option":
		switch args[len(args)-2] {
		case herdrStoredOption:
			tmux.stored = args[len(args)-1]
		case herdr.ChatNameOption:
			// tmux removes the backslash that keeps a final ";" in the value.
			value := args[len(args)-1]
			if strings.HasSuffix(value, `\;`) {
				value = value[:len(value)-2] + ";"
			}
			tmux.chatName = value
		}
		return "", nil
	case "rename-window":
		tmux.window = args[3]
		return "", nil
	case "show-options":
		if args[len(args)-1] == herdr.ChatNameOption {
			return tmux.chatName + "\n", nil
		}
		return tmux.stored + "\n", nil
	case "list-clients":
		return strings.Join(tmux.clients, "\n") + "\n", nil
	}
	return "", fmt.Errorf("fake tmux: unexpected %v", args)
}

func (tmux *fakeTmux) setOptionCalls() []string {
	tmux.mu.Lock()
	defer tmux.mu.Unlock()
	var calls []string
	for _, call := range tmux.calls {
		if strings.Contains(call, " set-option ") {
			calls = append(calls, call)
		}
	}
	return calls
}

func (tmux *fakeTmux) allCalls() []string {
	tmux.mu.Lock()
	defer tmux.mu.Unlock()
	return append([]string(nil), tmux.calls...)
}

func (tmux *fakeTmux) storedState(t *testing.T) herdrStored {
	t.Helper()
	tmux.mu.Lock()
	defer tmux.mu.Unlock()
	var state herdrStored
	if tmux.stored == "" {
		return state
	}
	if err := json.Unmarshal([]byte(tmux.stored), &state); err != nil {
		t.Fatalf("decode stored @pfm_herdr %q: %v", tmux.stored, err)
	}
	return state
}

type fakeProc struct {
	parent  int
	command string
}

// herdrJail is everything outside the process a reporter reads: the lock and
// error-log directory, the process table with the hook's own pid, whether
// herdr is on PATH, and whether the error log can be written.
type herdrJail struct {
	lockDir  string
	procs    map[int]fakeProc
	hookPID  int
	onPath   bool
	logFails error
	// stateDir is the `pfm theme` state directory the reporter reads; empty
	// leaves the theme door out of the run.
	stateDir string
}

// newHerdrJail lays out the usual chat: the engine (pid 50) is the pane's own
// process and the hook is a pfm child of it.
func newHerdrJail(t *testing.T) *herdrJail {
	t.Helper()
	return &herdrJail{
		lockDir: filepath.Join(testjail.ShortRoot(t), "lock"),
		hookPID: 100,
		procs:   map[int]fakeProc{100: {50, "pfm"}, 50: {1, "claude"}},
	}
}

func (jail *herdrJail) logged(session string) string {
	data, err := os.ReadFile(filepath.Join(jail.lockDir, session+".log"))
	if err != nil {
		return ""
	}
	return string(data)
}

func (jail *herdrJail) lockFiles() []string {
	files, _ := filepath.Glob(filepath.Join(jail.lockDir, "*.lock"))
	return files
}

// deps are the reporter's seams over the jail: env as the environment, a
// clock that steps 1µs per read.
func (jail *herdrJail) deps(env map[string]string, clock *int64) herdrDeps {
	return herdrDeps{
		getenv: func(name string) string { return env[name] },
		now: func() time.Time {
			*clock += 1000
			return time.Unix(0, *clock)
		},
		lookPath: func(name string) (string, error) {
			if jail.onPath {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found on PATH")
		},
		lockDir:  jail.lockDir,
		stateDir: jail.stateDir,
		logError: func(session string, at time.Time, message string) error {
			if jail.logFails != nil {
				return jail.logFails
			}
			return herdr.AppendError(jail.lockDir, session, at, message)
		},
		procInfo: func(pid int) (int, string, error) {
			proc, ok := jail.procs[pid]
			if !ok {
				return 0, "", fmt.Errorf("fake process table has no pid %d", pid)
			}
			return proc.parent, proc.command, nil
		},
		getpid: func() int { return jail.hookPID },
	}
}

type herdrRun struct {
	rc     int
	stderr string
}

func runHerdr(t *testing.T, env map[string]string, tmux *fakeTmux, clock *int64, args []string, stdin string) herdrRun {
	t.Helper()
	return runHerdrIn(t, newHerdrJail(t), env, tmux, clock, args, stdin)
}

func runHerdrIn(
	t *testing.T,
	jail *herdrJail,
	env map[string]string,
	tmux *fakeTmux,
	clock *int64,
	args []string,
	stdin string,
) herdrRun {
	t.Helper()
	var stderr bytes.Buffer
	deps := jail.deps(env, clock)
	if tmux != nil {
		deps.tmux = tmux.run
	}
	rc := reportToHerdr(args, strings.NewReader(stdin), &stderr, deps)
	return herdrRun{rc: rc, stderr: stderr.String()}
}

func herdrPaneEnv(server *fakeHerdr, pane string) map[string]string {
	return map[string]string{
		"HERDR_ENV":         "1",
		"HERDR_PANE_ID":     pane,
		"HERDR_SOCKET_PATH": server.path,
		"HERDR_BIN_PATH":    "/usr/bin/herdr",
	}
}
