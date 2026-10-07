package mcpserv

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestChatReadBudgetsAndJunkFilter(t *testing.T) {
	root := setupBackendFixture(t)
	path := filepath.Join(root, "claude", "project-alpha", "budget.jsonl")
	writeJSONL(t, path, []any{
		map[string]any{
			"type": "user", "cwd": "/work/alpha",
			"message": map[string]any{"content": "<system-reminder>killed"},
		},
		map[string]any{
			"type": "user", "cwd": "/work/alpha",
			"message": map[string]any{"content": "first visible"},
		},
		map[string]any{
			"type":    "assistant",
			"message": map[string]any{"content": strings.Repeat("z", 100)},
		},
		map[string]any{
			"type": "user", "cwd": "/work/alpha",
			"message": map[string]any{"content": "last visible"},
		},
	})
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	output := callTool[ReadOutput](t, client.clientSession, "chat_read", ReadInput{
		Source:   "budget",
		LastN:    3,
		MaxBytes: 30,
	})
	if output.Bytes > 30 || !output.Truncated {
		t.Fatalf("budget output = %+v", output)
	}
	for _, turn := range output.Turns {
		if strings.Contains(turn.Text, "system-reminder") {
			t.Fatalf("junk prompt leaked: %+v", output)
		}
	}
}
