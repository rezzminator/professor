package mcpserv

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestHerdrProxyRequesterCarriesItsOwnBackend(t *testing.T) {
	t.Run("unreachable native caller", func(t *testing.T) {
		row := compose.Row{
			SessionName: "REVIEW", Kind: compose.LiveClaude,
			Backend: "herdr", BackendError: "HERDR UNREACHABLE own socket",
		}
		current := &backend{chat: &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}}}
		caller, err := current.callerForRequest(context.Background(), mcp.Meta{
			"pfmProxy": map[string]any{"v": ProxyWireVersion, "session": "REVIEW"},
		})
		if err != nil || !caller.present || caller.valid || !strings.Contains(caller.detail, row.BackendError) {
			t.Fatalf("native lookup lost backend failure: %+v err=%v", caller, err)
		}
	})

	row := compose.Row{
		Kind:           compose.LiveClaude,
		ID:             "sid1",
		Name:           "REVIEW",
		SessionName:    "REVIEW",
		Backend:        "herdr",
		SeatID:         "seat1",
		HerdrWorkspace: "w6",
		Socket:         "/jail/native/herdr.sock",
		PaneID:         "w6:p3",
		CWD:            "/jail/work",
	}
	current := &backend{
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		paths: paths.Values{TmuxDir: "/jail/tmux"},
	}
	caller, err := current.callerForRequest(
		context.Background(),
		mcp.Meta{
			"pfmProxy": map[string]any{
				"v":          ProxyWireVersion,
				"session":    "REVIEW",
				"id":         "sid1",
				"socketPath": row.Socket,
				"socketName": "herdr.sock",
				"pane":       row.PaneID,
				"engine":     "claude",
			},
		},
	)
	if err != nil || !caller.valid || caller.identity.Backend != "herdr" || caller.identity.SeatID != "seat1" ||
		caller.identity.HerdrWorkspace != "w6" {
		t.Fatalf("caller=%+v err=%v", caller, err)
	}
	service := &Service{backend: current}
	self, err := service.resolvedSelf(context.Background(), caller)
	if err != nil || self.Backend != "herdr" || self.Socket != row.Socket || self.Pane != row.PaneID ||
		self.SeatID != "seat1" ||
		self.HerdrWorkspace != "w6" {
		t.Fatalf("self=%+v err=%v", self, err)
	}
}
