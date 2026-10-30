package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type placeCall struct {
	Method string
	Params map[string]any
}

// placeHerdr is a Herdr server whose workspaces, panes and moves a test reads
// back: workspaces label → id (pane counts in panes), the listed panes by id
// with their terminal, and every call in order.
type placeHerdr struct {
	mu         sync.Mutex
	workspaces [][3]any // id, label, pane count
	panes      map[string]string
	calls      []placeCall
	listDelay  time.Duration // how long each workspace.list takes to answer
}

func (fake *placeHerdr) answer(call placeCall) (any, string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, call)
	pane, _ := call.Params["pane_id"].(string)
	switch call.Method {
	case "ping":
		return map[string]any{"type": "pong", "version": "0.9.5", "protocol": 22}, ""
	case "workspace.list":
		time.Sleep(fake.listDelay)
		listed := []any{}
		for _, workspace := range fake.workspaces {
			listed = append(listed, map[string]any{
				"workspace_id": workspace[0], "label": workspace[1], "pane_count": workspace[2],
			})
		}
		return map[string]any{"type": "workspace_list", "workspaces": listed}, ""
	case "workspace.create":
		id := fmt.Sprintf("w%d", 90+len(fake.workspaces))
		fake.workspaces = append(fake.workspaces, [3]any{id, call.Params["label"], 1})
		return map[string]any{
			"type": "workspace_created", "workspace": map[string]any{"workspace_id": id, "label": call.Params["label"]},
		}, ""
	case "layout.apply":
		workspace := call.Params["workspace_id"]
		return map[string]any{"layout": map[string]any{
			"workspace_id": workspace, "root": map[string]any{"type": "pane", "pane_id": fmt.Sprint(workspace, ":p1")},
		}}, ""
	case "pane.list":
		listed := []any{}
		for id, terminal := range fake.panes {
			workspace, _, _ := strings.Cut(id, ":")
			listed = append(listed, map[string]any{"pane_id": id, "workspace_id": workspace, "terminal_id": terminal})
		}
		return map[string]any{"type": "pane_list", "panes": listed}, ""
	case "pane.process_info":
		return map[string]any{"type": "pane_process_info", "process_info": map[string]any{
			"pane_id": pane, "shell_pid": 41,
		}}, ""
	case "pane.move":
		destination, _ := call.Params["destination"].(map[string]any)
		workspace, _ := destination["workspace_id"].(string)
		terminal, found := fake.panes[pane]
		if !found {
			return nil, "pane_not_found"
		}
		moved := workspace + ":p8"
		delete(fake.panes, pane)
		fake.panes[moved] = terminal
		return map[string]any{"type": "pane_move", "move_result": map[string]any{
			"changed": true, "previous_pane_id": pane, "previous_workspace_id": strings.Split(pane, ":")[0],
			"pane": map[string]any{"pane_id": moved, "workspace_id": workspace, "terminal_id": terminal},
		}}, ""
	}
	return nil, "unexpected_method"
}

func (fake *placeHerdr) methods() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	methods := make([]string, 0, len(fake.calls))
	for _, call := range fake.calls {
		methods = append(methods, call.Method)
	}
	return methods
}

func (fake *placeHerdr) call(method string) (placeCall, bool) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, call := range fake.calls {
		if call.Method == method {
			return call, true
		}
	}
	return placeCall{}, false
}

func servePlaceHerdr(t *testing.T, fake *placeHerdr) string {
	t.Helper()
	socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { _ = listener.Close(); <-done })
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var call placeCall
			if err := json.NewDecoder(conn).Decode(&call); err == nil {
				result, code := fake.answer(call)
				if code != "" {
					_, _ = fmt.Fprintf(conn, `{"id":"x","ok":false,"error":{"code":%q,"message":"fixture"}}`+"\n", code)
				} else {
					_ = json.NewEncoder(conn).Encode(map[string]any{"id": "x", "ok": true, "result": result})
				}
			}
			_ = conn.Close()
		}
	}()
	return socket
}

// hostOps is the fleet of the live case: a host-ops place and an orchard
// place, a framework repo with a plugin repo nested in it, and a place whose
// chats only resume.
func hostOps() place.Fleet {
	return place.Fleet{
		Rows: []compose.Row{
			{Kind: compose.LiveClaude, Project: "host-ops", CWD: "/fixture/host-ops"},
			{Kind: compose.LiveClaude, Project: "orchard", CWD: "/fixture/orchard"},
			{Kind: compose.LiveClaude, Project: ".professor", CWD: "/fixture/.professor"},
			{Kind: compose.ResumeClaude, Project: "callmeter", CWD: "/fixture/.professor/plugins/callmeter"},
			{Kind: compose.ResumeClaude, Project: "attic", CWD: "/fixture/attic"},
		},
		ProjectDirs: map[string]string{
			"host-ops": "/fixture/host-ops", "orchard": "/fixture/orchard", ".professor": "/fixture/.professor",
			"callmeter": "/fixture/.professor/plugins/callmeter", "attic": "/fixture/attic",
		},
		Home: "/jail",
	}
}

func spawnInPlace(t *testing.T, fake *placeHerdr, cwd string) (fleetdb.Seat, error) {
	t.Helper()
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	socket := servePlaceHerdr(t, fake)
	binary := filepath.Join(values.Home, "native-claude")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	fleet := hostOps()
	result, err := SpawnHerdr(context.Background(), HerdrRequest{
		Paths: values,
		// The caller sits in orchard's workspace.
		Choice: BackendChoice{
			Backend:        BackendHerdr,
			HerdrSocket:    socket,
			HerdrPane:      "wF:p3",
			HerdrWorkspace: "wF",
			Places:         scanned(fleet),
		},
		Seat: fleetdb.Seat{SessionID: "sid1", Engine: "cc", Name: "BORN"},
		Run:  fleetdb.SeatRun{Binary: binary, CWD: cwd},
	})
	return result.Seat, err
}

// A new chat is born in its place's workspace, not the caller's; a label
// that already names a workspace is reused, never duplicated; a repo nested
// in another root is born in the outer root's workspace.
func TestSpawnHerdrBornInItsPlaceWorkspace(t *testing.T) {
	for _, test := range []struct{ name, cwd, want string }{
		{"its own place", "/fixture/host-ops", "wE"},
		{"a nested repo", "/fixture/.professor/plugins/callmeter", "wC"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &placeHerdr{workspaces: [][3]any{
				{"wC", ".professor", 6}, {"wF", "orchard", 7}, {"wE", "host-ops", 6},
			}}
			seat, err := spawnInPlace(t, fake, test.cwd)
			if err != nil {
				t.Fatal(err)
			}
			apply, found := fake.call("layout.apply")
			if !found || apply.Params["workspace_id"] != test.want || apply.Params["focus"] != false {
				t.Fatalf("layout.apply = %+v (found %v), want workspace %s unfocused", apply, found, test.want)
			}
			if _, created := fake.call("workspace.create"); created {
				t.Fatalf("a workspace was created though %q exists: %v", test.want, fake.methods())
			}
			if seat.HerdrWorkspace != test.want || seat.HerdrPane != test.want+":p1" {
				t.Fatalf("seat = %+v, want workspace %s", seat, test.want)
			}
		})
	}
}

// A place with no workspace gets one, opening in the place's root, wearing
// its label, without focus; the chat is born there.
func TestSpawnHerdrCreatesAMissingPlaceWorkspace(t *testing.T) {
	fake := &placeHerdr{workspaces: [][3]any{{"wF", "orchard", 7}}}
	seat, err := spawnInPlace(t, fake, "/fixture/host-ops")
	if err != nil {
		t.Fatal(err)
	}
	create, found := fake.call("workspace.create")
	if !found || create.Params["cwd"] != "/fixture/host-ops" || create.Params["label"] != "host-ops" ||
		create.Params["focus"] != false {
		t.Fatalf("workspace.create = %+v (found %v)", create, found)
	}
	if seat.HerdrWorkspace != "w91" {
		t.Fatalf("seat = %+v, want the created workspace w91", seat)
	}
}

// Births racing into a place with no workspace create it once: placement on a
// socket lists and creates under one lock.
func TestPlaceHerdrBirthsRacingCreateOneWorkspace(t *testing.T) {
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	fake := &placeHerdr{workspaces: [][3]any{{"wF", "orchard", 7}}, listDelay: 20 * time.Millisecond}
	fleet := hostOps()
	request := HerdrRequest{
		Paths: values,
		Choice: BackendChoice{
			Backend: BackendHerdr, HerdrSocket: servePlaceHerdr(t, fake), HerdrPane: "wF:p3", HerdrWorkspace: "wF",
		},
		Run: fleetdb.SeatRun{CWD: "/fixture/host-ops"},
	}
	workspaces, failures := make([]string, 4), make([]error, 4)
	var births sync.WaitGroup
	for birth := range workspaces {
		births.Add(1)
		go func() {
			defer births.Done()
			workspaces[birth], _, failures[birth] = placeHerdrBirth(context.Background(), request, fleet)
		}()
	}
	births.Wait()
	creates := 0
	for _, method := range fake.methods() {
		if method == "workspace.create" {
			creates++
		}
	}
	distinct := slices.Compact(slices.Clone(workspaces))
	if creates != 1 || errors.Join(failures...) != nil || len(distinct) != 1 || distinct[0] != "w91" {
		t.Fatalf("creates=%d workspaces=%q errors=%v, want one host-ops workspace w91 for every birth",
			creates, workspaces, errors.Join(failures...))
	}
}

// writeSeatHost seeds the process table a proved seat-host needs: shell 41,
// seat host 42 beneath it.
func writeSeatHost(t *testing.T, values paths.Values, seatID string) {
	t.Helper()
	if err := os.MkdirAll(values.ProcRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(values.ProcRoot, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for pid, parent := range map[int]int{41: 1, 42: 41} {
		dir := filepath.Join(values.ProcRoot, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields("S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100")
		fields[1] = fmt.Sprint(parent)
		stat := fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " "))
		argv := "/bin/sh\x00"
		if pid == 42 {
			argv = "/fixture/pfm\x00internal\x00seat-host\x00" + seatID + "\x00"
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A running chat in another place's workspace moves into its own, unfocused,
// and its seat takes the pane id Herdr gave it there, so every reader that
// proves a seat (kill, reload, the hooks, the caller's identity) finds it.
// A plain shell, a chat already home, a chat of a resumable-only place and
// the pane running this command all stay, and nothing new is created.
func TestReconcileHerdrMovesAMisplacedChatAndRewritesItsSeat(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	fake := &placeHerdr{
		workspaces: [][3]any{{"wC", ".professor", 6}, {"wF", "orchard", 7}, {"wE", "host-ops", 6}},
		panes:      map[string]string{"wF:p3": "term-ops", "wF:p5": "plain-shell", "wF:p6": "term-caller"},
	}
	socket := servePlaceHerdr(t, fake)
	writeSeatHost(t, values, "ops-seat")
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	seat := fleetdb.Seat{
		SeatID: "ops-seat", SessionID: "6fafd8a7-5059-4288-bf58-23e60c5fdb88", Engine: "cc", Name: "OPS",
		Backend: BackendHerdr, State: "live", HerdrSocket: socket, HerdrPane: "wF:p3", HerdrWorkspace: "wF",
		HerdrTerminalID: "term-ops", SeatHostPID: 42, SeatHostStart: 100,
	}
	if err := state.PutSeat(ctx, seat); err != nil {
		t.Fatal(err)
	}
	fleet := hostOps()
	live := func(project, cwd, seatID, pane string) compose.Row {
		workspace, _, _ := strings.Cut(pane, ":")
		return compose.Row{
			Kind: compose.LiveClaude, Backend: BackendHerdr, Project: project, CWD: cwd, Name: seatID,
			SeatID: seatID, Socket: socket, PaneID: pane, HerdrWorkspace: workspace,
		}
	}
	fleet.Rows = append(fleet.Rows,
		live("host-ops", "/fixture/host-ops", "ops-seat", "wF:p3"),
		live("orchard", "/fixture/orchard", "home-seat", "wF:p4"),
		live("host-ops", "/fixture/host-ops", "caller-seat", "wF:p6"),
	)
	if err := ReconcileHerdr(ctx, values, socket, fleet, "wF:p6"); err != nil {
		t.Fatal(err)
	}
	moves := 0
	for _, call := range fake.calls {
		switch call.Method {
		case "workspace.create":
			t.Fatalf("reconcile created a workspace: %+v", call)
		case "pane.move":
			moves++
			destination := call.Params["destination"].(map[string]any)
			if call.Params["pane_id"] != "wF:p3" || destination["type"] != "new_tab" ||
				destination["workspace_id"] != "wE" || call.Params["focus"] != false {
				t.Fatalf("pane.move = %+v", call.Params)
			}
		}
	}
	if moves != 1 {
		t.Fatalf("moves = %d, want only the misplaced chat: %v", moves, fake.methods())
	}
	moved, found, err := state.Seat(ctx, "ops-seat")
	if err != nil || !found || moved.HerdrPane != "wE:p8" || moved.HerdrWorkspace != "wE" ||
		moved.HerdrTerminalID != "term-ops" || moved.State != "live" {
		t.Fatalf("seat after the move = %+v (found %v, err %v)", moved, found, err)
	}
	byPane, found, err := state.SeatByPane(ctx, socket, "wE:p8")
	if err != nil || !found || byPane.SeatID != "ops-seat" {
		t.Fatalf("seat by its new pane = %+v (found %v, err %v)", byPane, found, err)
	}
	proved, owned, err := gather.ResolveHerdrSeat(ctx, values, moved)
	if err != nil || !owned || proved.HerdrPane != "wE:p8" || proved.HerdrWorkspace != "wE" {
		t.Fatalf("proof after the move = %+v (owned %v, err %v)", proved, owned, err)
	}
	if fake.panes["wF:p5"] != "plain-shell" || fake.panes["wF:p6"] != "term-caller" {
		t.Fatalf("a pane that is no misplaced chat moved: %v", fake.panes)
	}
}

// A chat that is the last pane of its workspace stays, named, since moving it
// would close that workspace, and a birth beside it goes on, naming it; a
// fleet with no live Herdr chat asks Herdr nothing.
func TestReconcileHerdrNeverClosesAWorkspace(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	fake := &placeHerdr{
		workspaces: [][3]any{{"wE", "host-ops", 6}, {"wS", "scratch", 1}},
		panes:      map[string]string{"wS:p1": "term-ops"},
	}
	socket := servePlaceHerdr(t, fake)
	if err := ReconcileHerdr(ctx, values, socket, hostOps(), ""); err != nil || len(fake.methods()) != 0 {
		t.Fatalf("a fleet with no live Herdr chat: err %v, calls %v", err, fake.methods())
	}
	writeSeatHost(t, values, "ops-seat")
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := state.PutSeat(ctx, fleetdb.Seat{
		SeatID: "ops-seat", SessionID: "6fafd8a7-5059-4288-bf58-23e60c5fdb88", Engine: "cc", Name: "OPS",
		Backend: BackendHerdr, State: "live", HerdrSocket: socket, HerdrPane: "wS:p1", HerdrWorkspace: "wS",
		HerdrTerminalID: "term-ops", SeatHostPID: 42, SeatHostStart: 100,
	}); err != nil {
		t.Fatal(err)
	}
	fleet := hostOps()
	fleet.Rows = append(fleet.Rows, compose.Row{
		Kind: compose.LiveClaude, Backend: BackendHerdr, Project: "host-ops", CWD: "/fixture/host-ops", Name: "OPS",
		SeatID: "ops-seat", Socket: socket, PaneID: "wS:p1", HerdrWorkspace: "wS",
	})
	err = ReconcileHerdr(ctx, values, socket, fleet, "")
	if err == nil || !strings.Contains(err.Error(), "last pane of workspace wS") {
		t.Fatalf("err = %v, want the last-pane refusal", err)
	}
	if _, moved := fake.call("pane.move"); moved {
		t.Fatalf("the last pane of a workspace moved: %v", fake.methods())
	}
	workspace, unmoved, err := placeHerdrBirth(ctx, HerdrRequest{
		Paths: values,
		Choice: BackendChoice{
			Backend: BackendHerdr, HerdrSocket: socket, HerdrPane: "wE:p1", HerdrWorkspace: "wE",
		},
		Run: fleetdb.SeatRun{CWD: "/fixture/host-ops"},
	}, fleet)
	if err != nil || workspace != "wE" || unmoved == nil ||
		!strings.Contains(unmoved.Error(), "last pane of workspace wS") {
		t.Fatalf("birth beside an unmovable chat: workspace %q, unmoved %v, err %v; want wE with the chat named",
			workspace, unmoved, err)
	}
}

// A chat that cannot move gets its place no workspace: one is created only for
// a chat proved movable into it. A scan fault is named, never a quiet fleet.
func TestReconcileHerdrCreatesNoWorkspaceForAnUnmovableChat(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	fake := &placeHerdr{
		workspaces: [][3]any{{"wS", "scratch", 1}},
		panes:      map[string]string{"wS:p1": "term-ops"},
	}
	socket := servePlaceHerdr(t, fake)
	writeSeatHost(t, values, "ops-seat")
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := state.PutSeat(ctx, fleetdb.Seat{
		SeatID: "ops-seat", SessionID: "6fafd8a7-5059-4288-bf58-23e60c5fdb88", Engine: "cc", Name: "OPS",
		Backend: BackendHerdr, State: "live", HerdrSocket: socket, HerdrPane: "wS:p1", HerdrWorkspace: "wS",
		HerdrTerminalID: "term-ops", SeatHostPID: 42, SeatHostStart: 100,
	}); err != nil {
		t.Fatal(err)
	}
	fleet := hostOps()
	fleet.Rows = append(fleet.Rows, compose.Row{
		Kind: compose.LiveClaude, Backend: BackendHerdr, Project: "host-ops", CWD: "/fixture/host-ops", Name: "OPS",
		SeatID: "ops-seat", Socket: socket, PaneID: "wS:p1", HerdrWorkspace: "wS",
	})
	err = ReconcileHerdr(ctx, values, socket, fleet, "")
	if err == nil || !strings.Contains(err.Error(), "last pane of workspace wS") {
		t.Fatalf("err = %v, want the last-pane refusal", err)
	}
	if created, found := fake.call("workspace.create"); found {
		t.Fatalf("a workspace was created for a chat that stays: %+v", created)
	}
	faulted := hostOps()
	faulted.Faults = []string{"read native seats: database is locked"}
	err = ReconcileHerdr(ctx, values, socket, faulted, "")
	if err == nil || !strings.Contains(err.Error(), "fleet scan: read native seats: database is locked") {
		t.Fatalf("err = %v, want the scan fault named", err)
	}
}
