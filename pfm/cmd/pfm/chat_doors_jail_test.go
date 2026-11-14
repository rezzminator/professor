//go:build jail

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
)

// TestChatDoorsAnswerAlike drives each refusal through both doors of one chat function, `pfm chat {verb}` and
// the chat MCP tool, from one table: the CLI exits the row's code with its stderr's last line the row's message
// behind the verb's prefix, and the MCP answers a receipt of the same status word, code and message — never a tool
// error. A name no chat answers to and a dead chat the registry knows are the two subjects.
func TestChatDoorsAnswerAlike(t *testing.T) {
	const deadID = "44444444-2222-4333-8444-555555555555"
	_, transcript := resumeLadderJail(t, "55555555-2222-4333-8444-555555555555")
	state := fleetdb.OpenSharedState(context.Background(), jailPaths(t))
	if err := state.PutSeat(context.Background(), fleetdb.Seat{
		SeatID: "dead-cx", SessionID: deadID, Engine: "cx", Backend: "herdr", State: "lost", Name: "gone-cx",
		LostNote: "fixture retired", HerdrSocket: filepath.Join(filepath.Dir(transcript), "gone.sock"),
		HerdrPane: "w1:p1", HerdrWorkspace: "w1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	statedTestSender(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	session := connectDoorsMCP(ctx, t, mcpRuntime(runtime, true))

	const unknown = "nobody-here"
	noChat := `no chat named "nobody-here"`
	deadRevive := `"gone-cx" is dead (engine cx, session ` + deadID + `); revive it with: pfm chat open gone-cx`
	cases := []struct {
		name      string
		argv      []string
		tool      string
		arguments map[string]any
		prefix    string // the CLI's stderr prefix before the shared message
		statusKey string // the MCP receipt's field holding the status word
		status    string
		code      int
		message   string
	}{
		{
			"capture unknown",
			[]string{"capture", unknown},
			"capture",
			map[string]any{"target": unknown},
			"pfm chat: ", "status", "not_found", 4, noChat,
		},
		{
			"keys unknown",
			[]string{"keys", unknown, "Escape"},
			"keys",
			map[string]any{"target": unknown, "keys": []string{"Escape"}},
			"pfm chat: ", "status", "not_found", 4, noChat,
		},
		{
			"last unknown",
			[]string{"last", unknown},
			"last",
			map[string]any{"target": unknown},
			"pfm chat: ", "status", "not_found", 4, noChat,
		},
		{
			"status unknown",
			[]string{"status", unknown},
			"status",
			map[string]any{"target": unknown},
			"pfm chat: ", "state", "not-found", 4, noChat,
		},
		{
			"call unknown",
			[]string{"call", unknown, "hi"},
			"call",
			map[string]any{"target": unknown, "message": "hi"},
			"pfm chat: ", "status", "not_found", 4, noChat,
		},
		{
			"kill unknown",
			[]string{"kill", unknown},
			"kill",
			map[string]any{"target": unknown},
			"pfm chat: ", "status", "not_found", 4, noChat,
		},
		{
			"unkill unknown",
			[]string{"unkill", unknown},
			"unkill",
			map[string]any{"target": unknown},
			"pfm chat: ", "status", "not_found", 4, noChat,
		},
		{
			"name unknown",
			[]string{"name", unknown, "zz"},
			"name",
			map[string]any{"target": unknown, "name": "zz"},
			"pfm chat: ", "status", "not_found", 4, noChat,
		},
		{
			"open unknown",
			[]string{"open", unknown},
			"open",
			map[string]any{"target": unknown},
			"pfm chat: ", "status", "not_found", 4, noChat,
		},
		{
			"resolve unknown",
			[]string{"resolve", unknown},
			"resolve",
			map[string]any{"kind": "label", "name": unknown},
			"pfm chat: ", "status", "not_found", 4, noChat,
		},
		{
			"capture dead",
			[]string{"capture", "gone-cx"},
			"capture",
			map[string]any{"target": "gone-cx"},
			"pfm chat capture: ", "status", "dead", 3, `"gone-cx" is not running`,
		},
		{
			"keys dead",
			[]string{"keys", "gone-cx", "Escape"},
			"keys",
			map[string]any{"target": "gone-cx", "keys": []string{"Escape"}},
			"pfm chat keys: ", "status", "dead", 3, `"gone-cx" is not running`,
		},
		{
			"call dead",
			[]string{"call", "gone-cx", "hi"},
			"call",
			map[string]any{"target": "gone-cx", "message": "hi"},
			"", "status", "dead", 3, deadRevive,
		},
		{
			"read dead answers as last does",
			[]string{"read", "gone-cx"},
			"last",
			map[string]any{"target": "gone-cx"},
			"pfm chat read: ", "status", "not_answered", 3, `"gone-cx" has not written a transcript yet`,
		},
		{
			"last dead",
			[]string{"last", "gone-cx"},
			"last",
			map[string]any{"target": "gone-cx"},
			"pfm chat last: ", "status", "not_answered", 3, `"gone-cx" has not written a transcript yet`,
		},
	}
	for _, testCase := range cases {
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"chat"}, testCase.argv...), &stdout, &stderr)
		lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
		if wantLine := testCase.prefix + testCase.message; code != testCase.code || lines[len(lines)-1] != wantLine {
			t.Errorf("%s: CLI exit %d, last stderr line %q; want exit %d, %q (stderr %q)",
				testCase.name, code, lines[len(lines)-1], testCase.code, wantLine, stderr.String())
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: testCase.tool, Arguments: testCase.arguments})
		if err != nil {
			t.Fatalf("%s: MCP protocol: %v", testCase.name, err)
		}
		var receipt map[string]any
		if raw, err := json.Marshal(result.StructuredContent); err != nil || json.Unmarshal(raw, &receipt) != nil {
			t.Fatalf("%s: MCP receipt %+v unreadable: %v", testCase.name, result.StructuredContent, err)
		}
		got := []any{result.IsError, receipt[testCase.statusKey], receipt["code"], receipt["message"]}
		want := []any{false, testCase.status, float64(testCase.code), testCase.message}
		for index := range want {
			if got[index] != want[index] {
				t.Errorf("%s: MCP (isError, %s, code, message) = %v, want %v (content %+v)",
					testCase.name, testCase.statusKey, got, want, result.Content)
				break
			}
		}
	}
}

// connectDoorsMCP serves the chat MCP over runtime in memory and returns a connected client session.
func connectDoorsMCP(ctx context.Context, t *testing.T, runtime mcpserv.Runtime) *mcp.ClientSession {
	t.Helper()
	service, err := mcpserv.NewConfigured("test", io.Discard, runtime)
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
	return clientSession
}

// TestChatDoorsReadALiveSeatAlike drives a live Herdr seat through both doors from one table: the state word ls
// lists (its inspection, Herdr's agent status, never the row's bare "idle"), the capture text with its trailing
// blank rows dropped and its tail counting content rows, and a seat whose pane cannot be read, refused as dead
// with one reason text.
func TestChatDoorsReadALiveSeatAlike(t *testing.T) {
	// snapshot is the header both doors print above a Herdr pane's text.
	const snapshot = "[Herdr recent snapshot limited to 1000 lines; truncated=false]\n"
	cases := []struct {
		name     string
		agent    string // the seat's Herdr agent status; "" keeps idle
		paneRead string
		argv     []string
		tool     string
		input    map[string]any
		// cliOut is the CLI's stdout, or for ls the row's state word; cliCode its exit.
		cliOut  string
		cliCode int
		// receipt holds the MCP receipt's fields this row pins.
		receipt map[string]any
	}{
		{
			"ls inspects a live seat", "working", "",
			[]string{"ls", "--all"},
			"ls",
			map[string]any{"all": true},
			"working", 0,
			map[string]any{"state": "working"},
		},
		{
			"capture drops trailing blank rows",
			"",
			`{"type":"pane_read","read":{"pane_id":"p3","workspace_id":"w6","text":"one\ntwo\nthree\n  \n\n","truncated":false}}`,
			[]string{"capture", "NATIVE"},
			"capture",
			map[string]any{"target": "NATIVE"},
			snapshot + "one\ntwo\nthree\n",
			0,
			map[string]any{"status": "ok", "code": float64(0), "text": snapshot + "one\ntwo\nthree\n"},
		},
		{
			"capture tail counts content rows",
			"",
			`{"type":"pane_read","read":{"pane_id":"p3","workspace_id":"w6","text":"one\ntwo\nthree\n  \n\n","truncated":false}}`,
			nil,
			"capture",
			map[string]any{"target": "NATIVE", "tail_lines": 2},
			"",
			0,
			map[string]any{"status": "ok", "code": float64(0), "text": "two\nthree\n", "truncated": true},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			overrides := map[string]string{}
			if testCase.agent != "" {
				overrides["agent.get"] = `{"type":"agent_info","agent":{"pane_id":"w6:p3","terminal_id":"fixture-w6:p3",` +
					`"workspace_id":"w6","tab_id":"w6:t1","focused":true,"agent":"claude","agent_status":"` +
					testCase.agent + `","state_change_seq":0,"revision":1}}`
			}
			if testCase.paneRead != "" {
				overrides["pane.read"] = testCase.paneRead
			}
			serveNativeSeat(t, overrides, nil)
			receipt := callDoorsTool(t, testCase.tool, testCase.input)
			if testCase.tool == "ls" {
				receipt = lsRowNamed(t, receipt, "NATIVE")
			}
			for key, want := range testCase.receipt {
				if receipt[key] != want {
					t.Errorf("MCP %s %s = %#v, want %#v (receipt %v)", testCase.tool, key, receipt[key], want, receipt)
				}
			}
			if testCase.argv == nil {
				return
			}
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"chat"}, testCase.argv...), &stdout, &stderr)
			got := stdout.String()
			if testCase.tool == "ls" {
				got = cliLSState(stdout.String(), "NATIVE")
			}
			if code != testCase.cliCode || got != testCase.cliOut {
				t.Errorf("CLI %v exit %d out %q, want exit %d out %q (stdout %q stderr %q)",
					testCase.argv, code, got, testCase.cliCode, testCase.cliOut, stdout.String(), stderr.String())
			}
		})
	}
	t.Run("capture of an unreadable pane", func(t *testing.T) {
		socket := serveNativeSeat(t, map[string]string{"pane.read": "error:pane w6:p3 is gone"}, nil)
		reason := fmt.Sprintf(
			"herdr capture %q on %q: herdr refused pane.read pfm-seat pane w6:p3: pane_not_found: pane w6:p3 is gone",
			"w6:p3", socket,
		)
		var stdout, stderr bytes.Buffer
		code := run([]string{"chat", "capture", "NATIVE"}, &stdout, &stderr)
		receipt := callDoorsTool(t, "capture", map[string]any{"target": "NATIVE"})
		if code != 3 || stderr.String() != "pfm chat capture: "+reason+"\n" ||
			receipt["status"] != "dead" || receipt["code"] != float64(3) || receipt["message"] != reason {
			t.Errorf("CLI exit %d stderr %q; MCP receipt %v; want exit 3, dead code 3 and reason %q",
				code, stderr.String(), receipt, reason)
		}
	})
}

// callDoorsTool calls the chat MCP tool over this jail's runtime and returns its receipt; a tool error fails.
func callDoorsTool(t *testing.T, tool string, arguments map[string]any) map[string]any {
	t.Helper()
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := connectDoorsMCP(ctx, t, mcpRuntime(runtime, true)).
		CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		t.Fatalf("MCP %s protocol: %v", tool, err)
	}
	var receipt map[string]any
	if raw, err := json.Marshal(result.StructuredContent); err != nil || json.Unmarshal(raw, &receipt) != nil {
		t.Fatalf("MCP %s receipt %+v unreadable: %v", tool, result.StructuredContent, err)
	}
	if result.IsError {
		t.Fatalf("MCP %s answered a tool error: %+v", tool, result.Content)
	}
	return receipt
}

// lsRowNamed is the ls receipt's row named name.
func lsRowNamed(t *testing.T, receipt map[string]any, name string) map[string]any {
	t.Helper()
	rows, _ := receipt["rows"].([]any)
	for _, raw := range rows {
		if row, _ := raw.(map[string]any); row["name"] == name {
			return row
		}
	}
	t.Fatalf("MCP ls lists no row %q: %v", name, receipt)
	return nil
}

// cliLSState is the state word of `pfm chat ls`'s row named name: the field after its name and handle.
func cliLSState(stdout, name string) string {
	for _, line := range strings.Split(stdout, "\n") {
		if fields := strings.Fields(line); len(fields) > 2 && fields[0] == name {
			return fields[2]
		}
	}
	return ""
}
