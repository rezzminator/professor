package mcpserv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
