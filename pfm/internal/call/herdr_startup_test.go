package call

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Native startup may expose an unknown agent before its composer is ready.
// Delivery waits only before input, and a stuck startup still refuses explicitly.
func TestHerdrCallWaitsForNativeBirthReadiness(t *testing.T) {
	for _, stage := range []string{"agent", "composer", "stuck", "read-failed"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("TMUX_TMPDIR", t.TempDir())
			reads := 0
			socket := newNativeSocket(t, func(req nativeRequest) (any, string) {
				switch req.Method {
				case "agent.get":
					if stage != "composer" {
						reads++
					}
					if stage == "read-failed" {
						return nil, "broken_read"
					}
					state := "idle"
					if stage == "stuck" || stage == "agent" && reads < 3 {
						state = "unknown"
					}
					return nativeAgent("agent_info", state), ""
				case "pane.read":
					if stage == "composer" {
						reads++
					}
					if stage == "composer" && reads < 3 {
						return nativePaneRead(req, "Starting the engine..."), ""
					}
					return nativePaneRead(req, "❯ "), ""
				case "agent.prompt":
					if reads < 3 {
						t.Errorf("input before native birth readiness: %d reads", reads)
					}
					return nativeAgent("agent_prompted", "working"), ""
				default:
					return nil, "unexpected_method"
				}
			})
			target := Target{
				Backend:    "herdr",
				SeatID:     "birth-seat",
				SocketPath: socket.path,
				Pane:       "w1:p2",
				Engine:     "cc",
			}
			engine := nativeEngine(t, target)
			engine.options.ComposerWait, engine.options.Poll = 20*time.Millisecond, time.Millisecond
			engine.options.Clock = &birthPublishClock{Clock: clock.Real, now: clock.Real.Now()}
			result, err := engine.Call(context.Background(), Request{Target: "new-chat", Message: "birth brief"})
			switch stage {
			case "stuck":
				if err != nil || result.Code != CodeUndelivered || result.Typed ||
					!strings.Contains(result.Message, "startup") {
					t.Fatalf("stuck birth must refuse without typing: result=%+v err=%v", result, err)
				}
			case "read-failed":
				if err == nil || !strings.Contains(err.Error(), "broken_read") || result.Typed {
					t.Fatalf("read failure became absence/readiness: result=%+v err=%v", result, err)
				}
			default:
				if err != nil || result.Code != 0 || !result.Typed {
					t.Fatalf("delivery after native birth: result=%+v err=%v", result, err)
				}
			}
			prompts := 0
			for _, req := range socket.calls() {
				if req.Method == "agent.prompt" {
					prompts++
				}
			}
			want := 1
			if stage == "stuck" || stage == "read-failed" {
				want = 0
			}
			if prompts != want {
				t.Fatalf("submitted %d prompts, want %d", prompts, want)
			}
		})
	}
}

// The durable reservation can precede publication of the canonical pane.
func TestHerdrCallWaitsForPanePublication(t *testing.T) {
	for _, publish := range []bool{true, false} {
		t.Run(fmt.Sprint(publish), func(t *testing.T) {
			t.Setenv("TMUX_TMPDIR", t.TempDir())
			socket := newNativeSocket(t, func(req nativeRequest) (any, string) {
				if req.Method == "pane.read" {
					return nativePaneRead(req, "❯ "), ""
				}
				if req.Method == "agent.get" {
					return nativeAgent("agent_info", "idle"), ""
				}
				return nativeAgent("agent_prompted", "working"), ""
			})
			target := Target{
				Backend:    "herdr",
				SeatID:     "birth-seat",
				SocketPath: socket.path,
				Pane:       "w1:p2",
				Engine:     "cc",
			}
			engine := nativeEngine(t, target)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			seat, found, err := db.Seat(context.Background(), target.SeatID)
			if err != nil || !found {
				t.Fatalf("birth seat: found=%t err=%v", found, err)
			}
			published := seat
			seat.HerdrPane, seat.HerdrTerminalID = "", ""
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			target.Pane = ""
			engine.names = nativeNames{target: target}
			engine.options.ComposerWait, engine.options.Poll = 3*time.Millisecond, time.Millisecond
			clk := &birthPublishClock{Clock: clock.Real, now: clock.Real.Now()}
			clk.publish = func() {
				if publish {
					if err := db.PutSeat(context.Background(), published); err != nil {
						t.Error(err)
					}
				}
			}
			engine.options.Clock = clk
			result, err := engine.Call(context.Background(), Request{Target: "birth-chat", Message: "birth brief"})
			if publish {
				if err != nil || result.Code != 0 || !result.Typed || result.Pane != "w1:p2" {
					t.Fatalf("pane publication: result=%+v err=%v", result, err)
				}
			} else if err != nil || result.Code != CodeUndelivered || result.Typed || !strings.Contains(result.Message, "publish") {
				t.Fatalf("unpublished pane must refuse: result=%+v err=%v", result, err)
			}
		})
	}
}

type birthPublishClock struct {
	clock.Clock
	now     time.Time
	publish func()
}

func (c *birthPublishClock) Now() time.Time { return c.now }
func (c *birthPublishClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(d)
	if c.publish != nil {
		publish := c.publish
		c.publish = nil
		publish()
	}
	return nil
}

func TestHerdrStartupGoneFailsFast(t *testing.T) {
	for _, stage := range []string{"pane", "native"} {
		t.Run(stage, func(t *testing.T) {
			socket := newNativeSocket(t, func(_ nativeRequest) (any, string) {
				return nativeAgent("agent_info", "unknown"), ""
			})
			target := Target{
				Backend:    "herdr",
				SeatID:     "birth-seat",
				SocketPath: socket.path,
				Pane:       "w1:p2",
				Engine:     "cc",
			}
			engine := nativeEngine(t, target)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			clk := &birthPublishClock{Clock: clock.Real, now: clock.Real.Now()}
			engine.options.Clock = clk
			engine.options.ComposerWait, engine.options.Poll = 20*time.Millisecond, time.Millisecond
			if stage == "pane" {
				seat, _, err := db.Seat(context.Background(), target.SeatID)
				if err != nil {
					t.Fatal(err)
				}
				seat.HerdrPane, seat.HerdrTerminalID = "", ""
				if err := db.PutSeat(context.Background(), seat); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(filepath.Join(values.ProcRoot, "42")); err != nil {
					t.Fatal(err)
				}
				target.Pane = ""
			} else {
				clk.publish = func() {
					if err := db.DeleteSeat(context.Background(), target.SeatID); err != nil {
						t.Error(err)
					}
				}
			}
			engine.names = nativeNames{target: target}
			start := clk.Now()
			result, err := engine.Call(context.Background(), Request{Target: "birth-chat", Message: "brief"})
			if err != nil || result.Code != CodeUndelivered || result.Typed || result.Status != statusUndelivered {
				t.Fatalf("gone %s: result=%+v err=%v", stage, result, err)
			}
			if elapsed := clk.Now().Sub(start); elapsed > engine.options.Poll {
				t.Fatalf("gone %s waited %s instead of failing on proof", stage, elapsed)
			}
		})
	}
}
