package hookentry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakePane is a pane in the fake Herdr's table: its id, the terminal that
// outlives a move, the foreground process pids pane.process_info reports, and
// the pane's own shell (0: none reported); kept names every process
// herdr-shield, the attach client of a pane herdr-shield holds.
type fakePane struct {
	id, terminal string
	pids         []int
	shell        int
	kept         bool
}

type fakeAgent struct{ name, pane string }

// fakeTab is a tab in the fake Herdr: the label its bar shows and how many
// panes it holds.
type fakeTab struct {
	label string
	panes int
}

// fakeHerdr is a Herdr socket: it records every request line and answers each
// from its pane table and agent list, with the configured error for a method
// (or for a rename to one name), and with pane_not_found (live Herdr 0.9.5's
// code) for a vacated pane id. As Herdr does, it serves ONE request per
// connection, then closes it.
type fakeHerdr struct {
	sessionResumeRefusals int
	path                  string
	mu                    sync.Mutex
	lines                 []string
	refuse                map[string]string
	refuseRename          map[string]string
	panes                 []fakePane
	vacated               map[string]bool
	aliases               map[string]string
	agents                []fakeAgent
	// tabOf is the tab agent.list names for an agent's pane; tabs answers
	// tab.get, and tab.rename relabels one.
	tabOf map[string]string
	tabs  map[string]fakeTab
}

func newFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	server := &fakeHerdr{
		path:         filepath.Join(testjail.ShortRoot(t), "h.sock"),
		refuse:       map[string]string{},
		refuseRename: map[string]string{},
		vacated:      map[string]bool{},
		tabOf:        map[string]string{},
		tabs:         map[string]fakeTab{},
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

// addKeptPane adds a pane herdr-shield holds: Herdr sees its attach client
// (pid client) as both shell and foreground.
func (server *fakeHerdr) addKeptPane(id, terminal string, client int) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.panes = append(
		server.panes,
		fakePane{id: id, terminal: terminal, pids: []int{client}, shell: client, kept: true},
	)
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
			TabID  string `json:"tab_id"`
			Label  string `json:"label"`
		} `json:"params"`
	}
	_ = json.Unmarshal([]byte(line), &request)
	server.mu.Lock()
	server.lines = append(server.lines, line)
	code, result := server.answer(request.Method, request.Params.PaneID, request.Params.Target, request.Params.Name)
	if request.Method == "pane.report_agent_session" && server.sessionResumeRefusals > 0 {
		server.sessionResumeRefusals--
		code = "resume_not_accepted"
	}
	if code == "" && strings.HasPrefix(request.Method, "tab.") {
		code, result = server.answerTab(request.Method, request.Params.TabID, request.Params.Label)
	}
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
	if canonical := server.aliases[pane]; canonical != "" {
		pane = canonical
	}
	for _, id := range []string{pane, target} {
		if id != "" && server.vacated[id] {
			return "pane_not_found", ""
		}
	}
	switch method {
	case "ping":
		return "", `{"type":"pong","version":"fixture","protocol":22}`
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
			TabID  string  `json:"tab_id,omitempty"`
		}
		agents := make([]listed, 0, len(server.agents))
		for _, agent := range server.agents {
			entry := listed{PaneID: agent.pane, TabID: server.tabOf[agent.pane]}
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
			name := "proc"
			if candidate.kept {
				name = herdr.ShieldBinary
			}
			for _, pid := range candidate.pids {
				foreground = append(foreground, map[string]any{"pid": pid, "name": name})
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

// answerTab is called with the lock held: tab.get answers the tab's
// tab_info, tab.rename relabels it, an unknown tab is tab_not_found.
func (server *fakeHerdr) answerTab(method, id, label string) (code, result string) {
	tab, ok := server.tabs[id]
	if !ok {
		return "tab_not_found", ""
	}
	if method == "tab.rename" {
		tab.label = label
		server.tabs[id] = tab
		return "", `{"type":"ok"}`
	}
	return "", mustJSON(map[string]any{"type": "tab_info", "tab": map[string]any{
		"tab_id": id, "label": tab.label, "pane_count": tab.panes, "workspace_id": "w1", "number": 1,
	}})
}

// putAgentInTab seats the agent of pane in tab, labelled label and holding
// panes panes.
func (server *fakeHerdr) putAgentInTab(pane, tab, label string, panes int) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.tabOf[pane] = tab
	server.tabs[tab] = fakeTab{label: label, panes: panes}
}

// tabLabel is the label tab's bar shows now.
func (server *fakeHerdr) tabLabel(tab string) string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.tabs[tab].label
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

// fakeTmux plays the one chat server a hook sees: its one session, the
// pid of the pane process, the attached client pids, the stored @pfm_herdr
// option and the chat's name.
type fakeTmux struct {
	mu       sync.Mutex
	session  string
	panePID  string
	stored   string
	chatName string
	// chatNameAuto is the server's herdr.ChatNameAutoOption.
	chatNameAuto string
	window       string
	clients      []string
	// panes are the server's pane ids list-panes -a answers; nil is the
	// chat's one pane, %0.
	panes  []string
	calls  []string
	failOn string
	// probeErr is what the pane-list door (deps.listPanes) answers instead of
	// the panes: a dead or unresponsive server.
	probeErr error
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
	case "list-sessions":
		return tmux.session + "\n", nil
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
		case herdr.ChatNameAutoOption:
			value := args[len(args)-1]
			if strings.HasSuffix(value, `\;`) {
				value = value[:len(value)-2] + ";"
			}
			tmux.chatNameAuto = value
		}
		return "", nil
	case "rename-window":
		tmux.window = args[3]
		return "", nil
	case "show-options":
		switch args[len(args)-1] {
		case herdr.ChatNameOption:
			return tmux.chatName + "\n", nil
		case herdr.ChatNameAutoOption:
			return tmux.chatNameAuto + "\n", nil
		}
		return tmux.stored + "\n", nil
	case "list-clients":
		return strings.Join(tmux.clients, "\n") + "\n", nil
	case "list-panes":
		if tmux.panes == nil {
			return "%0\n", nil
		}
		return strings.Join(tmux.panes, "\n") + "\n", nil
	}
	return "", fmt.Errorf("fake tmux: unexpected %v", args)
}

// listPanes plays gather.ProbePanes on the one chat server: the pane ids of
// list-panes, probeErr in their place, recorded as a list-panes call.
func (tmux *fakeTmux) listPanes(ctx context.Context, socket string) ([]gather.ProbePane, error) {
	tmux.mu.Lock()
	probeErr := tmux.probeErr
	tmux.mu.Unlock()
	if probeErr != nil {
		return nil, probeErr
	}
	listed, err := tmux.run(ctx, socket, "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		return nil, err
	}
	var panes []gather.ProbePane
	for _, id := range strings.Fields(listed) {
		panes = append(panes, gather.ProbePane{Socket: filepath.Base(socket), PaneID: id})
	}
	return panes, nil
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
	activityTmux *fakeTmux
	lockDir      string
	procs        map[int]fakeProc
	hookPID      int
	onPath       bool
	logFails     error
	// stateDir is the `pfm theme` state directory the reporter reads; empty
	// leaves the theme door out of the run.
	stateDir string
	// sidDir holds the reload pane mutexes the end event probes.
	sidDir string
	// spawn records the detached watch a blocked event starts; nil leaves the
	// watch door out of the run.
	spawn func(argv []string) error
	// paneEnv is each process's own environment; a pid it lacks cannot be read.
	paneEnv map[int]map[string]string
	// argv is each process's command line; a pid it lacks cannot be read.
	argv map[int][]string
	// procFails is each live process whose table entry cannot be read; a pid
	// procs lacks is a process that is gone.
	procFails map[int]error
	// answers is each process the pid probe still finds; a pid it lacks is gone.
	answers map[int]bool
}

// newHerdrJail lays out the usual chat: the engine (pid 50) is the pane's own
// process and the hook is a pfm child of it.
func newHerdrJail(t *testing.T) *herdrJail {
	t.Helper()
	return &herdrJail{
		lockDir: filepath.Join(testjail.ShortRoot(t), "lock"),
		sidDir:  t.TempDir(),
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
	deps := herdrDeps{
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
		sidDir:   jail.sidDir,
		spawn:    jail.spawn,
		logError: func(session string, at time.Time, message string) error {
			if jail.logFails != nil {
				return jail.logFails
			}
			return herdr.AppendError(jail.lockDir, session, at, message)
		},
		procInfo: func(pid int) (int, string, error) {
			if err := jail.procFails[pid]; err != nil {
				return 0, "", fmt.Errorf("fake process table cannot read pid %d: %w", pid, err)
			}
			proc, ok := jail.procs[pid]
			if !ok {
				return 0, "", fmt.Errorf("fake process table has no pid %d: %w", pid, fs.ErrNotExist)
			}
			return proc.parent, proc.command, nil
		},
		getpid: func() int { return jail.hookPID },
		processAlive: func(pid int) (bool, error) {
			return jail.answers[pid], nil
		},
		cmdline: func(pid int) ([]string, error) {
			argv, ok := jail.argv[pid]
			if !ok {
				return nil, fmt.Errorf("fake process table has no command line for pid %d", pid)
			}
			return argv, nil
		},
		environ: func(pid int) (map[string]string, error) {
			environment, ok := jail.paneEnv[pid]
			if !ok {
				return nil, fmt.Errorf("fake process table has no environment for pid %d", pid)
			}
			return environment, nil
		},
	}
	if jail.activityTmux != nil {
		deps.tmux = jail.activityTmux.run
		deps.listPanes = jail.activityTmux.listPanes
	}
	return deps
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
		deps.listPanes = tmux.listPanes
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

// This producer-faithful native fixture records mutations separately from proof reads.
func newNativeReporterHerdr(t *testing.T, seatID, pane, terminal string, beforeSend ...func()) *fakeHerdr {
	return newNativeReporterHerdrCount(t, seatID, pane, terminal, nil, nil, beforeSend...)
}

func newNativeReporterHerdrCount(
	t *testing.T,
	seatID, pane, terminal string,
	onProof func(),
	location func() (string, string),
	beforeSend ...func(),
) *fakeHerdr {
	t.Helper()
	root := testjail.ShortRoot(t)
	proc := filepath.Join(root, "proc")
	if err := os.MkdirAll(proc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(pid, parent int, argv string) {
		dir := filepath.Join(proc, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fields := make([]string, 24)
		for i := range fields {
			fields[i] = "0"
		}
		fields[0] = "S"
		fields[1] = strconv.Itoa(parent)
		fields[19] = "100"
		if err := os.WriteFile(
			filepath.Join(dir, "stat"),
			[]byte(fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " "))),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(900001, 1, "/fixture/pfm\x00internal\x00seat-host\x00"+seatID+"\x00")
	write(os.Getpid(), 900001, "/fixture/pfm\x00internal\x00herdr-state\x00working\x00")
	t.Setenv(paths.EnvProcRoot, proc)
	server := &fakeHerdr{path: filepath.Join(root, "h.sock")}
	listener, err := net.Listen("unix", server.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string                     `json:"id"`
					Method string                     `json:"method"`
					Params map[string]json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				var result any
				if (req.Method == "pane.list" || req.Method == "pane.process_info") && onProof != nil {
					onProof()
				}
				if location != nil {
					pane, terminal = location()
				}
				switch req.Method {
				case "pane.list":
					fields := testjail.HerdrPaneInfo(pane)["pane"].(map[string]any)
					fields["terminal_id"] = terminal
					result = map[string]any{"type": "pane_list", "panes": []any{fields}}
				case "pane.process_info":
					info := map[string]any{"pane_id": pane, "shell_pid": 900001}
					server.mu.Lock()
					for _, held := range server.panes {
						if held.kept && held.id == pane {
							// Herdr sees only herdr-shield's attach client in a held pane.
							info["shell_pid"] = held.shell
							info["foreground_processes"] = []any{
								map[string]any{
									"pid": held.shell, "name": herdr.ShieldBinary, "argv": []string{herdr.ShieldBinary},
								},
							}
						}
					}
					server.mu.Unlock()
					result = map[string]any{"type": "pane_process_info", "process_info": info}
				default:
					for _, check := range beforeSend {
						check()
					}
					line, _ := json.Marshal(req)
					server.mu.Lock()
					server.lines = append(server.lines, string(line))
					server.mu.Unlock()
					result = map[string]any{"type": "ok"}
					switch req.Method {
					case "pane.rename":
						result = map[string]any{"pane": map[string]any{"pane_id": pane, "label": req.Params["label"]}}
					case "agent.rename":
						result = map[string]any{"agent": map[string]any{"pane_id": pane, "name": req.Params["name"]}}
					case "pane.get":
						result = testjail.HerdrPaneInfo(pane)
					case "tab.get":
						result = map[string]any{
							"type": "tab_info",
							"tab":  map[string]any{"tab_id": "w6:t1", "label": "Previous", "pane_count": 1},
						}
					}
				}
				if err := json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
					t.Error(err)
				}
			}()
		}
	}()
	return server
}

// holdNativeReporterPane makes pane one herdr-shield holds: holder 900020 runs
// the shell 900021 the seat host 900001 runs under, whose terminal's
// foreground group is the seat host's, and Herdr sees attach client 900030.
func holdNativeReporterPane(t *testing.T, server *fakeHerdr, pane string) {
	t.Helper()
	proc := os.Getenv(paths.EnvProcRoot)
	for _, process := range []struct{ pid, parent, group, terminalGroup int }{
		{900020, 1, 900020, -1}, {900021, 900020, 900021, 900001}, {900001, 900021, 900001, 900001},
	} {
		fields := strings.Fields(strings.Repeat("0 ", 24))
		fields[0], fields[1], fields[2] = "S", strconv.Itoa(process.parent), strconv.Itoa(process.group)
		fields[5], fields[19] = strconv.Itoa(process.terminalGroup), "100"
		stat := fmt.Sprintf("%d (pfm) %s", process.pid, strings.Join(fields, " "))
		dir := filepath.Join(proc, strconv.Itoa(process.pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
			t.Fatal(err)
		}
		if process.pid != 900001 {
			if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("/fixture/sh\x00"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	testjail.StageHerdrShield(t, testjail.HerdrShieldListing(900020, 100, 900021, 100, pane, server.path), 0)
	server.addKeptPane(pane, "native", 900030)
}

func stampNativeReporterSeat(t *testing.T, seat *fleetdb.Seat) {
	t.Helper()
	if err := gather.StampHerdrHost(gather.NewProcFS(os.Getenv(paths.EnvProcRoot)), seat, 900001); err != nil {
		t.Fatal(err)
	}
	seat.HerdrTerminalID = "native-terminal"
	seat.HerdrWorkspace = "w6"
}
