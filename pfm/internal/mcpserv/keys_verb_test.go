package mcpserv

import (
	"context"
	"encoding/json"
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
	if !result.IsError ||
		string(
			content,
		) != "[{\"type\":\"text\",\"text\":\"\\\"Esc\\\" is not a tmux key — tmux would type it as text; send it literal (--literal, MCP literal=true) to mean that, or use one of: BSpace BTab DC Delete Down End Enter Escape Home IC Insert Left NPage PPage PageDown PageUp PgDn PgUp Right Space Tab Up F1-F12, each optionally prefixed C- M- S-\"}]" {
		t.Fatalf("keys name refusal = %s (isError %v), want the chat verb's refusal", content, result.IsError)
	}
}
