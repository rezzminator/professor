package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
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
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testjail/fakegit"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

type placeCall struct {
	Method string
	Params map[string]any
}

// placeHerdr is a Herdr server whose workspaces, panes and moves a test reads
// back: workspaces label → id (pane counts in panes), the listed panes by id
// with their terminal and the directory pane.list reports, and every call in
// order. A move that empties a workspace closes it, as Herdr does.
type placeHerdr struct {
	mu         sync.Mutex
	workspaces [][3]any // id, label, pane count
	panes      map[string]string
	cwds       map[string]string
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
	case "workspace.rename":
		for index := range fake.workspaces {
			if id := fake.workspaces[index][0]; id == call.Params["workspace_id"] {
				fake.workspaces[index][1] = call.Params["label"]
				return map[string]any{
					"type":      "workspace_info",
					"workspace": map[string]any{"workspace_id": id, "label": call.Params["label"]},
				}, ""
			}
		}
		return nil, "workspace_not_found"
	case "layout.apply":
		workspace := call.Params["workspace_id"]
		return map[string]any{"layout": map[string]any{
			"workspace_id": workspace, "root": map[string]any{"type": "pane", "pane_id": fmt.Sprint(workspace, ":p1")},
		}}, ""
	case "pane.list":
		listed := []any{}
		for id, terminal := range fake.panes {
			workspace, _, _ := strings.Cut(id, ":")
			listed = append(listed, map[string]any{
				"pane_id": id, "workspace_id": workspace, "terminal_id": terminal, "cwd": fake.cwds[id],
			})
		}
		return map[string]any{"type": "pane_list", "panes": listed}, ""
	case "pane.get":
		terminal, found := fake.panes[pane]
		if !found {
			return nil, "pane_not_found"
		}
		return map[string]any{"type": "pane_info", "pane": map[string]any{"pane_id": pane, "terminal_id": terminal}}, ""
	case "pane.close":
		delete(fake.panes, pane)
		return map[string]any{"type": "ok"}, ""
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
		moved, left := workspace+":p8", strings.Split(pane, ":")[0]
		delete(fake.panes, pane)
		fake.panes[moved] = terminal
		result := map[string]any{
			"changed": true, "previous_pane_id": pane, "previous_workspace_id": left,
			"pane": map[string]any{"pane_id": moved, "workspace_id": workspace, "terminal_id": terminal},
		}
		for id := range fake.panes {
			if strings.HasPrefix(id, left+":") {
				return map[string]any{"type": "pane_move", "move_result": result}, ""
			}
		}
		result["closed_workspace_id"] = left
		fake.workspaces = slices.DeleteFunc(fake.workspaces, func(listed [3]any) bool { return listed[0] == left })
		return map[string]any{"type": "pane_move", "move_result": result}, ""
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

// fleetDesk is the fleet of the live case: a fleet-desk place and an orchard
// place, a framework repo with a plugin repo nested in it, and a place whose
// chats only resume.
func fleetDesk() place.Fleet {
	return place.Fleet{
		Rows: []compose.Row{
			{Kind: compose.LiveClaude, Project: "fleet-desk", CWD: "/fixture/fleet-desk"},
			{Kind: compose.LiveClaude, Project: "orchard", CWD: "/fixture/orchard"},
			{Kind: compose.LiveClaude, Project: ".professor", CWD: "/fixture/.professor"},
			{Kind: compose.ResumeClaude, Project: "callmeter", CWD: "/fixture/.professor/plugins/callmeter"},
			{Kind: compose.ResumeClaude, Project: "attic", CWD: "/fixture/attic"},
		},
		ProjectDirs: map[string]string{
			"fleet-desk": "/fixture/fleet-desk", "orchard": "/fixture/orchard", ".professor": "/fixture/.professor",
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
	if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	fleet := fleetDesk()
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
		{"its own place", "/fixture/fleet-desk", "wE"},
		{"a nested repo", "/fixture/.professor/plugins/callmeter", "wC"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &placeHerdr{workspaces: [][3]any{
				{"wC", ".professor", 6}, {"wF", "orchard", 7}, {"wE", "fleet-desk", 6},
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
	seat, err := spawnInPlace(t, fake, "/fixture/fleet-desk")
	if err != nil {
		t.Fatal(err)
	}
	create, found := fake.call("workspace.create")
	if !found || create.Params["cwd"] != "/fixture/fleet-desk" || create.Params["label"] != "fleet-desk" ||
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
	fleet := fleetDesk()
	request := HerdrRequest{
		Paths: values,
		Choice: BackendChoice{
			Backend: BackendHerdr, HerdrSocket: servePlaceHerdr(t, fake), HerdrPane: "wF:p3", HerdrWorkspace: "wF",
		},
		Run: fleetdb.SeatRun{CWD: "/fixture/fleet-desk"},
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
		t.Fatalf("creates=%d workspaces=%q errors=%v, want one fleet-desk workspace w91 for every birth",
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
		workspaces: [][3]any{{"wC", ".professor", 6}, {"wF", "orchard", 7}, {"wE", "fleet-desk", 6}},
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
	fleet := fleetDesk()
	live := func(project, cwd, seatID, pane string) compose.Row {
		workspace, _, _ := strings.Cut(pane, ":")
		return compose.Row{
			Kind: compose.LiveClaude, Backend: BackendHerdr, Project: project, CWD: cwd, Name: seatID,
			SeatID: seatID, Socket: socket, PaneID: pane, HerdrWorkspace: workspace,
		}
	}
	fleet.Rows = append(fleet.Rows,
		live("fleet-desk", "/fixture/fleet-desk", "ops-seat", "wF:p3"),
		live("orchard", "/fixture/orchard", "home-seat", "wF:p4"),
		live("fleet-desk", "/fixture/fleet-desk", "caller-seat", "wF:p6"),
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
				destination["label"] != "ops-seat" ||
				destination["workspace_id"] != "wE" ||
				call.Params["focus"] != false {
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

// A chat that is the last pane of another place's workspace moves into its
// own, and Herdr closes the workspace the move emptied; a birth beside it then
// finds every chat placed. A fleet with no live Herdr chat asks Herdr nothing.
func TestReconcileHerdrMovesTheLastPaneOfAWorkspace(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	fake := &placeHerdr{
		workspaces: [][3]any{{"wE", "fleet-desk", 6}, {"wS", "scratch", 1}},
		panes:      map[string]string{"wS:p1": "term-ops"},
	}
	socket := servePlaceHerdr(t, fake)
	if err := ReconcileHerdr(ctx, values, socket, fleetDesk(), ""); err != nil || len(fake.methods()) != 0 {
		t.Fatalf("a fleet with no live Herdr chat: err %v, calls %v", err, fake.methods())
	}
	state := putOpsSeat(t, values, socket, "wS:p1")
	fleet := fleetDesk()
	fleet.Rows = append(fleet.Rows, compose.Row{
		Kind: compose.LiveClaude, Backend: BackendHerdr, Project: "fleet-desk", CWD: "/fixture/fleet-desk", Name: "OPS",
		SeatID: "ops-seat", Socket: socket, PaneID: "wS:p1", HerdrWorkspace: "wS",
	})
	if err := ReconcileHerdr(ctx, values, socket, fleet, ""); err != nil {
		t.Fatal(err)
	}
	move, found := fake.call("pane.move")
	want := map[string]any{
		"pane_id":     "wS:p1",
		"destination": map[string]any{"type": "new_tab", "workspace_id": "wE", "label": "OPS"},
		"focus":       false,
	}
	if !found || !reflect.DeepEqual(move.Params, want) {
		t.Fatalf("pane.move = %+v (found %v), want %+v", move.Params, found, want)
	}
	if want := [][3]any{{"wE", "fleet-desk", float64(6)}}; !reflect.DeepEqual(fake.listed(), want) {
		t.Fatalf("workspaces = %v, want %v: the emptied scratch workspace closed", fake.listed(), want)
	}
	moved, found, err := state.Seat(ctx, "ops-seat")
	if err != nil || !found || moved.HerdrPane != "wE:p8" || moved.HerdrWorkspace != "wE" {
		t.Fatalf("seat after the move = %+v (found %v, err %v), want wE:p8", moved, found, err)
	}
	fleet.Rows[len(fleet.Rows)-1].PaneID, fleet.Rows[len(fleet.Rows)-1].HerdrWorkspace = "wE:p8", "wE"
	workspace, unmoved, err := placeHerdrBirth(ctx, HerdrRequest{
		Paths: values,
		Choice: BackendChoice{
			Backend: BackendHerdr, HerdrSocket: socket, HerdrPane: "wE:p1", HerdrWorkspace: "wE",
		},
		Run: fleetdb.SeatRun{CWD: "/fixture/fleet-desk"},
	}, fleet)
	if err != nil || workspace != "wE" || unmoved != nil {
		t.Fatalf("birth beside a placed chat: workspace %q, unmoved %v, err %v; want wE", workspace, unmoved, err)
	}
}

// putOpsSeat records OPS, a live Claude chat proved in pane on socket, with
// the seat host writeSeatHost seeds; the returned state closes with the test.
func putOpsSeat(t *testing.T, values paths.Values, socket, pane string) *fleetdb.Store {
	t.Helper()
	writeSeatHost(t, values, "ops-seat")
	state := fleetdb.OpenSharedState(context.Background(), values)
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	})
	workspace, _, _ := strings.Cut(pane, ":")
	if err := state.PutSeat(context.Background(), fleetdb.Seat{
		SeatID: "ops-seat", SessionID: "6fafd8a7-5059-4288-bf58-23e60c5fdb88", Engine: "cc", Name: "OPS",
		Backend: BackendHerdr, State: "live", HerdrSocket: socket, HerdrPane: pane, HerdrWorkspace: workspace,
		HerdrTerminalID: "term-ops", SeatHostPID: 42, SeatHostStart: 100,
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

// listed is the fake's workspaces as a test reads them back.
func (fake *placeHerdr) listed() [][3]any {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	listed := make([][3]any, 0, len(fake.workspaces))
	for _, workspace := range fake.workspaces {
		// Pane counts read back as JSON numbers do.
		workspace[2] = float64(workspace[2].(int))
		listed = append(listed, workspace)
	}
	return listed
}

// With no workspace wearing its place's label, a chat's workspace takes the
// label when it holds nothing but the place's chats and shells (a shell under
// the place counts, wherever its supervisor sits); one holding another place's
// pane keeps its label, and the place gets a workspace the chat moves into.
// A scan fault is named, never a quiet fleet.
func TestReconcileHerdrRenamesAWorkspaceHoldingOnlyItsPlace(t *testing.T) {
	for _, test := range []struct {
		name       string
		neighbour  string // the directory pane.list reports for wS:p2, "" for no second pane
		chat       string // the directory of the chat running in wS:p2, "" for a shell
		workspaces [][3]any
		pane       string
	}{
		{"only the chat", "", "", [][3]any{{"wS", "fleet-desk", float64(1)}}, "wS:p1"},
		{"a shell under the place", "/fixture/fleet-desk/docs", "", [][3]any{{"wS", "fleet-desk", float64(2)}}, "wS:p1"},
		{
			"a shell of another place", "/fixture/orchard", "",
			[][3]any{{"wS", "scratch", float64(2)}, {"w91", "fleet-desk", float64(1)}},
			"w91:p8",
		},
		{
			"another place's chat in the place's directory", "/fixture/fleet-desk", "/fixture/orchard",
			[][3]any{{"wS", "scratch", float64(2)}, {"w91", "fleet-desk", float64(1)}},
			"w91:p8",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			if test.neighbour != "" {
				fake.workspaces[0][2] = 2
				fake.panes["wS:p2"] = "term-neighbour"
				fake.cwds = map[string]string{"wS:p2": test.neighbour}
			}
			socket := servePlaceHerdr(t, fake)
			state := putOpsSeat(t, values, socket, "wS:p1")
			fleet := fleetDesk()
			fleet.Rows = append(fleet.Rows, compose.Row{
				Kind: compose.LiveClaude, Backend: BackendHerdr, Project: "fleet-desk", CWD: "/fixture/fleet-desk",
				Name: "OPS", SeatID: "ops-seat", Socket: socket, PaneID: "wS:p1", HerdrWorkspace: "wS",
			})
			if test.chat != "" {
				fleet.Rows = append(fleet.Rows, compose.Row{
					Kind: compose.LiveClaude, Backend: BackendHerdr, Project: "orchard", CWD: test.chat,
					Name: "GROVE", SeatID: "grove-seat", Socket: socket, PaneID: "wS:p2", HerdrWorkspace: "wS",
				})
			}
			if err := ReconcileHerdr(ctx, values, socket, fleet, ""); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(fake.listed(), test.workspaces) {
				t.Fatalf("workspaces = %v, want %v (calls %v)", fake.listed(), test.workspaces, fake.methods())
			}
			seat, found, err := state.Seat(ctx, "ops-seat")
			if err != nil || !found || seat.HerdrPane != test.pane {
				t.Fatalf("seat = %+v (found %v, err %v), want pane %s", seat, found, err, test.pane)
			}
		})
	}
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	faulted := fleetDesk()
	faulted.Faults = []string{"read native seats: database is locked"}
	err = ReconcileHerdr(context.Background(), values, servePlaceHerdr(t, &placeHerdr{}), faulted, "")
	if err == nil || err.Error() != "fleet scan: read native seats: database is locked" {
		t.Fatalf("err = %v, want the scan fault named", err)
	}
}

// A chat files by its own directory, never by its pane's: a chat opened in a
// pane whose supervisor works in the home directory, running in a worktree or
// workbench of its project, sits in its project's workspace, never in "~".
func TestReconcileHerdrPlacesAChatByItsOwnDirectory(t *testing.T) {
	repo := testjail.GitRepoWithWorktrees(t, &fakegit.Runner{}, "lantern")
	bench := filepath.Join(repo.Repo, ".workbenches", "outreach")
	if err := os.MkdirAll(bench, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, cwd, pane, want string }{
		{"a worktree chat in the home workspace", repo.Inner, "wH:p2", "wI:p8"},
		{"a workbench chat in the home workspace", bench, "wH:p2", "wI:p8"},
		{"a worktree chat already in its project's workspace", repo.Inner, "wI:p2", "wI:p2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			ctx := context.Background()
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			fake := &placeHerdr{
				workspaces: [][3]any{{"wH", "~", 2}, {"wI", "lantern", 1}},
				panes:      map[string]string{"wH:p1": "term-shell", "wI:p1": "term-shell-2", test.pane: "term-ops"},
				cwds:       map[string]string{"wH:p1": values.Home, "wI:p1": repo.Repo, test.pane: values.Home},
			}
			socket := servePlaceHerdr(t, fake)
			state := putOpsSeat(t, values, socket, test.pane)
			seat, _, err := state.Seat(ctx, "ops-seat")
			if err != nil {
				t.Fatal(err)
			}
			scan := compose.Compose(compose.Input{
				Options: compose.Options{View: compose.AllView},
				Transcripts: []store.Transcript{
					{UUID: seat.SessionID, Path: filepath.Join(values.Home, "ops.jsonl"), CWD: test.cwd},
				},
				HerdrSeats: []gather.HerdrSeat{{Seat: seat, CWD: values.Home}},
			})
			fleet := place.Fleet{
				Rows:        scan.Rows,
				ProjectDirs: scan.ProjectDirs,
				Home:        values.Home,
				Benches: []workbench.Bench{
					{Dir: bench, Root: repo.Repo, Project: "lantern", Key: "lantern › outreach"},
				},
			}
			if err := ReconcileHerdr(ctx, values, socket, fleet, ""); err != nil {
				t.Fatal(err)
			}
			if _, ran := fake.call("workspace.list"); !ran {
				t.Fatalf("reconcile never listed the workspaces: %v", fake.methods())
			}
			if move, moved := fake.call("pane.move"); moved && !reflect.DeepEqual(
				move.Params["destination"], map[string]any{"type": "new_tab", "workspace_id": "wI", "label": "OPS"},
			) {
				t.Fatalf("pane.move = %+v, want a new OPS tab in lantern's wI", move.Params)
			}
			want := [][3]any{{"wH", "~", float64(2)}, {"wI", "lantern", float64(1)}}
			if !reflect.DeepEqual(fake.listed(), want) {
				t.Fatalf("workspaces = %v, want %v (calls %v)", fake.listed(), want, fake.methods())
			}
			placed, found, err := state.Seat(ctx, "ops-seat")
			if err != nil || !found || placed.HerdrPane != test.want {
				t.Fatalf("seat = %+v (found %v, err %v), want pane %s", placed, found, err, test.want)
			}
		})
	}
}

// An open happens in the caller's pane only when the caller's workspace is
// the chat's place's, renamed to the place's label first when no workspace
// wears it and the caller's holds nothing but the place's; otherwise it opens
// in a new tab of the place's workspace. A chat filed under no place opens in
// place, asking Herdr nothing.
func TestHerdrOpensInPlaceOnlyInItsPlaceWorkspace(t *testing.T) {
	for _, test := range []struct {
		name       string
		workspaces [][3]any
		neighbour  string // the directory of the caller's neighbour pane wC:p4
		cwd        string
		inPlace    bool
		want       [][3]any
	}{
		{
			"the caller's workspace is the place's",
			[][3]any{{"wC", "orchard", 2}},
			"/fixture/fleet-desk",
			"/fixture/orchard", true,
			[][3]any{{"wC", "orchard", float64(2)}},
		},
		{
			"the place's workspace is another",
			[][3]any{{"wC", "orchard", 2}, {"wE", "fleet-desk", 1}},
			"/fixture/orchard",
			"/fixture/fleet-desk", false,
			[][3]any{{"wC", "orchard", float64(2)}, {"wE", "fleet-desk", float64(1)}},
		},
		{
			"no workspace wears the label and the caller's holds only the place's",
			[][3]any{{"wC", "scratch", 2}},
			"/fixture/fleet-desk/docs", "/fixture/fleet-desk", true,
			[][3]any{{"wC", "fleet-desk", float64(2)}},
		},
		{
			"no workspace wears the label and the caller's holds another place's",
			[][3]any{{"wC", "scratch", 2}},
			"/fixture/orchard", "/fixture/fleet-desk", false,
			[][3]any{{"wC", "scratch", float64(2)}},
		},
		{"a chat filed under no place", [][3]any{{"wC", "scratch", 2}}, "/fixture/orchard", "", true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			fake := &placeHerdr{
				workspaces: test.workspaces,
				panes:      map[string]string{"wC:p3": "term-caller", "wC:p4": "term-neighbour"},
				cwds:       map[string]string{"wC:p3": "/fixture/attic", "wC:p4": test.neighbour},
			}
			socket := servePlaceHerdr(t, fake)
			inPlace, err := HerdrOpensInPlace(context.Background(), values, &BackendChoice{
				Backend: BackendHerdr, HerdrSocket: socket, HerdrPane: "wC:p3", HerdrWorkspace: "wC",
				Places: scanned(fleetDesk()),
			}, test.cwd)
			if err != nil || inPlace != test.inPlace {
				t.Fatalf("HerdrOpensInPlace = %v, %v; want %v (calls %v)", inPlace, err, test.inPlace, fake.methods())
			}
			if test.want == nil {
				if calls := fake.methods(); len(calls) != 0 {
					t.Fatalf("an unplaced chat asked Herdr %v", calls)
				}
				return
			}
			if !reflect.DeepEqual(fake.listed(), test.want) {
				t.Fatalf("workspaces = %v, want %v (calls %v)", fake.listed(), test.want, fake.methods())
			}
			if _, created := fake.call("workspace.create"); created {
				t.Fatalf("an open decision created a workspace: %v", fake.methods())
			}
		})
	}
}
