package mcpserv

import (
	"context"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hostops/pfm/internal/chat"
	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/transcript"
)

// TestChatLSFindReadAdaptTheTypedVerbs pins MCP's half of chat_ls, chat_find
// and chat_read: each maps its input onto the verb's typed request, applies
// the tool's payload contract, and projects the answer onto the wire — no
// callback into package main, no second implementation of the verb.
func TestChatLSFindReadAdaptTheTypedVerbs(t *testing.T) {
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
			Matched: 5, Truncated: true, KilledCount: 1,
		},
		found: []chat.TranscriptMatch{
			{ID: "one", Path: "/t/one.jsonl", Last: "2026-01-02", Hits: 2},
			{ID: "two", Path: "/t/two.jsonl", Hits: 1},
			{ID: "three", Path: "/t/three.jsonl", Hits: 1},
		},
		read: []transcript.Entry{
			{Role: transcript.RoleUser, Text: "question"},
			{Role: transcript.RoleAssistant, Text: "a long answer"},
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
		t.Fatalf("chat_ls counts = %+v", ls)
	}
	live, killed := ls.Rows[0], ls.Rows[1]
	if live.Session != "cx-a" || live.Engine != pfmengine.Codex || live.State != "idle" ||
		live.Account != 2 || live.Pane != "%1" || live.Kind != compose.LiveCodex.String() {
		t.Fatalf("live row = %+v", live)
	}
	if killed.Session != "claude-b" || killed.State != "resumable" || !killed.Killed {
		t.Fatalf("killed resumable row = %+v, want its id as the session", killed)
	}

	_, find, err := service.chatFind(ctx, nil, FindInput{Excerpt: "a literal chat name", Limit: 2, IncludeSelf: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []chat.FindRequest{{Excerpt: "a literal chat name"}}; !reflect.DeepEqual(verbs.finds, want) {
		t.Fatalf("Find calls = %+v, want %+v", verbs.finds, want)
	}
	if find.Count != 2 || find.Candidates[0].ID != "one" || find.Candidates[0].Date != "2026-01-02" ||
		!find.Candidates[0].Confirmed || find.SelfID != "" ||
		!reflect.DeepEqual(find.Needles, []string{"a literal chat name"}) {
		t.Fatalf("chat_find = %+v, want the two best candidates and no self_id", find)
	}

	_, read, err := service.chatRead(ctx, nil, ReadInput{Source: "claude-b", MaxBytes: 12})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude-b/20"}; !reflect.DeepEqual(verbs.reads, want) {
		t.Fatalf("Read calls = %q, want %q", verbs.reads, want)
	}
	if read.ID != "claude-b" || read.Count != 1 || read.Turns[0].Role != transcript.RoleAssistant ||
		read.Bytes > 12 || !read.Truncated {
		t.Fatalf("chat_read = %+v, want the newest turn cut to the byte budget", read)
	}
}

// TestChatLSFindReadRefuseBadInputBeforeTheVerb pins the entry validation:
// contradictory or out-of-range input is refused without reaching the verb,
// and a server built without its verb layer says so instead of answering empty.
func TestChatLSFindReadRefuseBadInputBeforeTheVerb(t *testing.T) {
	verbs := &fakeChatVerbs{}
	service := newService("test", &backend{chat: verbs})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"ls all+killed": func() error { _, _, err := service.chatLS(ctx, nil, LSInput{All: true, Killed: true}); return err },
		"ls limit":      func() error { _, _, err := service.chatLS(ctx, nil, LSInput{Limit: maxChatLSLimit + 1}); return err },
		"find limit":    func() error { _, _, err := service.chatFind(ctx, nil, FindInput{Excerpt: "x", Limit: 51}); return err },
		"read last_n":   func() error { _, _, err := service.chatRead(ctx, nil, ReadInput{Source: "x", LastN: 201}); return err },
		"read bytes": func() error {
			_, _, err := service.chatRead(ctx, nil, ReadInput{Source: "x", MaxBytes: 1<<20 + 1})
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(verbs.lists)+len(verbs.finds)+len(verbs.reads) != 0 {
		t.Fatalf("a refused input reached the verb: %+v", verbs)
	}
	unwired := newService("test", &backend{})
	for name, call := range map[string]func() error{
		"chat_ls":   func() error { _, _, err := unwired.chatLS(ctx, nil, LSInput{}); return err },
		"chat_find": func() error { _, _, err := unwired.chatFind(ctx, nil, FindInput{Excerpt: "x"}); return err },
		"chat_read": func() error { _, _, err := unwired.chatRead(ctx, nil, ReadInput{Source: "x"}); return err },
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
	defer service.Close()

	if _, _, err := service.chatName(context.Background(), nil, NameInput{
		Target: "target", Name: "new name",
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"chat/name/target/new name"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dispatch args = %q, want %q", got, want)
	}
}
