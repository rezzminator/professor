package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// An unnamed native Herdr seat finds itself through every self door: the
// shell's whoami prints a handle status resolves, and the chat MCP's whoami
// and exit — stamped with the identity the per-chat proxy derives — resolve
// the caller's own seat instead of refusing it as "matched no live chat".
func TestUnnamedHerdrSeatFindsItself(t *testing.T) {
	jailTest(t)
	values := jailPaths(t)
	socket := filepath.Join(t.TempDir(), "h.sock")
	serveNativeAdapter(t, socket, map[string]string{
		"pane.get":  `{"type":"pane_info","pane":{"pane_id":"w6:p3","workspace_id":"w6","terminal_id":"fixture-w6:p3","tab_id":"w6:t1","focused":true}}`,
		"pane.list": `{"type":"pane_list","panes":[{"pane_id":"w6:p3","workspace_id":"w6","terminal_id":"fixture-w6:p3","tab_id":"w6:t1","focused":true}]}`,
		"agent.get": `{"type":"agent_info","agent":{"pane_id":"w6:p3","terminal_id":"fixture-w6:p3","workspace_id":"w6","tab_id":"w6:t1","focused":true,"agent":"claude","agent_status":"idle","state_change_seq":0,"revision":1}}`,
		"pane.process_info": fmt.Sprintf(
			`{"type":"pane_process_info","process_info":{"pane_id":"w6:p3","shell_pid":900001,"foreground_processes":[{"pid":%d,"argv0":"claude"}]}}`,
			os.Getpid(),
		),
		"pane.read": `{"type":"pane_read","read":{"pane_id":"p3","workspace_id":"w6","text":"❯ \n","truncated":false}}`,
	})
	seat := fleetdb.Seat{
		SeatID: "native-unnamed", SessionID: "native-session", Engine: "cc", Backend: "herdr", State: "live",
		HerdrSocket: socket, HerdrPane: "w6:p3", HerdrWorkspace: "w6", HerdrTerminalID: "fixture-w6:p3",
	}
	values.ProcRoot = t.TempDir()
	t.Setenv(paths.EnvProcRoot, values.ProcRoot)
	stageNativeCallerProc(t, values.ProcRoot, seat.SeatID)
	if err := gather.StampHerdrHost(gather.NewProcFS(values.ProcRoot), &seat, 900001); err != nil {
		t.Fatal(err)
	}
	state := fleetdb.OpenSharedState(context.Background(), values)
	if err := state.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w6:p3")
	handle := resolve.SeatPrefix + seat.SeatID

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "whoami"}, &stdout, &stderr); code != 0 ||
		strings.TrimSpace(stdout.String()) != handle {
		t.Fatalf("whoami code=%d stdout=%q stderr=%q, want %q", code, stdout.String(), stderr.String(), handle)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"chat", "status", handle}, &stdout, &stderr); code != 0 {
		t.Fatalf("status %s code=%d stdout=%q stderr=%q", handle, code, stdout.String(), stderr.String())
	}

	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{CodexSeats: pfmchat.CodexSeats(&runtime)})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := identifier.Identify(ctx)
	if err != nil {
		t.Fatalf("proxy caller identity: %v", err)
	}
	// The payload the per-chat proxy stamps on every tools/call.
	wire, err := json.Marshal(mcpserv.ProxyIdentity{
		Version: mcpserv.ProxyWireVersion, Session: identity.Session, Backend: identity.Backend,
		SeatID: identity.SeatID, HerdrWorkspace: identity.HerdrWorkspace, SocketPath: identity.SocketPath,
		SocketName: identity.SocketName, Pane: identity.Pane, Engine: identity.Engine, ID: identity.ID,
		Source: identity.Source,
	})
	if err != nil {
		t.Fatal(err)
	}
	var proxy map[string]any
	if err := json.Unmarshal(wire, &proxy); err != nil {
		t.Fatal(err)
	}

	bridge := mcpRuntime(runtime, false)
	exits := make(chan headless.Chat, 1)
	bridge.Chat = exitRecordingVerbs{ChatVerbs: bridge.Chat, exits: exits}
	service, err := mcpserv.NewConfigured("test", io.Discard, bridge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "pfm-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	for _, tool := range []string{"whoami", "exit"} {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Meta: mcp.Meta{"pfmProxy": proxy}, Name: tool, Arguments: map[string]any{},
		})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		answer, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || !strings.Contains(string(answer), `"status":"ok"`) {
			t.Fatalf("%s answered %s (content %+v)", tool, answer, result.Content)
		}
	}
	select {
	case target := <-exits:
		if target.SeatID != seat.SeatID || target.Backend != "herdr" {
			t.Fatalf("exit target = %+v, want seat %s", target, seat.SeatID)
		}
	default:
		t.Fatal("exit answered ok without reaching the exit verb")
	}
}

// exitRecordingVerbs keeps every real chat verb but records exit's target
// instead of running the detached exit choreography on the test process.
type exitRecordingVerbs struct {
	mcpserv.ChatVerbs
	exits chan headless.Chat
}

func (verbs exitRecordingVerbs) Exit(_ context.Context, self headless.Chat) (string, error) {
	verbs.exits <- self
	return "recorded", nil
}

// stageNativeCallerProc writes a jailed /proc in which this test process runs
// under seatID's seat host (pid 900001), the ancestry the Herdr caller proof
// demands of a native seat's own process.
func stageNativeCallerProc(t *testing.T, root, seatID string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		pid, parent int
		argv        string
	}{
		{900001, 1, "/fixture/pfm\x00internal\x00seat-host\x00" + seatID + "\x00"},
		{os.Getpid(), 900001, "pfm-test\x00"},
	} {
		dir := filepath.Join(root, strconv.Itoa(entry.pid))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fields := make([]string, 24)
		for i := range fields {
			fields[i] = "0"
		}
		fields[0], fields[1], fields[19] = "S", strconv.Itoa(entry.parent), "100"
		if err := os.WriteFile(
			filepath.Join(dir, "stat"),
			[]byte(fmt.Sprintf("%d (pfm) %s", entry.pid, strings.Join(fields, " "))),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(entry.argv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
