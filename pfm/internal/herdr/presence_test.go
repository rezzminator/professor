package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// herdrFake is a Herdr socket that answers one request per connection (the
// real contract) from a method-to-result table and counts the requests.
type herdrFake struct {
	path   string
	mu     sync.Mutex
	counts map[string]int
}

func (fake *herdrFake) count(method string) int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.counts[method]
}

func (fake *herdrFake) total() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	sum := 0
	for _, n := range fake.counts {
		sum += n
	}
	return sum
}

// serveHerdr answers each method with its table entry: a result object, or
// with the "error:" prefix an error response whose code follows.
func serveHerdr(t *testing.T, table map[string]string) *herdrFake {
	t.Helper()
	fake := &herdrFake{path: filepath.Join(t.TempDir(), "h.sock"), counts: map[string]int{}}
	listener, err := net.Listen("unix", fake.path)
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
			go func() {
				defer func() { _ = conn.Close() }()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var asked struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if err := json.Unmarshal(line, &asked); err != nil {
					return
				}
				fake.mu.Lock()
				fake.counts[asked.Method]++
				fake.mu.Unlock()
				answer := table[asked.Method]
				if code, isError := strings.CutPrefix(answer, "error:"); isError {
					_, _ = conn.Write(
						[]byte(`{"id":"` + asked.ID + `","error":{"code":"` + code + `","message":"no"}}` + "\n"),
					)
					return
				}
				_, _ = conn.Write([]byte(`{"id":"` + asked.ID + `","result":` + answer + "}\n"))
			}()
		}
	}()
	return fake
}

const (
	panesJSON = `{"type":"pane_list","panes":[` +
		`{"pane_id":"w6:p2","terminal_id":"term_a","workspace_id":"w6"},` +
		`{"pane_id":"wA:p1","terminal_id":"term_b","workspace_id":"wA"},` +
		`{"pane_id":"wA:p4","terminal_id":"term_c","workspace_id":"wA"}]}`
	agentsJSON = `{"type":"agent_list","agents":[` +
		`{"pane_id":"w6:p2","workspace_id":"w6","agent":"claude","agent_status":"idle","name":"pfm-1"},` +
		`{"pane_id":"wA:p1","workspace_id":"wA","agent":"codex","agent_status":"working","name":null}]}`
	workspacesJSON = `{"type":"workspace_list","workspaces":[` +
		`{"workspace_id":"w6","label":".professor"},{"workspace_id":"wA","label":""}]}`
)

func healthyHerdr(t *testing.T) *herdrFake {
	t.Helper()
	return serveHerdr(t, map[string]string{
		"pane.list": panesJSON, "agent.list": agentsJSON, "workspace.list": workspacesJSON,
	})
}

// fakeTmux answers show-options per session with its stored JSON (or error)
// and counts every call.
type fakeTmux struct {
	stored map[string]string
	errs   map[string]error
	calls  int
}

func (tmux *fakeTmux) run(_ context.Context, socketPath string, args ...string) (string, error) {
	tmux.calls++
	session := args[len(args)-2]
	if err := tmux.errs[session]; err != nil {
		return "", err
	}
	return tmux.stored[socketPath+"|"+session], nil
}

func storedWith(clients map[string]Client) string {
	encoded, err := json.Marshal(Stored{Engine: "claude", SessionID: "s", State: "idle", Clients: clients})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func probeFor(tmux *fakeTmux, withHerdr bool, warn *strings.Builder) Probe {
	probe := Probe{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Tmux:     tmux.run,
		Call:     Call,
		Warn:     warn,
	}
	if withHerdr {
		probe.Getenv = func(name string) string {
			if name == EnvBin {
				return "/bin/herdr"
			}
			return ""
		}
	}
	return probe
}

func TestPresenceOutsideHerdrIsADashWithNoTmuxOrSocketCall(t *testing.T) {
	fake := healthyHerdr(t)
	tmux := &fakeTmux{stored: map[string]string{
		"/t/cc-1|s1": storedWith(map[string]Client{"1": {Pane: "w6:p2", Terminal: "term_a", Socket: fake.path}}),
	}}
	var warn strings.Builder
	seats := []Seat{{SocketPath: "/t/cc-1", Session: "s1"}, {}, {SocketPath: "/t/cc-2", Session: "s2"}}
	got := probeFor(tmux, false, &warn).Presence(context.Background(), seats)
	if want := []string{
		PresenceNoHerdr,
		PresenceNoHerdr,
		PresenceNoHerdr,
	}; strings.Join(
		got,
		"|",
	) != strings.Join(
		want,
		"|",
	) {
		t.Fatalf("Presence outside Herdr = %q, want %q", got, want)
	}
	if tmux.calls != 0 || fake.total() != 0 {
		t.Fatalf("outside Herdr: %d tmux calls and %d socket requests, want none of either", tmux.calls, fake.total())
	}
	if warn.Len() != 0 {
		t.Fatalf("outside Herdr logged %q, want nothing", warn.String())
	}
}

func TestPresenceFindsHerdrOnPathWhenNoBinEnv(t *testing.T) {
	tmux := &fakeTmux{}
	probe := probeFor(tmux, false, nil)
	probe.LookPath = func(name string) (string, error) {
		if name != "herdr" {
			t.Errorf("LookPath(%q), want herdr", name)
		}
		return "/usr/bin/herdr", nil
	}
	got := probe.Presence(context.Background(), []Seat{{}})
	if got[0] != PresenceNotInHerdr {
		t.Fatalf("Presence with herdr on PATH = %q, want %q for a seat with no tmux server", got[0], PresenceNotInHerdr)
	}
}

func TestPresenceReadsPaneWorkspaceStatusAndNameByTerminal(t *testing.T) {
	fake := healthyHerdr(t)
	tmux := &fakeTmux{stored: map[string]string{
		// The stored pane id is stale: the terminal moved to w6:p2.
		"/t/cc-1|s1": storedWith(map[string]Client{"1": {Pane: "w1:p9", Terminal: "term_a", Socket: fake.path}}),
		// An unnamed agent in a workspace with no label reads its workspace id.
		"/t/cc-2|s2": storedWith(map[string]Client{"2": {Pane: "wA:p1", Terminal: "term_b", Socket: fake.path}}),
		// A pane with no agent entry says so.
		"/t/cc-3|s3": storedWith(map[string]Client{"3": {Pane: "wA:p4", Terminal: "term_c", Socket: fake.path}}),
		// No terminal recorded: the stored pane id is the key.
		"/t/cc-4|s4": storedWith(map[string]Client{"4": {Pane: "w6:p2", Socket: fake.path}}),
	}}
	seats := []Seat{
		{SocketPath: "/t/cc-1", Session: "s1"},
		{SocketPath: "/t/cc-2", Session: "s2"},
		{SocketPath: "/t/cc-3", Session: "s3"},
		{SocketPath: "/t/cc-4", Session: "s4"},
	}
	got := probeFor(tmux, true, nil).Presence(context.Background(), seats)
	want := []string{
		"w6:p2 .professor idle pfm-1",
		"wA:p1 wA working",
		"wA:p4 wA no-agent",
		"w6:p2 .professor idle pfm-1",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Presence = %q, want %q", got, want)
	}
}

func TestPresenceAsksEachHerdrSocketOnceForAllSeats(t *testing.T) {
	fake := healthyHerdr(t)
	clients := map[string]Client{"1": {Pane: "w6:p2", Terminal: "term_a", Socket: fake.path}}
	tmux := &fakeTmux{stored: map[string]string{
		"/t/cc-1|s1": storedWith(clients), "/t/cc-2|s2": storedWith(clients), "/t/cc-3|s3": storedWith(clients),
	}}
	seats := []Seat{
		{
			SocketPath: "/t/cc-1",
			Session:    "s1",
		},
		{SocketPath: "/t/cc-2", Session: "s2"},
		{SocketPath: "/t/cc-3", Session: "s3"},
	}
	probeFor(tmux, true, nil).Presence(context.Background(), seats)
	for _, method := range []string{"pane.list", "agent.list", "workspace.list"} {
		if got := fake.count(method); got != 1 {
			t.Errorf("%s requests for 3 seats on one Herdr socket = %d, want 1", method, got)
		}
	}
}

func TestPresenceNotInHerdrWhenNoServerNoOptionNoClientOrNoLivePane(t *testing.T) {
	fake := healthyHerdr(t)
	tmux := &fakeTmux{stored: map[string]string{
		"/t/cc-2|s2": "", // option never set
		"/t/cc-3|s3": storedWith(nil),
		// the only registered client's terminal is gone from Herdr
		"/t/cc-4|s4": storedWith(map[string]Client{"4": {Pane: "w9:p9", Terminal: "term_gone", Socket: fake.path}}),
	}}
	seats := []Seat{
		{}, // a resumable row: no live tmux server
		{
			SocketPath: "/t/cc-2",
			Session:    "s2",
		},
		{SocketPath: "/t/cc-3", Session: "s3"},
		{SocketPath: "/t/cc-4", Session: "s4"},
	}
	for index, got := range probeFor(tmux, true, nil).Presence(context.Background(), seats) {
		if got != PresenceNotInHerdr {
			t.Errorf("seat %d Presence = %q, want %q", index, got, PresenceNotInHerdr)
		}
	}
}

func TestPresenceJoinsSeveralClientsAndSkipsAGoneOne(t *testing.T) {
	fake := healthyHerdr(t)
	tmux := &fakeTmux{stored: map[string]string{"/t/cc-1|s1": storedWith(map[string]Client{
		"20": {Pane: "wA:p1", Terminal: "term_b", Socket: fake.path},
		"10": {Pane: "w6:p2", Terminal: "term_a", Socket: fake.path},
		"30": {Pane: "w9:p9", Terminal: "term_gone", Socket: fake.path},
	})}}
	got := probeFor(tmux, true, nil).Presence(context.Background(), []Seat{{SocketPath: "/t/cc-1", Session: "s1"}})
	if want := "w6:p2 .professor idle pfm-1, wA:p1 wA working"; got[0] != want {
		t.Fatalf("Presence = %q, want %q (clients by pid, the gone one skipped)", got[0], want)
	}
}

func TestPresenceFailedLookupReadsHerdrErrorNeverNotInHerdr(t *testing.T) {
	healthy := healthyHerdr(t)
	refusing := serveHerdr(t, map[string]string{
		"pane.list": "error:forbidden", "agent.list": agentsJSON, "workspace.list": workspacesJSON,
	})
	wrongType := serveHerdr(t, map[string]string{
		"pane.list": panesJSON, "agent.list": agentsJSON, "workspace.list": `{"type":"nope"}`,
	})
	deadSocket := filepath.Join(t.TempDir(), "dead.sock")
	client := func(socket string) string {
		return storedWith(map[string]Client{"1": {Pane: "w6:p2", Terminal: "term_a", Socket: socket}})
	}
	tmux := &fakeTmux{
		stored: map[string]string{
			"/t/cc-ok|ok":      client(healthy.path),
			"/t/cc-dial|dial":  client(deadSocket),
			"/t/cc-refuse|ref": client(refusing.path),
			"/t/cc-type|type":  client(wrongType.path),
			"/t/cc-json|json":  "{not json",
			"/t/cc-nosock|nos": storedWith(map[string]Client{"1": {Pane: "w6:p2"}}),
		},
		errs: map[string]error{"tmuxerr": errors.New("tmux show-options: no server running")},
	}
	seats := []Seat{
		{SocketPath: "/t/cc-ok", Session: "ok"},
		{SocketPath: "/t/cc-dial", Session: "dial"},
		{SocketPath: "/t/cc-refuse", Session: "ref"},
		{SocketPath: "/t/cc-type", Session: "type"},
		{SocketPath: "/t/cc-json", Session: "json"},
		{SocketPath: "/t/cc-nosock", Session: "nos"},
		{SocketPath: "/t/cc-tmux", Session: "tmuxerr"},
		{SocketPath: "/t/cc-noname"},
		{
			SocketPath: "/t/cc-pathfail",
			Session:    "pf",
			Err:        errors.New("resolve socket \"x/y\": must be one relative tmux socket name"),
		},
	}
	var warn strings.Builder
	got := probeFor(tmux, true, &warn).Presence(context.Background(), seats)
	if got[0] != "w6:p2 .professor idle pfm-1" {
		t.Errorf("healthy seat beside failing ones = %q, want its pane", got[0])
	}
	causes := []string{
		"dial",
		"forbidden",
		"workspace_list",
		"decode",
		"no pane or no Herdr socket",
		"no server running",
		"names no session",
		"must be one relative",
	}
	for offset, cause := range causes {
		text := got[offset+1]
		if !strings.HasPrefix(text, "herdr error: ") || !strings.Contains(text, cause) {
			t.Errorf("seat %d Presence = %q, want %q naming %q", offset+1, text, "herdr error: …", cause)
		}
		if text == PresenceNotInHerdr {
			t.Errorf("seat %d failed lookup read as %q", offset+1, PresenceNotInHerdr)
		}
	}
	if lines := strings.Count(warn.String(), "\n"); lines != len(causes) {
		t.Errorf("warn lines = %d, want one per failed seat (%d):\n%s", lines, len(causes), warn.String())
	}
}
