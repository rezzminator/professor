package mcpserv

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/inject"
)

func TestToolErrorsKeepHandlerOutput(t *testing.T) {
	const detail = "fixture failure"
	tests := []struct {
		name, tool string
		arguments  any
		backend    *backend
		prefix     string
		fields     map[string]any
	}{
		{
			name: "keys dying pane", tool: "chat_keys",
			arguments: map[string]any{"target": "gone", "keys": []string{"Escape", "Enter"}},
			backend:   &backend{injector: captureCodeInjector{}, allowAmbientIdentity: true},
			prefix:    `send "Escape": `,
			fields: map[string]any{
				"status": statusDead, "code": float64(inject.CodeDead), "count": float64(0),
				"pane": "%0", "keys": []any{"Escape", "Enter"},
			},
		},
		{
			name: "keys unknown target", tool: "chat_keys",
			arguments: map[string]any{"target": "gone", "keys": []string{"Escape"}},
			backend: &backend{
				injector:             captureCodeInjector{code: inject.CodeUnknown, detail: detail},
				allowAmbientIdentity: true,
			},
			prefix: `resolve "gone": ` + detail,
			fields: map[string]any{"status": statusNotFound, "code": float64(inject.CodeUnknown)},
		},
		{
			name: "capture failed", tool: "chat_capture",
			arguments: map[string]any{"target": "gone"},
			backend: &backend{
				injector:             captureCodeInjector{code: inject.CodeCaptureFailed, detail: detail},
				allowAmbientIdentity: true,
			},
			prefix: "chat_capture: " + detail,
			fields: map[string]any{"status": statusError, "code": float64(inject.CodeCaptureFailed), "message": detail},
		},
		{
			name: "name action failed", tool: "chat_name",
			arguments: map[string]any{"target": "fixture", "name": "new-name"},
			backend: &backend{dispatch: func(_ context.Context, _ []string, _, stderr io.Writer) int {
				_, _ = io.WriteString(stderr, detail+"\n")
				return 7
			}},
			prefix: "pfm chat name fixture new-name exited 7: " + detail,
			fields: map[string]any{"status": statusError, "code": float64(7), "message": detail},
		},
		{
			name: "ls failed", tool: "chat_ls",
			arguments: map[string]any{"all": true},
			backend:   &backend{},
			prefix:    "chat_ls verb is not configured",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newService("test", test.backend)
			session := connectInMemory(t, service.Server()).clientSession
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: test.tool, Arguments: test.arguments,
			})
			if err != nil {
				t.Fatalf("protocol error: %v", err)
			}
			if !result.IsError || len(result.Content) != 1 {
				t.Fatalf("tool result = %+v", result)
			}
			content, ok := result.Content[0].(*mcp.TextContent)
			if !ok || !strings.HasPrefix(content.Text, test.prefix) {
				t.Fatalf("content = %+v, want prefix %q", result.Content, test.prefix)
			}
			if test.fields == nil {
				return
			}
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var output map[string]any
			if err := json.Unmarshal(encoded, &output); err != nil {
				t.Fatal(err)
			}
			for field, want := range test.fields {
				got, err := json.Marshal(output[field])
				if err != nil {
					t.Fatal(err)
				}
				encodedWant, _ := json.Marshal(want)
				if !bytes.Equal(got, encodedWant) {
					t.Errorf("structuredContent.%s = %s, want %s; full = %s", field, got, encodedWant, encoded)
				}
			}
		})
	}
}
