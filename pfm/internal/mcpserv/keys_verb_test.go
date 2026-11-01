package mcpserv

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestChatKeysMCPRefusesANameInTheChatVerbsWords pins the MCP keys tool to the
// one chat keys verb's name refusal: the same sentence `pfm chat keys` prints,
// not a second wording that drifts from it.
func TestChatKeysMCPRefusesANameInTheChatVerbsWords(t *testing.T) {
	t.Parallel()
	service := newService("test", &backend{callEngine: captureCodeCaller{}, allowAmbientIdentity: true})
	protocol := connectInMemory(t, service.Server())
	result, err := protocol.clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "keys",
		Arguments: KeysInput{Target: "not-used", Keys: []string{"Enter", "Esc"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(result.Content)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(string(content), `\"Esc\" is not a tmux key — tmux would type it as text`) {
		t.Fatalf("keys name refusal = %s (isError %v), want the chat verb's refusal", content, result.IsError)
	}
}

// TestChatKeysMCPCapturesTheSeatItKeyed pins keys{capture:true} to the chat
// verb's read of the seat the keys went to: the screen comes from that pane,
// never from a second resolve of the target name, which could land elsewhere.
func TestChatKeysMCPCapturesTheSeatItKeyed(t *testing.T) {
	socket := filepath.Join(testjail.ShortRoot(t), "native.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var methods []string
	done := make(chan struct{})
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Error(err)
			}
			var request map[string]any
			if err := json.NewDecoder(conn).Decode(&request); err != nil {
				t.Error(err)
			}
			method, _ := request["method"].(string)
			mu.Lock()
			methods = append(methods, method)
			mu.Unlock()
			var result any = map[string]any{"type": "ok"}
			switch method {
			case "pane.list":
				result = map[string]any{"type": "pane_list", "panes": []any{testjail.HerdrPaneInfo("w6:p3")["pane"]}}
			case "pane.process_info":
				result = map[string]any{
					"type":         "pane_process_info",
					"process_info": map[string]any{"pane_id": "w6:p3", "shell_pid": 41},
				}
			case "pane.read":
				result = map[string]any{"type": "pane_read", "read": map[string]any{
					"pane_id": "w6:p3", "workspace_id": "w6", "text": "keyed screen", "truncated": false,
				}}
			}
			if err := json.NewEncoder(conn).Encode(map[string]any{"id": request["id"], "result": result}); err != nil {
				t.Error(err)
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	service := newFixtureService(t)
	seedKeysHerdrOwner(t, socket)
	service.backend.callEngine = resolveGateCaller{
		target: pfmcall.Target{Backend: "herdr", SocketPath: socket, Pane: "w6:p3"},
	}
	_, output, err := service.chatKeys(
		context.Background(), nil, KeysInput{Target: "NATIVE", Keys: []string{"Enter"}, Capture: true},
	)
	if err != nil || output.Status != "ok" || output.Count != 1 || !strings.Contains(output.Text, "keyed screen") {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("keys capture output=%+v err=%v methods=%v, want the keyed seat's screen", output, err, methods)
	}
}
