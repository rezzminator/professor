package reload

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestNativeReloadAdapterTarget(t *testing.T) {
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	socket := filepath.Join(t.TempDir(), "arbitrary.sock")
	for _, pane := range []string{"w6:p3", "w6:p4"} {
		if err := db.PutSeat(
			ctx,
			fleetdb.Seat{
				SeatID:      pane,
				Backend:     "herdr",
				Engine:      string(pfmengine.Codex),
				State:       "live",
				HerdrSocket: socket,
				HerdrPane:   pane,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name, sock, pane string
		bad              bool
	}{{"selected", socket, "w6:p3", false}, {"ambiguous", socket, "", true}, {"missing-pane", socket, "w6:p7", true}} {
		t.Run(test.name, func(t *testing.T) {
			seat, native, err := NativeTarget(ctx, values, test.sock, test.pane, &paths.MapEnv{})
			if !native || (err != nil) != test.bad ||
				(!test.bad && (seat == nil || seat.HerdrPane != test.pane || seat.Engine != string(pfmengine.Codex))) {
				t.Fatalf("seat=%+v native=%v err=%v", seat, native, err)
			}
		})
	}
	seat, err := SeatForReload(ctx, values, socket, "w6:p3")
	if err != nil || seat == nil || seat.HerdrPane != "w6:p3" {
		t.Fatalf("seat=%+v err=%v", seat, err)
	}
}

func TestNativeReloadAdapterEngine(t *testing.T) {
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	socket := filepath.Join(t.TempDir(), "arbitrary.sock")
	if err := db.PutSeat(
		ctx,
		fleetdb.Seat{
			SeatID:      "engine-adapter",
			Backend:     "herdr",
			Engine:      string(pfmengine.Codex),
			State:       "live",
			HerdrSocket: socket,
			HerdrPane:   "w6:p3",
		},
	); err != nil {
		t.Fatal(err)
	}
	got, err := EngineForTarget(ctx, values, socket, "w6:p3")
	if err != nil || got != pfmengine.Codex {
		t.Fatalf("engine=%q err=%v", got, err)
	}
}

func TestNotifyWorkerFailureDelivery(t *testing.T) {
	for _, tc := range []struct{ name, reply, want string }{
		{"shown", `{"type":"notification_show","shown":true,"reason":"shown"}`, ""},
		{"disabled", `{"type":"notification_show","shown":false,"reason":"disabled"}`, "not shown (disabled)"},
		{"busy", `{"type":"notification_show","shown":false,"reason":"busy"}`, "not shown (busy)"},
		{"rate_limited", `{"type":"notification_show","shown":false,"reason":"rate_limited"}`, "not shown (rate_limited)"},
		{"no_foreground_client", `{"type":"notification_show","shown":false,"reason":"no_foreground_client"}`, "not shown (no_foreground_client)"},
		{"wrong type", `{"type":"ok","shown":true,"reason":"shown"}`, "acknowledgement"},
		{"missing shown", `{"type":"notification_show","reason":"shown"}`, "acknowledgement"},
		{"missing reason", `{"type":"notification_show","shown":true}`, "acknowledgement"},
		{"contradictory", `{"type":"notification_show","shown":true,"reason":"disabled"}`, "acknowledgement"},
		{"wrong shown shape", `{"type":"notification_show","shown":"true","reason":"shown"}`, "decode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "pfm.db"))
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(testjail.ShortRoot(t), "n.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := listener.Close(); err != nil {
					t.Error(err)
				}
			})
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() {
					if err := conn.Close(); err != nil {
						t.Error(err)
					}
				}()
				if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Error(err)
					return
				}
				var request struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(conn).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request.Method != "notification.show" {
					t.Errorf("method=%s", request.Method)
				}
				if err := json.NewEncoder(conn).
					Encode(map[string]any{"id": request.ID, "result": json.RawMessage(tc.reply)}); err != nil {
					t.Error(err)
				}
			}()
			db := fleetdb.OpenSharedState(t.Context(), values)
			if err := db.PutSeat(
				t.Context(),
				fleetdb.Seat{
					SeatID:      "notify",
					Backend:     "herdr",
					HerdrSocket: socket,
					HerdrPane:   "w6:p3",
					State:       "live",
					Engine:      "claude",
				},
			); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			handled, err := NotifyWorkerFailure(t.Context(), values, socket, "w6:p3", "worker failed")
			if !handled || (tc.want == "" && err != nil) ||
				(tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
				t.Fatalf("handled=%v err=%v, want %q", handled, err, tc.want)
			}
		})
	}
}
