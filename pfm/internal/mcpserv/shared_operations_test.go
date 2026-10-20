package mcpserv

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestChatLSAdaptTheTypedVerbs pins MCP's half of ls: each maps its input onto the verb's typed request, applies
// the tool's payload contract, and projects the answer onto the wire — no
// callback into package main, no second implementation of the verb.
func TestChatLSAdaptTheTypedVerbs(t *testing.T) {
	t.Parallel()
	verbs := &fakeChatVerbs{
		listed: chat.ListResult{
			Rows: []compose.Row{
				{
					ID:          "thread-a",
					Kind:        compose.LiveCodex,
					SessionName: "cx-a",
					Socket:      "cx-a",
					PaneID:      "%1",
					Accounts:    []int{2},
				},
				{ID: "claude-b", Kind: compose.ResumeClaude, Killed: true},
			},
			Herdr:   []string{"w6:p2 .professor idle pfm-1", "not in herdr"},
			Matched: 5, Truncated: true, KilledCount: 1,
		},
	}
	service := newService("test", &backend{chat: verbs})
	ctx := context.Background()

	_, ls, err := service.chatLS(ctx, nil, LSInput{All: true, Project: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []chat.ListRequest{
		{View: compose.AllView, Project: "alpha", Limit: defaultChatLSLimit},
	}; !reflect.DeepEqual(
		verbs.lists,
		want,
	) {
		t.Fatalf("List calls = %+v, want %+v", verbs.lists, want)
	}
	if ls.Count != 2 || ls.Matched != 5 || !ls.Truncated || ls.KilledCount != 1 || ls.Filter != "alpha" {
		t.Fatalf("ls counts = %+v", ls)
	}
	live, killed := ls.Rows[0], ls.Rows[1]
	if live.Session != "cx-a" || live.Engine != pfmengine.Codex || live.State != "idle" ||
		live.Account != 2 || live.Pane != "%1" || live.Kind != compose.LiveCodex.String() {
		t.Fatalf("live row = %+v", live)
	}
	if killed.Session != "claude-b" || killed.State != "resumable" || !killed.Killed {
		t.Fatalf("killed resumable row = %+v, want its id as the session", killed)
	}
	if live.Herdr != "w6:p2 .professor idle pfm-1" || killed.Herdr != "not in herdr" {
		t.Fatalf("herdr presence = %q and %q, want each row's own presence string", live.Herdr, killed.Herdr)
	}
	encoded, err := json.Marshal(live)
	if err != nil || !strings.Contains(string(encoded), `"herdr":"w6:p2 .professor idle pfm-1"`) {
		t.Fatalf("live row JSON = %s, %v, want the herdr field", encoded, err)
	}
}

// TestChatLSRefuseBadInputBeforeTheVerb pins the entry validation:
// contradictory or out-of-range input is refused without reaching the verb,
// and a server built without its verb layer says so instead of answering empty.
func TestChatLSRefuseBadInputBeforeTheVerb(t *testing.T) {
	t.Parallel()
	verbs := &fakeChatVerbs{}
	service := newService("test", &backend{chat: verbs})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"ls all+killed": func() error { _, _, err := service.chatLS(ctx, nil, LSInput{All: true, Killed: true}); return err },
		"ls limit":      func() error { _, _, err := service.chatLS(ctx, nil, LSInput{Limit: maxChatLSLimit + 1}); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(verbs.lists) != 0 {
		t.Fatalf("a refused input reached the verb: %+v", verbs)
	}
	unwired := newService("test", &backend{})
	for name, call := range map[string]func() error{
		"ls": func() error { _, _, err := unwired.chatLS(ctx, nil, LSInput{}); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), name+" verb is not configured") {
			t.Errorf("%s without verbs error = %v", name, err)
		}
	}
}

func TestChatMCPDispatchesStatefulActionsInProcess(t *testing.T) {
	setupBackendFixture(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	service, err := NewConfigured("test", nil, Runtime{
		Paths: resolved,
		Dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
			got = append(got, filepath.Join(args...))
			return 0
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()

	if _, _, err := service.chatName(context.Background(), nil, NameInput{
		Target: "target", Name: "new name",
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"chat/name/target/new name"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dispatch args = %q, want %q", got, want)
	}
}
