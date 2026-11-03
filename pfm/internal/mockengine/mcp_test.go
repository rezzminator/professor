package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

const mcpTraceServerArg = "mockengine-mcp-trace-server"

func init() {
	if len(os.Args) < 2 || os.Args[1] != mcpTraceServerArg {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "trace-fixture", Version: "test"}, nil)
	mcp.AddTool(
		server,
		&mcp.Tool{Name: "trace"},
		func(_ context.Context, request *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			encoded, err := json.Marshal(request.Params.Meta["pfmTrace"])
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil, nil
		},
	)
	if err := server.Run(context.Background(), &mcp.IOTransport{Reader: os.Stdin, Writer: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestLocalMCPRefreshesTraceAndPreservesToolDiscovery(t *testing.T) {
	for _, listTools := range []bool{true, false} {
		t.Run(fmt.Sprint(listTools), func(t *testing.T) {
			fix := newFixture(t)
			action := filepath.Join(fix.root, "action.json")
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			proc := &process{
				ctx:      t.Context(),
				cwd:      fix.work,
				stderr:   io.Discard,
				env:      fix.env(map[string]string{obs.EnvBeat: "birth", obs.EnvRun: "birth"}),
				script:   newScript(Scenario{ActionFile: action}, engineCodex),
				recorder: recorder{dir: fix.recordDir},
			}
			for _, beat := range []string{"A", "B"} {
				if err := os.WriteFile(action, []byte(`{"beat":"`+beat+`","run":"synthetic"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				answer, err := proc.callLocalMCP(
					Step{Tool: "trace"},
					[]string{self, mcpTraceServerArg},
					os.Environ(),
					localMCPOptions{ListTools: listTools},
				)
				if err != nil {
					t.Fatal(err)
				}
				if answer == nil || answer.IsError || len(answer.Content) != 1 {
					t.Fatalf("MCP answer=%+v, want one successful trace", answer)
				}
				actual, ok := answer.Content[0].(*mcp.TextContent)
				expected := `{"beat":"` + beat + `","run":"synthetic"}`
				if !ok || actual.Text != expected {
					t.Fatalf("%s: MCP trace=%+v, want %q", action, answer.Content, expected)
				}
			}
			path := filepath.Join(fix.recordDir, "mcp-chat-professor.json")
			if listTools {
				if actual := readFile(t, path); actual != "[\"trace\"]\n[\"trace\"]\n" {
					t.Fatalf("%s: discovery=%q, want two exact trace lists", path, actual)
				}
			} else if raw, err := os.ReadFile(path); !os.IsNotExist(err) {
				t.Fatalf("%s: call-only discovery=%q err=%v, want no tools/list record", path, raw, err)
			}
		})
	}
}

func TestLocalMCPRefusesAnEmptyCommand(t *testing.T) {
	proc := &process{}
	for _, command := range [][]string{nil, {""}} {
		answer, err := proc.callLocalMCP(Step{}, command, nil, localMCPOptions{})
		if answer != nil || err == nil || err.Error() != "local MCP command must name an executable" {
			t.Fatalf("command=%v yielded answer=%+v err=%v, want explicit empty-command refusal", command, answer, err)
		}
	}
}
