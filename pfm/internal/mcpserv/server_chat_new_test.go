package mcpserv

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestChatNewCacheChoiceAndValidation(t *testing.T) {
	t.Parallel()
	var calls [][]string
	service := newService("test", &backend{dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
		calls = append(calls, append([]string(nil), args...))
		return 0
	}})
	for _, cache := range []string{"5m", "1h"} {
		if _, _, err := service.chatNew(context.Background(), nil, NewInput{Name: "child", Cache: cache}); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]string{
		{"chat", "new", "--name", "child", "--cache", "5m"},
		{"chat", "new", "--name", "child", "--cache", "1h"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("chat_new calls = %q, want %q", calls, want)
	}
	if _, _, err := service.chatNew(
		context.Background(), nil, NewInput{Name: "child", Cache: "on"},
	); err == nil || !strings.Contains(err.Error(), "1h|5m") {
		t.Fatalf("invalid cache error = %v", err)
	}
	if len(calls) != len(want) {
		t.Fatalf("invalid cache dispatched: %q", calls)
	}
}

func TestChatNewCacheSchemaEnum(t *testing.T) {
	t.Parallel()
	service := newService("test", &backend{})
	protocol := connectInMemory(t, service.server)
	listed, err := protocol.clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "chat_new" {
			continue
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		if got := schema.Properties["cache"].Enum; !reflect.DeepEqual(got, []string{"1h", "5m"}) {
			t.Fatalf("cache schema enum = %q", got)
		}
		return
	}
	t.Fatal("chat_new tool missing")
}

func TestChatNewCarriesTheAgentRole(t *testing.T) {
	var calls [][]string
	service := newService("test", &backend{dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
		calls = append(calls, append([]string(nil), args...))
		return 0
	}})
	input := NewInput{Name: "seat", Model: "claude-sonnet-5-5", Effort: "xhigh", AgentRole: "flights-smart-executor"}
	if _, _, err := service.chatNew(context.Background(), nil, input); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{
		"chat", "new", "--name", "seat", "--model", "claude-sonnet-5-5", "--effort", "xhigh",
		"--agent-role", "flights-smart-executor",
	}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("chat_new calls = %q, want %q", calls, want)
	}
}

// TestChatNewDescriptionRoutesEveryModelRunHere reads chat_new as a caller
// does: the description within the 600-rune tool budget, its example call
// naming only real schema fields, the launchers it replaces and the cleanup
// named, and every schema field described.
func TestChatNewDescriptionRoutesEveryModelRunHere(t *testing.T) {
	tool := listedChatNew(t)
	description := tool.Description
	if runes := utf8.RuneCountInString(description); runes > 600 {
		t.Fatalf("chat_new description is %d runes, over 600: %q", runes, description)
	}
	for _, phrase := range []string{"model run", "pfm headless exec", "claude -p", "chat_kill", "sub-agent"} {
		if !strings.Contains(description, phrase) {
			t.Fatalf("chat_new description lacks %q: %q", phrase, description)
		}
	}
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	example := regexp.MustCompile(`chat_new\{([^}]*)\}`).FindStringSubmatch(description)
	if example == nil {
		t.Fatalf("chat_new description has no example call: %q", description)
	}
	for _, key := range regexp.MustCompile(`(\w+):`).FindAllStringSubmatch(example[1], -1) {
		if _, found := schema.Properties[key[1]]; !found {
			t.Fatalf("chat_new example names %q, not a schema field: %q", key[1], example[0])
		}
	}
	for name, property := range schema.Properties {
		if strings.TrimSpace(property.Description) == "" {
			t.Fatalf("chat_new schema field %q has no description", name)
		}
	}
}

func listedChatNew(t *testing.T) *mcp.Tool {
	t.Helper()
	service := newService("test", &backend{})
	protocol := connectInMemory(t, service.server)
	listed, err := protocol.clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "chat_new" {
			return tool
		}
	}
	t.Fatal("chat_new tool missing")
	return nil
}
