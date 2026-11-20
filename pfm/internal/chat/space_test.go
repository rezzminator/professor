package chat

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A chat's space is recorded under the id every row of it carries (a Herdr
// seat's own session id, whichever conversation segment runs now; a tmux
// chat's session), the scan Herdr placement files chats by reads it back, and
// the line says where the chat sits; outside Herdr nothing moves.
func TestSpaceRecordsTheSpaceAndSaysWhereTheChatSits(t *testing.T) {
	herdr := resolve.Identity{Backend: "herdr", SeatID: "seat-1", ID: "segment-after-clear", Pane: "w1:p2"}
	tmux := resolve.Identity{Backend: "tmux", ID: "tmux-chat"}
	for _, test := range []struct {
		name     string
		identity resolve.Identity
		recorded string // the space the chat holds before the call
		space    string
		moveErr  error
		line     string
		err      string
		spaces   map[string]string
		moved    string // "socket id" the placement ran for; "" for none
		unheld   bool   // the scan does not hold the chat yet, so files it by its folder
		homeless bool   // the chat's folder files under no place
	}{
		{
			name: "a Herdr chat moves into its space", identity: herdr, space: " glass ",
			line:   `chat is in space "glass", Herdr workspace "glass"`,
			spaces: map[string]string{"seat-session": "glass"}, moved: "SOCKET seat-session glass",
		},
		{
			name: "clearing returns a Herdr chat to its folder place", identity: herdr, recorded: "glass",
			line:   `chat space cleared: the chat is back in its folder place, Herdr workspace "atlas"`,
			spaces: map[string]string{}, moved: "SOCKET seat-session ",
		},
		{
			name: "a chat the scan does not hold yet stays in its folder place and says so", identity: herdr,
			space: "glass", unheld: true,
			err: `space "glass" is recorded, but the fleet scan does not hold chat seat-session yet, so it stays ` +
				`in Herdr workspace "atlas"; the next placement of the chat moves it`,
			spaces: map[string]string{"seat-session": "glass"}, moved: "SOCKET seat-session ",
		},
		{
			name: "clearing a chat whose folder files under no place", identity: herdr, recorded: "glass",
			homeless: true, line: "chat space cleared: its folder files under no place, so the chat stays where it sits",
			spaces: map[string]string{}, moved: "SOCKET seat-session ",
		},
		{
			name: "home's label is refused", identity: tmux, space: "~",
			err: `the space name "~" is home's place; pick another`, spaces: map[string]string{},
		},
		{
			name: "a tmux chat records the space and does not move", identity: tmux, space: "glass",
			line: `chat space "glass" recorded: the chat is not in Herdr, so nothing moved; ` +
				`the picker files it under "glass"`,
			spaces: map[string]string{"tmux-chat": "glass"},
		},
		{
			name: "clearing a tmux chat's space", identity: tmux, recorded: "glass",
			line:   "chat space cleared: the chat is not in Herdr, so nothing moved; the picker files it by its folder",
			spaces: map[string]string{},
		},
		{
			name: "a refused move keeps the space and names the reason", identity: herdr, space: "glass",
			moveErr: errors.New("workspace_not_found"),
			err:     `space "glass" is recorded; Herdr did not move the chat: workspace_not_found`,
			spaces:  map[string]string{"seat-session": "glass"}, moved: "SOCKET seat-session glass",
		},
		{
			name: "a name with a control character is refused", identity: tmux, space: "gla\nss",
			err: `the space name "gla\nss" must be one printable line`, spaces: map[string]string{},
		},
		{
			name: "a chat with no session id yet", identity: resolve.Identity{Backend: "tmux"}, space: "glass",
			err:    "the calling chat has no session id yet; send it a first message",
			spaces: map[string]string{},
		},
		{
			name: "a Herdr caller whose seat is unknown", identity: resolve.Identity{Backend: "herdr", SeatID: "ghost"},
			space: "glass", spaces: map[string]string{},
			err: `read the calling chat's seat "ghost": no such seat`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			ctx := context.Background()
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			state := fleetdb.OpenSharedState(ctx, values)
			defer func() {
				if err := state.Close(); err != nil {
					t.Error(err)
				}
			}()
			if err := state.PutSeat(ctx, fleetdb.Seat{
				SeatID: "seat-1", SessionID: "seat-session", Engine: "cc", Name: "GLASS:BKS", Backend: "herdr",
				State: "live", HerdrSocket: "SOCKET", HerdrPane: "w1:p2", HerdrWorkspace: "w1",
			}); err != nil {
				t.Fatal(err)
			}
			if test.recorded != "" {
				chat := map[string]string{"herdr": "seat-session", "tmux": "tmux-chat"}[test.identity.Backend]
				if err := state.SetSpace(ctx, chat, test.recorded, 1); err != nil {
					t.Fatal(err)
				}
			}
			var moved, scanned string
			doors := spaceDoors{
				places: func(ctx context.Context, _ *config.Runtime) (*place.Fleet, error) {
					recorded, err := spaceNames(ctx, state)
					if test.unheld {
						recorded = map[string]string{}
					}
					scanned = recorded["seat-session"]
					return &place.Fleet{Spaces: recorded}, err
				},
				move: func(_ context.Context, _ paths.Values, socket string, fleet place.Fleet, id string) (
					place.Place, error,
				) {
					moved = socket + " " + id + " " + fleet.Spaces[id]
					switch space := fleet.Spaces[id]; {
					case space != "":
						return place.Place{Root: place.SpaceRoot(space), Label: space}, test.moveErr
					case test.homeless:
						return place.Place{}, test.moveErr
					}
					return place.Place{Root: "/fixture/atlas", Label: "atlas"}, test.moveErr
				},
			}
			line, err := spaceCaller(ctx, config.Runtime{Paths: values}, test.identity, test.space, doors)
			if got := errText(err); got != test.err || line != test.line {
				t.Fatalf("spaceCaller = %q, %q; want %q, %q", line, got, test.line, test.err)
			}
			if moved != test.moved {
				t.Fatalf("placement ran for %q, want %q", moved, test.moved)
			}
			if test.moved != "" && !test.unheld && scanned != test.spaces["seat-session"] {
				t.Fatalf("the placement scan read space %q, want the recorded %q", scanned, test.spaces["seat-session"])
			}
			spaces, err := spaceNames(ctx, state)
			if err != nil {
				t.Fatal(err)
			}
			if want := test.spaces; !maps.Equal(spaces, want) {
				t.Fatalf("recorded spaces = %v, want %v", spaces, want)
			}
		})
	}
}

// A caller no lookup can identify gets an error naming why, never a recorded
// space for a chat nobody knows.
func TestSpaceFromNoChatFailsNamingTheCaller(t *testing.T) {
	testjail.Fleet(t)
	for name, value := range map[string]string{
		"TMUX": "", "TMUX_PANE": "", "HERDR_SOCKET_PATH": filepath.Join(testjail.ShortRoot(t), "absent.sock"),
		"HERDR_PANE_ID": "", "CLAUDE_CODE_SESSION_ID": "", "CODEX_THREAD_ID": "",
	} {
		t.Setenv(name, value)
	}
	line, err := Space(context.Background(), nil, "glass")
	if want := "identify the calling chat: " + resolve.ErrNotInChat.Error(); errText(err) != want || line != "" {
		t.Fatalf("Space = %q, %q; want an error %q", line, errText(err), want)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// spaceNames is each chat's recorded space by chat id, a cleared record left out.
func spaceNames(ctx context.Context, state *fleetdb.Store) (map[string]string, error) {
	records, err := state.Spaces(ctx)
	names := make(map[string]string, len(records))
	for id, record := range records {
		if record.Space != "" {
			names[id] = record.Space
		}
	}
	return names, err
}
