package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHerdrSpawnReservesStructuredRecipeAndCleansRefusal(t *testing.T) {
	for _, mode := range []string{"tab", "same-pane", "refused", "malformed", "occupied-pane", "occupied-bind"} {
		t.Run(mode, func(t *testing.T) {
			testjail.Fleet(t)
			oldClock := clock.Real
			fakeNow := oldClock.Now()
			clock.Real = clock.NewFake(fakeNow)
			t.Cleanup(func() { clock.Real = oldClock })
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			requests := make(chan map[string]any, 1)
			go func() {
				conn, req, err := acceptBirthRequest(listener)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				requests <- req
				if mode == "malformed" {
					_, _ = fmt.Fprintln(conn, "not-json")
					return
				}

				if mode == "refused" {
					_, _ = fmt.Fprintf(
						conn,
						`{"id":%q,"ok":false,"error":{"code":"layout_refused","message":"no room"}}`+"\n",
						req["id"],
					)
					return
				}
				_, _ = fmt.Fprintf(
					conn,
					`{"id":%q,"ok":true,"result":{"layout":{"workspace_id":"w6","root":{"type":"pane","pane_id":"w6:p9"}}}}`+"\n",
					req["id"],
				)
			}()
			binary := filepath.Join(values.Home, "native-claude")
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			request := HerdrRequest{
				Paths:  values,
				Choice: BackendChoice{Backend: "herdr", HerdrSocket: socket, HerdrPane: "w6:p3", HerdrWorkspace: "w6"},
				Seat:   fleetdb.Seat{SeatID: "seat1", SessionID: "sid1", Engine: "cc", Name: "REVIEW"},
				Run: fleetdb.SeatRun{
					Binary: binary,
					Args:   []string{"a b", "quoted'arg"},
					Env:    []string{"SAFE=value"},
					CWD:    "/fixture/work",
				},
			}
			if strings.HasPrefix(mode, "occupied-") {
				state := fleetdb.OpenSharedState(context.Background(), values)
				pane := "w6:p3"
				if mode == "occupied-bind" {
					pane = "w6:p9"
				}
				err := state.PutSeat(
					context.Background(),
					fleetdb.Seat{
						SeatID:      "owner",
						Backend:     "herdr",
						State:       "live",
						HerdrSocket: socket,
						HerdrPane:   pane,
					},
				)
				closeErr := state.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("seed owner: %v %v", err, closeErr)
				}
			}
			var got HerdrResult
			if mode == "same-pane" || mode == "occupied-pane" {
				got, err = PrepareHerdr(context.Background(), request)
			} else {
				got, err = SpawnHerdr(context.Background(), request)
			}
			state := fleetdb.OpenSharedState(context.Background(), values)
			defer func() {
				if err := state.Close(); err != nil {
					t.Error(err)
				}
			}()
			seat, found, readErr := state.Seat(context.Background(), "seat1")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.HasPrefix(mode, "occupied-") {
				if err == nil || !strings.Contains(err.Error(), "already owned") {
					t.Fatalf("occupied pane result=%+v seat=%+v err=%v", got, seat, err)
				}
				if mode == "occupied-bind" {
					if !found || seat.HerdrPane != "" {
						t.Fatalf("bind conflict lost unbound reservation: %+v %v", seat, found)
					}
				} else if found {
					t.Fatalf("reservation on occupied pane persisted: %+v", seat)
				}
				owner, ok, ownerErr := state.Seat(context.Background(), "owner")
				if ownerErr != nil || !ok || owner.State != "live" {
					t.Fatalf("owner changed: %+v %v %v", owner, ok, ownerErr)
				}
				return
			}
			if mode == "malformed" {
				if err == nil || !found {
					t.Fatalf("ambiguous creation lost reservation: result=%+v found=%v err=%v", got, found, err)
				}
				return
			}
			if mode == "refused" {
				if err == nil || !strings.Contains(err.Error(), "layout_refused") ||
					!strings.Contains(err.Error(), "no room") ||
					found {
					t.Fatalf("got=%+v err=%v found=%v", got, err, found)
				}
				_, runErr := state.ReadSeatRun("seat1")
				if !errors.Is(runErr, os.ErrNotExist) {
					t.Fatalf("recipe read err=%v", runErr)
				}
				return
			}
			wantPane := "w6:p9"
			if mode == "same-pane" {
				wantPane = "w6:p3"
			}
			if err != nil || !found || seat.UpdatedAt != fakeNow.Unix() || seat.Backend != "herdr" ||
				seat.HerdrPane != wantPane ||
				got.Seat.SeatID != "seat1" ||
				got.Eval != "exec pfm internal seat-host seat1" {
				t.Fatalf("got=%+v seat=%+v found=%v err=%v", got, seat, found, err)
			}
			run, err := state.ReadSeatRun("seat1")
			if err != nil || !reflect.DeepEqual(run, request.Run) {
				t.Fatalf("run=%+v err=%v", run, err)
			}
			if mode == "tab" {
				req := <-requests
				params := req["params"].(map[string]any)
				root := params["root"].(map[string]any)
				if req["method"] != "layout.apply" || params["workspace_id"] != "w6" ||
					params["tab_label"] != "REVIEW" ||
					params["focus"] != false ||
					!reflect.DeepEqual(root["command"], []any{"pfm", "internal", "seat-host", "seat1"}) {
					t.Fatalf("request=%+v", req)
				}
			}
		})
	}
}

func TestHerdrBirthResolvesRealEngineBeforeReservation(t *testing.T) {
	for _, mode := range []string{"tab", "same-pane"} {
		for _, resolution := range []string{"configured", "version", "path", "missing"} {
			t.Run(mode+"/"+resolution, func(t *testing.T) {
				testjail.Fleet(t)
				values, err := paths.Resolve()
				if err != nil {
					t.Fatal(err)
				}
				managed := filepath.Join(values.Home, ".local", "bin", "claude")
				engineBinary := filepath.Join(values.Home, "real", "claude")
				if resolution == "version" {
					engineBinary = filepath.Join(values.Home, ".local", "share", "claude", "versions", "2.1.270")
				}
				for _, path := range []string{managed, engineBinary} {
					if path == engineBinary && resolution == "missing" {
						continue
					}
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := testjail.WriteExecutable(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv(
					"PATH",
					filepath.Dir(managed)+string(os.PathListSeparator)+filepath.Join(values.Home, "real"),
				)
				socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
				listener, err := net.Listen("unix", socket)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
				go func() {
					conn, req, err := acceptBirthRequest(listener)
					if err != nil {
						return
					}
					defer func() { _ = conn.Close() }()
					_, _ = fmt.Fprintf(
						conn,
						`{"id":%q,"ok":true,"result":{"layout":{"workspace_id":"w6","root":{"type":"pane","pane_id":"w6:p9"}}}}`+"\n",
						req["id"],
					)
				}()
				binary := "claude"
				if resolution == "configured" {
					binary = engineBinary
				}
				request := HerdrRequest{
					Paths: values,
					Choice: BackendChoice{
						Backend:        "herdr",
						HerdrSocket:    socket,
						HerdrPane:      "w6:p3",
						HerdrWorkspace: "w6",
					},
					Seat: fleetdb.Seat{SeatID: "native-resolution", SessionID: "sid", Engine: "cc"},
					Run: fleetdb.SeatRun{
						Binary:     binary,
						Args:       []string{"exact prompt"},
						Env:        []string{"SAFE=value"},
						CWD:        values.Home,
						CostLedger: true,
					},
				}
				var got HerdrResult
				if mode == "tab" {
					got, err = SpawnHerdr(context.Background(), request)
				} else {
					got, err = PrepareHerdr(context.Background(), request)
				}
				db := fleetdb.OpenSharedState(context.Background(), values)
				defer func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				}()
				if resolution == "missing" {
					if err == nil || !strings.Contains(err.Error(), "claude binary not found") ||
						got.Seat.SeatID != "" {
						t.Fatalf("missing engine result=%+v err=%v", got, err)
					}
					if _, found, err := db.Seat(context.Background(), request.Seat.SeatID); err != nil || found {
						t.Fatalf("failed birth row found=%v err=%v", found, err)
					}
					if _, err := db.ReadSeatRun(request.Seat.SeatID); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("failed birth recipe err=%v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				run, err := db.ReadSeatRun(got.Seat.SeatID)
				want := request.Run
				want.Binary = engineBinary
				if err != nil || !reflect.DeepEqual(run, want) {
					t.Fatalf("persisted native recipe=%+v err=%v; want=%+v", run, err, want)
				}
			})
		}
	}
}

func TestHerdrRawBirthResolvesActualEngineBeforeReservation(t *testing.T) {
	for _, engineID := range []pfmengine.ID{pfmengine.Codex, pfmengine.OpenCode} {
		for _, mode := range []string{"configured", "path", "missing"} {
			t.Run(string(engineID)+"/"+mode, func(t *testing.T) {
				testjail.Fleet(t)
				values, err := paths.Resolve()
				if err != nil {
					t.Fatal(err)
				}
				binary := filepath.Join(values.Home, "real", pfmengine.MustLookup(engineID).Binary)
				if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
					t.Fatal(err)
				}
				if mode != "missing" {
					if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("PATH", filepath.Dir(binary))
				word := binary
				if mode != "configured" {
					word = pfmengine.MustLookup(engineID).Binary
				}
				request := HerdrRequest{
					Paths: values,
					Choice: BackendChoice{
						Backend:        BackendHerdr,
						HerdrSocket:    birthPingSocket(t),
						HerdrPane:      "w6:p3",
						HerdrWorkspace: "w6",
					},
					Seat: fleetdb.Seat{SeatID: "raw-seat", SessionID: "provisional", Engine: string(engineID)},
					Run:  fleetdb.SeatRun{Binary: word, CWD: values.Home, Args: []string{"a b", "a'quote"}},
				}
				result, err := PrepareHerdr(context.Background(), request)
				db := fleetdb.OpenSharedState(context.Background(), values)
				defer func() { _ = db.Close() }()
				if mode == "missing" {
					if err == nil || !strings.Contains(err.Error(), "resolve native") {
						t.Fatalf("missing raw binary result=%+v err=%v", result, err)
					}
					if _, found, err := db.Seat(context.Background(), "raw-seat"); err != nil || found {
						t.Fatalf("missing birth row found=%v err=%v", found, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				run, err := db.ReadSeatRun(result.Seat.SeatID)
				want := request.Run
				want.Binary = binary
				if err != nil || !reflect.DeepEqual(run, want) {
					t.Fatalf("raw actual recipe=%+v want=%+v err=%v", run, want, err)
				}
			})
		}
	}
}

func TestHerdrBirthRefusesProtocolBeforeReservation(t *testing.T) {
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(testjail.ShortRoot(t), "protocol.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req map[string]any
		if json.NewDecoder(conn).Decode(&req) != nil {
			return
		}
		_, _ = fmt.Fprintf(
			conn,
			`{"id":%q,"ok":true,"result":{"type":"pong","version":"0.9.5","protocol":23}}`+"\n",
			req["id"],
		)
	}()
	binary := filepath.Join(values.Home, "real-engine")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = PrepareHerdr(
		context.Background(),
		HerdrRequest{
			Paths:  values,
			Choice: BackendChoice{Backend: BackendHerdr, HerdrSocket: socket, HerdrPane: "w6:p3", HerdrWorkspace: "w6"},
			Seat:   fleetdb.Seat{SeatID: "protocol-seat", SessionID: "sid", Engine: "cc"},
			Run:    fleetdb.SeatRun{Binary: binary, CWD: values.Home},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "seats disabled") {
		t.Fatalf("protocol23 birth error=%v", err)
	}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	if _, found, err := db.Seat(context.Background(), "protocol-seat"); err != nil || found {
		t.Fatalf("protocol refusal reservation found=%v err=%v", found, err)
	}
	if _, err := db.ReadSeatRun("protocol-seat"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("protocol refusal recipe=%v", err)
	}
}

func acceptBirthRequest(listener net.Listener) (net.Conn, map[string]any, error) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return nil, nil, err
		}
		var req map[string]any
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
		if req["method"] != "ping" {
			return conn, req, nil
		}
		_, err = fmt.Fprintf(
			conn,
			`{"id":%q,"ok":true,"result":{"type":"pong","version":"0.9.5","protocol":22}}`+"\n",
			req["id"],
		)
		_ = conn.Close()
		if err != nil {
			return nil, nil, err
		}
	}
}

func birthPingSocket(t *testing.T) string {
	t.Helper()
	socket := filepath.Join(testjail.ShortRoot(t), "ping.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, _, err := acceptBirthRequest(listener)
		if err == nil {
			_ = conn.Close()
		}
	}()
	return socket
}
