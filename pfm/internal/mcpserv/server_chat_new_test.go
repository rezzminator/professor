package mcpserv

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestChatNewCacheChoiceAndValidation(t *testing.T) {
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
