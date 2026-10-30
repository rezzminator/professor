package spawn

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

func TestPlace(t *testing.T) {
	herdrSeat := resolve.Identity{
		Backend:        "herdr",
		SeatID:         "87caf6bf",
		SocketPath:     "/h/herdr.sock",
		Pane:           "wC:p24",
		HerdrWorkspace: "wC",
		Source:         "herdr-ancestry",
	}
	plain := herdrSeat
	plain.SeatID = ""
	plain.Fronted = true
	nested := plain
	nested.Fronted = false
	failure := errors.New("caller pane read failed")
	for _, tc := range []struct {
		name, requested string
		caller          resolve.Identity
		callerErr       error
		want            BackendChoice
		err             string
	}{
		{name: "explicit tmux", requested: "tmux", caller: herdrSeat, want: BackendChoice{Backend: "tmux"}},
		{name: "Herdr seat", requested: "auto", caller: herdrSeat, want: BackendChoice{Backend: "herdr", HerdrSocket: "/h/herdr.sock", HerdrPane: "wC:p24", HerdrWorkspace: "wC"}},
		{name: "own plain pane", requested: "auto", caller: plain, callerErr: resolve.ErrNotRegistered, want: BackendChoice{Backend: "herdr", HerdrSocket: "/h/herdr.sock", HerdrPane: "wC:p24", HerdrWorkspace: "wC", InPlace: true}},
		{name: "nested plain pane", requested: "auto", caller: nested, callerErr: resolve.ErrNotRegistered, want: BackendChoice{Backend: "herdr", HerdrSocket: "/h/herdr.sock", HerdrPane: "wC:p24", HerdrWorkspace: "wC"}},
		{name: "tmux caller", requested: "auto", caller: resolve.Identity{Backend: "tmux", SocketPath: "/tmp/tmux-1000/cc-1"}, want: BackendChoice{Backend: "tmux"}},
		{name: "outside", requested: "auto", callerErr: resolve.ErrNotInChat, want: BackendChoice{Backend: "tmux"}},
		{name: "explicit Herdr from tmux", requested: "herdr", caller: resolve.Identity{Backend: "tmux"}, err: "herdr backend needs a caller in a Herdr pane:"},
		{name: "explicit Herdr outside", requested: "herdr", callerErr: resolve.ErrNotInChat, err: "herdr backend needs a caller in a Herdr pane:"},
		{name: "failed read", requested: "auto", callerErr: failure, err: "place chat: caller unresolved:"},
		{name: "ambiguous", requested: "auto", callerErr: resolve.ErrAmbiguous, err: "place chat: caller unresolved:"},
		{name: "invalid", requested: "bogus", err: "backend \"bogus\": want auto, tmux or herdr"},
		{name: "empty", callerErr: resolve.ErrNotInChat, want: BackendChoice{Backend: "tmux"}},
		{name: "explicit tmux despite failure", requested: "tmux", callerErr: failure, want: BackendChoice{Backend: "tmux"}},
		{name: "incomplete native", requested: "auto", caller: resolve.Identity{Backend: "herdr"}, err: "incomplete Herdr location"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			got, err := Place(ctx, "picker", tc.requested, tc.caller, tc.callerErr)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("choice=%+v error=%v; want %q", got, err, tc.err)
				}
			} else if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("choice=%+v error=%v; want %+v", got, err, tc.want)
			}
			if tc.callerErr != nil && tc.err == "place chat: caller unresolved:" && !errors.Is(err, tc.callerErr) {
				t.Fatalf("error %v does not wrap caller error", err)
			}
			records := recorder.Records()
			if len(records) != 1 || records[0].Message != "chat.placement" || records[0].Level != "INFO" {
				t.Fatalf("placement records=%+v", records)
			}
			requested := tc.requested
			if requested == "" {
				requested = "auto"
			}
			callerError := ""
			if tc.callerErr != nil {
				callerError = tc.callerErr.Error()
			}
			for key, want := range map[string]any{"door": "picker", "requested": requested, "caller_backend": tc.caller.Backend, "caller_socket": tc.caller.SocketPath, "caller_pane": tc.caller.Pane, "caller_workspace": tc.caller.HerdrWorkspace, "caller_fronted": tc.caller.Fronted, "caller_source": tc.caller.Source, "caller_err": callerError, "chosen": got.Backend, "in_place": got.InPlace} {
				if value, ok := records[0].Field(key); !ok || value != want {
					t.Errorf("field %s=%v (%v), want %v", key, value, ok, want)
				}
			}
			if err != nil {
				if value, _ := records[0].Field("place_err"); value != err.Error() {
					t.Errorf("place_err=%v want %v", value, err)
				}
			}
		})
	}
}

func TestPlacementRejectsCallerReadFailure(t *testing.T) {
	failure := errors.New("caller pane read failed")
	choice, err := Place(context.Background(), "picker", "auto", resolve.Identity{}, failure)
	if !errors.Is(err, failure) || choice.Backend != "" {
		t.Fatalf("choice=%+v err=%v; want caller error, never tmux", choice, err)
	}
}
