package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSeatRequest(t *testing.T) {
	for _, tc := range []struct {
		method string
		params map[string]any
	}{
		{"pane.get", map[string]any{"pane_id": "w6:p3"}},
		{"agent.get", map[string]any{"target": "w6:p3"}},
		{"agent.prompt", map[string]any{"target": "w6:p3", "text": "hello"}},
		{"pane.read", map[string]any{"pane_id": "w6:p3", "source": "recent", "lines": float64(80)}},
		{"pane.send_keys", map[string]any{"pane_id": "w6:p3", "keys": []any{"Enter", "Escape", "C-c", "Down", "Tab"}}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			socket := serve(t, func(line string) string {
				var got struct {
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if err := json.Unmarshal([]byte(line), &got); err != nil {
					t.Error(err)
				}
				if got.Method != tc.method || !reflect.DeepEqual(got.Params, tc.params) {
					t.Errorf("request = %+v, want %s %+v", got, tc.method, tc.params)
				}
				return `{"result":{"type":"seat_test"}}`
			})
			result, err := SeatRequest(context.Background(), socket, tc.method, tc.params)
			if err != nil || string(result) != `{"type":"seat_test"}` {
				t.Fatalf("SeatRequest = %s,%v", result, err)
			}
		})
	}
	socket := serve(t, func(string) string { return `{"error":{"code":"pane_not_found","message":"missing pane"}}` })
	_, err := SeatRequest(context.Background(), socket, "pane.get", map[string]string{"pane_id": "w6:p3"})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != "pane_not_found" || refusal.Message != "missing pane" {
		t.Fatalf("refusal = %v", err)
	}
}

func TestSeatWaitUsesCallerDeadline(t *testing.T) {
	socket := serve(t, func(string) string {
		<-time.After(1100 * time.Millisecond)
		return `{"result":{"type":"agent_waited","agent_status":"idle"}}`
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := SeatRequest(
		ctx,
		socket,
		"agent.wait",
		map[string]any{"target": "w6:p3", "timeout_ms": 2000, "until": []string{"idle", "done", "blocked"}},
	)
	if err != nil || len(got) == 0 {
		t.Fatalf("wait beyond reporter timeout = %s,%v", got, err)
	}
	t.Run("cancellation after acceptance", func(t *testing.T) {
		accepted, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		socket := serve(t, func(string) string {
			close(accepted)
			<-release
			return `{"result":{"type":"agent_waited","agent_status":"idle"}}`
		})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := SeatRequest(ctx, socket, "agent.wait", map[string]any{"target": "w6:p3"})
			done <- err
		}()
		select {
		case <-accepted:
			cancel()
		case <-time.After(5 * time.Second):
			t.Fatal("wait request never reached server")
		}
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled wait = %v, want context.Canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("accepted wait ignored cancellation until its long deadline")
		}
	})
}

func TestSeatRecentReadDeadline(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		bound        time.Duration
		ok           bool
	}{
		{"history", "recent", 3 * time.Second, true}, {"caller-bound", "recent", 30 * time.Millisecond, false}, {"visible-stays-short", "visible", 3 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			socket := serve(t, func(string) string {
				select {
				case <-time.After(1100 * time.Millisecond):
				case <-release:
				}
				return `{"result":{"type":"pane_read"}}`
			})
			ctx, cancel := context.WithTimeout(context.Background(), tc.bound)
			defer cancel()
			started := time.Now()
			got, err := SeatRequest(ctx, socket, "pane.read", map[string]any{"pane_id": "w1:p1", "source": tc.source})
			if (err == nil) != tc.ok || (tc.ok && len(got) == 0) {
				t.Fatalf("read beyond 1s got=%s err=%v want ok=%t", got, err, tc.ok)
			}
			if tc.name == "caller-bound" && time.Since(started) > 500*time.Millisecond {
				t.Fatalf("history read ignored shorter caller deadline: %v", time.Since(started))
			}
		})
	}
}

func TestSeatEncodedRequestLimit(t *testing.T) {
	const limit = 1024 * 1024
	for _, unit := range []string{"x", "\x00", "界", "<&\""} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("%x/%d", unit, extra), func(t *testing.T) {
				params := map[string]string{"target": "w1:p1", "text": ""}
				prefix, err := json.Marshal(request{ID: "pfm-seat", Method: "agent.prompt", Params: params})
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(unit)
				if err != nil {
					t.Fatal(err)
				}
				width := len(encoded) - 2
				room := limit - len(prefix)
				params["text"] = strings.Repeat(unit, room/width) + strings.Repeat("x", room%width+extra)
				var calls atomic.Int32
				socket := serve(t, func(line string) string {
					calls.Add(1)
					if len(line) != limit {
						t.Errorf("wire envelope=%d want %d", len(line), limit)
					}
					return `{"result":{"type":"ok"}}`
				})
				_, err = SeatRequest(context.Background(), socket, "agent.prompt", params)
				if extra == 0 {
					if err != nil || calls.Load() != 1 {
						t.Fatalf("exact limit refused: calls=%d err=%v", calls.Load(), err)
					}
				} else {
					if err == nil || !strings.Contains(err.Error(), "request") || calls.Load() != 0 {
						t.Fatalf("oversized envelope sent: calls=%d err=%v", calls.Load(), err)
					}
				}
			})
		}
	}
}
