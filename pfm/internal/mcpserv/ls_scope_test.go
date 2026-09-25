package mcpserv

import (
	"context"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// selectingChatVerbs lists fixture rows through chat.Select, the filter the
// real chat.List applies after its scan.
type selectingChatVerbs struct {
	chat.Verbs
	rows []compose.Row
}

func (verbs *selectingChatVerbs) List(_ context.Context, request chat.ListRequest) (chat.ListResult, error) {
	return chat.Select(verbs.rows, 0, request), nil
}

// chat_ls scopes to the caller's repository when _meta.threadId resolves the
// caller, and says it listed every repository when it does not.
func TestChatLSScopesToTheResolvedCallersRepository(t *testing.T) {
	fixture := testjail.GitRepoWithWorktrees(t, "intuita")
	rows := []compose.Row{
		{
			Kind:        compose.LiveCodex,
			ID:          "thread-a",
			Name:        "a",
			Socket:      "cx-a",
			SessionName: "a",
			PaneID:      "%1",
			CWD:         fixture.Inner,
		},
		{
			Kind:        compose.LiveClaude,
			ID:          "root-chat",
			Name:        "r",
			Socket:      "cc-r",
			SessionName: "r",
			PaneID:      "%2",
			CWD:         fixture.Repo,
		},
		{
			Kind:        compose.LiveCodex,
			ID:          "thread-b",
			Name:        "b",
			Socket:      "cx-b",
			SessionName: "b",
			PaneID:      "%3",
			CWD:         fixture.Other,
		},
	}
	service := newService("test", &backend{
		chat: &selectingChatVerbs{rows: rows}, paths: paths.Values{TmuxDir: t.TempDir()},
	})
	ids := func(output LSOutput) []string {
		listed := make([]string, 0, len(output.Rows))
		for _, row := range output.Rows {
			listed = append(listed, row.ID)
		}
		return listed
	}

	caller := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"threadId": "thread-a"}}}
	_, scoped, err := service.chatLS(context.Background(), caller, LSInput{})
	if err != nil {
		t.Fatalf("chat_ls with a resolved caller: %v", err)
	}
	if got, want := ids(scoped), []string{"thread-a", "root-chat"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("chat_ls from a worktree caller listed %v, want the repo's chats %v", got, want)
	}
	if scoped.Scope != fixture.Repo || scoped.Elsewhere != 1 {
		t.Fatalf("scoped chat_ls scope=%q elsewhere=%d, want %q and 1", scoped.Scope, scoped.Elsewhere, fixture.Repo)
	}

	_, everywhere, err := service.chatLS(context.Background(), nil, LSInput{})
	if err != nil {
		t.Fatalf("chat_ls without a caller: %v", err)
	}
	if got := ids(everywhere); len(got) != 3 {
		t.Fatalf("chat_ls without a caller listed %v, want every repository", got)
	}
	if everywhere.Scope != "all repos — caller cwd unknown" || everywhere.Elsewhere != 0 {
		t.Fatalf(
			"unresolved chat_ls scope=%q elsewhere=%d, want the unknown-caller line",
			everywhere.Scope,
			everywhere.Elsewhere,
		)
	}

	_, all, err := service.chatLS(context.Background(), caller, LSInput{All: true})
	if err != nil || len(all.Rows) != 3 || all.Scope != "all repos" {
		t.Fatalf(
			"chat_ls all from a resolved caller = %d rows scope %q err %v, want every repository",
			len(all.Rows),
			all.Scope,
			err,
		)
	}
}
