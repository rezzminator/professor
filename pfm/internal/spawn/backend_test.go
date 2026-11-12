package spawn

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/herdr"
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
	tmuxPane := resolve.Identity{Backend: "tmux", SocketPath: "/tmp/tmux-1000/cc-1"}
	failure := errors.New("caller pane read failed")
	unproven := fmt.Errorf("%w: the MCP request carried no caller identity", ErrCallerUnproven)
	down := func(socket string) error {
		return fmt.Errorf("%w at %s: connect: no such file or directory", herdr.ErrServerDown, socket)
	}
	mute := errors.New("read the Herdr server at /env/herdr.sock: malformed ping result")
	running := map[string]error{"/env/herdr.sock": nil, "/rec/herdr.sock": nil}
	envDown := map[string]error{"/env/herdr.sock": down("/env/herdr.sock"), "/rec/herdr.sock": nil}
	allDown := map[string]error{"/env/herdr.sock": down("/env/herdr.sock"), "/rec/herdr.sock": down("/rec/herdr.sock")}
	envMute := map[string]error{"/env/herdr.sock": mute}
	inHerdr := BackendChoice{Backend: "herdr", HerdrSocket: "/env/herdr.sock"}
	seatChoice := BackendChoice{
		Backend: "herdr", HerdrSocket: "/h/herdr.sock", HerdrPane: "wC:p24", HerdrWorkspace: "wC",
	}
	tmux := BackendChoice{Backend: "tmux"}
	const refused = "host refused at "
	for _, tc := range []struct {
		name, door, requested string
		recorded              string
		revive                bool
		caller                resolve.Identity
		callerErr             error
		servers               map[string]error // nil: the rule must not read the server
		socketErr             error
		want                  BackendChoice
		read                  string
		err                   string
	}{
		// 1. tmux asked for in words: tmux from anywhere, Herdr running or not, no read.
		{name: "explicit tmux from a Herdr seat", requested: "tmux", caller: herdrSeat, want: tmux},
		{name: "explicit tmux from a plain Herdr pane", requested: "tmux", caller: plain, callerErr: resolve.ErrNotRegistered, want: tmux},
		{name: "explicit tmux with no caller", requested: "tmux", callerErr: ErrNoCaller, want: tmux},
		{name: "explicit tmux despite a failed read", requested: "tmux", callerErr: failure, want: tmux},
		{name: "explicit tmux, a recorded Herdr host", requested: "tmux", recorded: "/rec/herdr.sock", revive: true, callerErr: ErrNoCaller, want: tmux},
		// 2. A caller in a Herdr pane places it there, no read.
		{name: "Herdr seat", requested: "auto", caller: herdrSeat, want: seatChoice},
		{name: "revive from a Herdr seat", requested: "auto", revive: true, caller: herdrSeat, want: seatChoice},
		{name: "own plain pane", requested: "auto", caller: plain, callerErr: resolve.ErrNotRegistered, want: BackendChoice{Backend: "herdr", HerdrSocket: "/h/herdr.sock", HerdrPane: "wC:p24", HerdrWorkspace: "wC", InPlace: true}},
		{name: "nested plain pane", requested: "auto", caller: nested, callerErr: resolve.ErrNotRegistered, want: seatChoice},
		{name: "configured Herdr from a Herdr seat", requested: "herdr", caller: herdrSeat, want: seatChoice},
		{name: "incomplete Herdr location", requested: "auto", caller: resolve.Identity{Backend: "herdr"}, err: refused + "cli.new: the caller has an incomplete Herdr location"},
		// 2. Any other caller goes to Herdr when it runs: a new tab on the
		// environment's server, a revive at its recorded host first.
		{name: "no caller, a new chat", requested: "auto", callerErr: ErrNoCaller, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "no caller, a stored tmux chat", requested: "auto", revive: true, callerErr: ErrNoCaller, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "no caller, recorded Herdr host", requested: "auto", recorded: "/rec/herdr.sock", revive: true, callerErr: ErrNoCaller, servers: running, want: BackendChoice{Backend: "herdr", HerdrSocket: "/rec/herdr.sock"}, read: "running at /rec/herdr.sock"},
		{name: "recorded host down, environment's server running", requested: "auto", recorded: "/rec/herdr.sock", revive: true, callerErr: ErrNoCaller, servers: map[string]error{"/rec/herdr.sock": down("/rec/herdr.sock"), "/env/herdr.sock": nil}, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "unproven caller", requested: "auto", callerErr: unproven, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "Herdr read that no pane proves", requested: "auto", caller: herdrSeat, callerErr: resolve.ErrNotInChat, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "failed caller read", requested: "auto", callerErr: failure, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "ambiguous caller", requested: "auto", callerErr: resolve.ErrAmbiguous, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "outside Herdr, a spawn", requested: "auto", callerErr: resolve.ErrNotInChat, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "tmux pane, a spawn", requested: "auto", caller: tmuxPane, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "configured Herdr outside", requested: "herdr", callerErr: resolve.ErrNotInChat, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "empty request", callerErr: resolve.ErrNotInChat, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		// 3. No Herdr server runs: tmux, recorded in the placement record.
		{name: "outside Herdr, Herdr down", requested: "auto", callerErr: resolve.ErrNotInChat, servers: envDown, want: tmux, read: "down: " + down("/env/herdr.sock").Error()},
		{name: "no caller, Herdr down", requested: "auto", revive: true, callerErr: ErrNoCaller, servers: envDown, want: tmux, read: "down: " + down("/env/herdr.sock").Error()},
		{name: "recorded and environment's server down", requested: "auto", recorded: "/rec/herdr.sock", revive: true, callerErr: ErrNoCaller, servers: allDown, want: tmux, read: "down: " + down("/rec/herdr.sock").Error() + "; " + down("/env/herdr.sock").Error()},
		{name: "configured Herdr, Herdr down", requested: "herdr", callerErr: resolve.ErrNotInChat, servers: envDown, err: refused + "cli.new: herdr backend asked for, but " + down("/env/herdr.sock").Error(), read: "down: " + down("/env/herdr.sock").Error()},
		{name: "configured Herdr, a revive, Herdr down", requested: "herdr", revive: true, callerErr: ErrNoCaller, servers: envDown, want: tmux, read: "down: " + down("/env/herdr.sock").Error()},
		// 4. pfm cannot tell whether Herdr runs: refused, never tmux.
		{name: "a server with no usable answer", requested: "auto", callerErr: resolve.ErrNotInChat, servers: envMute, err: refused + "cli.new: cannot tell whether Herdr runs: " + mute.Error(), read: mute.Error()},
		{name: "a socket pfm cannot resolve", requested: "auto", callerErr: ErrNoCaller, socketErr: errors.New("resolve the Herdr socket: home directory: no home"), servers: map[string]error{}, err: refused + "cli.new: cannot tell whether Herdr runs: resolve the Herdr socket: home directory: no home", read: "resolve the Herdr socket: home directory: no home"},
		// 6. A launch owning a terminal proven outside Herdr stays there on tmux.
		{name: "picker outside Herdr", door: "picker", requested: "auto", callerErr: resolve.ErrNotInChat, want: tmux},
		{name: "typed claude outside Herdr", door: "hook.launch", requested: "auto", callerErr: resolve.ErrNotInChat, want: tmux},
		{name: "typed codex in a tmux pane", door: "shim.cx", requested: "auto", caller: tmuxPane, want: tmux},
		{name: "picker in a plain tmux pane", door: "picker", requested: "auto", caller: tmuxPane, callerErr: resolve.ErrNotRegistered, want: tmux},
		{name: "picker with configured Herdr", door: "picker", requested: "herdr", callerErr: resolve.ErrNotInChat, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "picker with an unproven caller", door: "picker", requested: "auto", callerErr: unproven, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "picker with no caller", door: "picker", requested: "auto", callerErr: ErrNoCaller, servers: running, want: inHerdr, read: "running at /env/herdr.sock"},
		{name: "invalid", requested: "bogus", err: "backend \"bogus\": want auto, tmux or herdr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			door := cmp.Or(tc.door, "cli.new")
			ctx, recorder := obs.Test(t)
			up := func(_ context.Context, socket string) error {
				if tc.servers == nil {
					t.Fatalf("the rule read Herdr server %s", socket)
				}
				err, known := tc.servers[socket]
				if !known {
					t.Fatalf("the rule read unknown Herdr server %s", socket)
				}
				return err
			}
			server := HerdrServer{Socket: "/env/herdr.sock", SocketErr: tc.socketErr, Up: up}
			got, err := Place(ctx, door, tc.requested, tc.recorded, tc.revive, tc.caller, tc.callerErr, server)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err || !reflect.DeepEqual(got, BackendChoice{}) {
					t.Fatalf("choice=%+v error=%v; want %q", got, err, tc.err)
				}
				if refusal := strings.HasPrefix(tc.err, refused); errors.Is(err, ErrHostRefused) != refusal ||
					FailureCode(err) != map[bool]int{true: 10, false: 1}[refusal] {
					t.Fatalf("error %v: refusal %v, exit code %d", err, !refusal, FailureCode(err))
				}
			} else if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("choice=%+v error=%v; want %+v", got, err, tc.want)
			}
			records := recorder.Records()
			if len(records) != 1 || records[0].Message != "chat.placement" || records[0].Level != "INFO" {
				t.Fatalf("placement records=%+v", records)
			}
			requested := cmp.Or(tc.requested, "auto")
			callerError := ""
			if tc.callerErr != nil {
				callerError = tc.callerErr.Error()
			}
			for key, want := range map[string]any{"door": door, "requested": requested, "caller_backend": tc.caller.Backend, "caller_socket": tc.caller.SocketPath, "caller_pane": tc.caller.Pane, "caller_workspace": tc.caller.HerdrWorkspace, "caller_fronted": tc.caller.Fronted, "caller_source": tc.caller.Source, "caller_err": callerError, "recorded_socket": tc.recorded, "revive": tc.revive, "herdr_server": tc.read, "chosen": got.Backend, "in_place": got.InPlace} {
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

func TestProvenCaller(t *testing.T) {
	herdrEnv := resolve.WhoamiEnvironment{HerdrPane: "w1:p2", HerdrSocket: "/h/herdr.sock"}
	failure := errors.New("caller pane read failed")
	for _, tc := range []struct {
		name        string
		environment resolve.WhoamiEnvironment
		caller      resolve.Identity
		err         error
		want        string // the error's text, "" for none
		unproven    bool
	}{
		{name: "plain terminal", err: resolve.ErrNotInChat, want: resolve.ErrNotInChat.Error()},
		{name: "tmux pane", caller: resolve.Identity{Backend: "tmux"}},
		{name: "proven Herdr pane", environment: herdrEnv, caller: resolve.Identity{Backend: "herdr"}},
		{
			name: "Herdr claim no pane proves", environment: herdrEnv, err: resolve.ErrNotInChat, unproven: true,
			want: "caller unproven: its environment names Herdr pane w1:p2, which its lookup did not prove (" +
				resolve.ErrNotInChat.Error() + ")",
		},
		{
			name: "tmux pane under a Herdr claim", environment: herdrEnv, caller: resolve.Identity{Backend: "tmux"},
			unproven: true,
			want:     "caller unproven: its environment names Herdr pane w1:p2, which its lookup did not prove (<nil>)",
		},
		{
			name: "engine shell no pane holds", caller: resolve.Identity{Engine: "claude", ID: "a1"},
			err: resolve.ErrNotInChat, unproven: true,
			want: "caller unproven: claude session a1 runs in no pane pfm can prove (" + resolve.ErrNotInChat.Error() + ")",
		},
		{name: "engine shell in a tmux pane", caller: resolve.Identity{Backend: "tmux", Engine: "claude", ID: "a1"}},
		{name: "failed read", err: failure, want: "caller pane read failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ProvenCaller(tc.environment, tc.caller, tc.err)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tc.want || errors.Is(err, ErrCallerUnproven) != tc.unproven {
				t.Fatalf("ProvenCaller = %q (unproven %v), want %q (unproven %v)",
					got, errors.Is(err, ErrCallerUnproven), tc.want, tc.unproven)
			}
		})
	}
}

func TestRefuseTmuxClient(t *testing.T) {
	if err := RefuseTmuxClient("tmux attach", resolve.WhoamiEnvironment{TMUX: "/tmp/tmux-1/default,1,0"}); err != nil {
		t.Fatalf("outside Herdr: %v, want no refusal", err)
	}
	err := RefuseTmuxClient("tmux attach", resolve.WhoamiEnvironment{HerdrPane: "w1:p2"})
	want := "host refused at tmux attach: this terminal is Herdr pane w1:p2, where a tmux client is never attached; " +
		"open the chat from a plain terminal"
	if err == nil || err.Error() != want || FailureCode(err) != 10 {
		t.Fatalf("in Herdr: %v (exit %d), want %q (exit 10)", err, FailureCode(err), want)
	}
}
