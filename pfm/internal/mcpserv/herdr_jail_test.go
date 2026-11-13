//go:build jail

package mcpserv

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHerdrCodexThreadMetaSignsCallAndWhoami(t *testing.T) {
	testjail.TmuxServer(t)
	service := metadataIdentityService(t)
	verbs := service.backend.chat.(*fakeChatVerbs)
	row := &verbs.listed.Rows[0]
	row.Backend = "herdr"
	row.SeatID = "seat-cx"
	row.HerdrWorkspace = "w7"
	row.Socket = "/jail/native/herdr.sock"
	row.PaneID = "w7:pW"
	client := connectInMemory(t, service.Server())
	meta := mcp.Meta{"threadId": "thread-a"}
	whoami := callToolWithMeta[WhoamiOutput](t, client.clientSession, "whoami", meta, WhoamiInput{})
	if whoami.Status != "ok" || whoami.SocketPath != row.Socket || whoami.Pane != row.PaneID ||
		whoami.ID != row.ID || whoami.Engine != "cx" {
		t.Fatalf("native whoami = %+v", whoami)
	}
	raw := callToolWithMeta[ResolveOutput](t, client.clientSession, "resolve", meta, ResolveInput{
		Kind: "session", Name: "%2",
	})
	if raw.Code == 0 || raw.SocketPath != "" || !strings.Contains(raw.Candidates, "Herdr") {
		t.Errorf("native caller resolved a tmux-only raw pane: %+v", raw)
	}
	output := callToolWithMeta[CallOutput](t, client.clientSession, "call", meta, CallInput{
		Target: "busy-session", Message: "native caller signature",
	})
	if output.Code != 0 || !output.Typed || output.Unsigned ||
		!strings.Contains(output.Proof, "sid thread-a") ||
		!strings.Contains(output.Proof, `to reply: call "Codex A" <message>`) {
		t.Fatalf("native caller lost signed explicit delivery: %+v", output)
	}
}
