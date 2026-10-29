package mcpserv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
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

// An unnamed native seat states its durable seat handle while ls lists it by
// its transcript id: the caller must still find its own row, or exit and
// whoami refuse a live chat as "matched no live chat".
func TestHerdrProxyFindsAnUnnamedSeatBySeatID(t *testing.T) {
	row := compose.Row{
		Kind: compose.LiveClaude, ID: "sid1", Backend: "herdr", SeatID: "seat1", HerdrWorkspace: "w6",
		Socket: "/jail/native/herdr.sock", PaneID: "w6:p3", CWD: "/jail/work",
	}
	current := &backend{
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		paths: paths.Values{TmuxDir: "/jail/tmux"},
	}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": resolve.SeatPrefix + "seat1", "backend": "herdr",
			"seatId": "seat1", "herdrWorkspace": "w6", "socketPath": row.Socket, "socketName": "herdr.sock",
			"pane": row.PaneID, "engine": "claude", "id": "sid1",
		},
	})
	if err != nil || !caller.valid || caller.row.ID != "sid1" || caller.identity.SeatID != "seat1" {
		t.Fatalf("caller=%+v err=%v", caller, err)
	}
}

func TestHerdrLossNoteIsNotBackendFailure(t *testing.T) {
	for _, test := range []struct{ note, failure, state string }{
		{note: "your last message was not answered — re-send it", state: "lost"},
		{failure: "HERDR UNREACHABLE own socket", state: "error"},
	} {
		row := compose.Row{
			Kind:         compose.ResumeClaude,
			ID:           "sid",
			Backend:      "herdr",
			LostNote:     test.note,
			BackendError: test.failure,
		}
		current := &backend{chat: &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}}}
		listed, err := current.list(context.Background(), LSInput{})
		if err != nil || len(listed.Rows) != 1 {
			t.Fatalf("list=%+v err=%v", listed, err)
		}
		data, err := json.Marshal(listed.Rows[0])
		if err != nil {
			t.Fatal(err)
		}
		var output map[string]any
		if err := json.Unmarshal(data, &output); err != nil {
			t.Fatal(err)
		}
		if output["state"] != test.state || (test.note != "" && output["lost_note"] != test.note) ||
			(test.failure != "" && output["backend_error"] != test.failure) {
			t.Errorf("native row=%s", data)
		}
		status := statusOutput(headless.Status{Name: "sid", State: "lost", LostNote: test.note, Error: test.failure})
		data, err = json.Marshal(status)
		if err != nil {
			t.Fatal(err)
		}
		output = nil
		if err := json.Unmarshal(data, &output); err != nil {
			t.Fatal(err)
		}
		if test.note != "" && output["lost_note"] != test.note {
			t.Errorf("native status lost note: %s", data)
		}
	}
}

// TestHerdrCodexThreadMetaBindsNativeSeat pins that a Codex chat seated on
// Herdr signs its own calls: _meta.threadId binds the native row, its
// absolute Herdr socket kept as-is (never judged as a tmux socket name), and
// the identity carries the Herdr backend, seat and workspace.
func TestHerdrCodexThreadMetaBindsNativeSeat(t *testing.T) {
	row := compose.Row{
		Kind:           compose.LiveCodex,
		ID:             "thread-native",
		Name:           "SMOKE_CX",
		SessionName:    "SMOKE_CX",
		Backend:        "herdr",
		SeatID:         "seat-cx",
		HerdrWorkspace: "w7",
		Socket:         "/jail/native/herdr.sock",
		PaneID:         "w7:pW",
		CWD:            "/jail/work",
	}
	current := &backend{
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		paths: paths.Values{TmuxDir: "/jail/tmux"},
	}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{"threadId": "thread-native"})
	if err != nil || !caller.valid {
		t.Fatalf("Herdr Codex caller = %+v err=%v, want a valid binding", caller, err)
	}
	identity := caller.identity
	if identity.SocketPath != row.Socket || identity.SocketName != "herdr.sock" || identity.Pane != row.PaneID ||
		identity.Session != "SMOKE_CX" || identity.ID != "thread-native" || identity.Engine != "cx" ||
		identity.Backend != "herdr" || identity.SeatID != "seat-cx" || identity.HerdrWorkspace != "w7" ||
		identity.Source != "mcp-thread-meta" {
		t.Fatalf("identity = %+v, want the native Herdr seat", identity)
	}
}

func TestHerdrCodexThreadMetaSignsCallAndWhoami(t *testing.T) {
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

func TestHerdrCodexThreadMetaNamesBackendFailure(t *testing.T) {
	row := compose.Row{
		Kind: compose.LiveCodex, ID: "thread-native", SessionName: "Native",
		Backend: "herdr", Socket: "/jail/native/herdr.sock", PaneID: "w7:pW",
		BackendError: "HERDR UNREACHABLE own socket",
	}
	current := &backend{
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		paths: paths.Values{TmuxDir: "/jail/tmux"},
	}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{"threadId": row.ID})
	if err != nil || caller.valid || !caller.present || !strings.Contains(caller.detail, row.BackendError) {
		t.Fatalf("backend failure caller = %+v err=%v", caller, err)
	}
}

// TestHerdrCodexThreadMetaRejectsRelativeNativeSocket pins that a native row
// whose socket is not absolute stays unbound with its reason, never signed.
func TestHerdrCodexThreadMetaRejectsRelativeNativeSocket(t *testing.T) {
	row := compose.Row{
		Kind: compose.LiveCodex, ID: "thread-native", SessionName: "SMOKE_CX",
		Backend: "herdr", Socket: "herdr.sock", PaneID: "w7:pW",
	}
	current := &backend{
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		paths: paths.Values{TmuxDir: "/jail/tmux"},
	}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{"threadId": "thread-native"})
	if err != nil || caller.valid || !caller.present || !strings.Contains(caller.detail, "not absolute") {
		t.Fatalf("relative native socket caller = %+v err=%v, want a present invalid binding", caller, err)
	}
}
