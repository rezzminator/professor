package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestOpenNativeFocusAcknowledgement(t *testing.T) {
	// Pinned pane.focus producer envelope with its required identity fields.
	const producer = `{"type":"pane_info","pane":{"terminal_id":"fixture-w1:p2","workspace_id":"w1","tab_id":"w1:t2","pane_id":"w1:p2","focused":true}}`
	const agentProducer = `{"type":"agent_info","agent":{"terminal_id":"fixture-w6:p3","agent_status":"idle","workspace_id":"w1","tab_id":"w1:t2","pane_id":"w1:p2","focused":true,"state_change_seq":0,"revision":1}}`
	for _, tc := range []struct {
		name, body, refusal, diagnostic string
		agent                           bool
	}{
		{name: "agent-producer", body: agentProducer, agent: true},
		{name: "agent-unfocused", body: strings.Replace(agentProducer, `"focused":true`, `"focused":false`, 1), agent: true, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "agent-mismatched-pane", body: strings.Replace(agentProducer, `"pane_id":"w1:p2"`, `"pane_id":"w1:p9"`, 1), agent: true, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "producer", body: producer},
		{name: "legacy", body: `{"type":"pane_focused","pane_id":"w1:p2"}`},
		{name: "missing-pane-object", body: `{"type":"pane_info"}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "null-pane", body: `{"type":"pane_info","pane":null}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "missing-pane", body: `{"type":"pane_info","pane":{"focused":true}}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "missing-focused", body: `{"type":"pane_info","pane":{"pane_id":"w1:p2"}}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "unfocused", body: strings.Replace(producer, `"focused":true`, `"focused":false`, 1), diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "mismatched-pane", body: strings.Replace(producer, `"pane_id":"w1:p2"`, `"pane_id":"w1:p9"`, 1), diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "wrong-type", body: strings.Replace(producer, `"type":"pane_info"`, `"type":"agent_info"`, 1), diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "malformed-payload", body: `"malformed"`, diagnostic: "decode Herdr focus"},
		{name: "malformed-focused", body: strings.Replace(producer, `"focused":true`, `"focused":"true"`, 1), diagnostic: "decode Herdr focus"},
		{name: "top-level-spoof", body: `{"type":"pane_info","pane_id":"w1:p2","focused":true}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "refused", refusal: "focus_refused", diagnostic: "focus_refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if value, code, ok := nativeChatProof(req, "w1:p2"); ok {
					return value, code
				}
				switch req.Method {
				case "agent.focus":
					if string(req.Params["target"]) != `"w1:p2"` {
						t.Errorf("agent focus target=%s", req.Params["target"])
					}
					if tc.agent {
						return json.RawMessage(tc.body), tc.refusal
					}
					return nil, "agent_not_found"
				case "pane.focus":
					if string(req.Params["pane_id"]) != `"w1:p2"` {
						t.Errorf("focus target=%s", req.Params["pane_id"])
					}
					return json.RawMessage(tc.body), tc.refusal
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat.HerdrPane = "w1:p2"
			seat = nativeChatHost(t, db, seat, true)
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w1:p9")
			var stdout, stderr bytes.Buffer
			code := OpenID(context.Background(), seat.SessionID, &stdout, &stderr, &runtime)
			if tc.diagnostic == "" {
				if code != 0 || stderr.Len() != 0 {
					t.Fatalf("valid focus acknowledgement: code=%d stderr=%s", code, stderr.String())
				}
			} else if code != 1 || !strings.Contains(stderr.String(), tc.diagnostic) {
				t.Fatalf(
					"invalid focus acknowledgement: code=%d stderr=%s want=%q",
					code,
					stderr.String(),
					tc.diagnostic,
				)
			}
		})
	}
}

func TestDetachedNativeLiveViewReleasesConversationLock(t *testing.T) {
	testjail.Fleet(t)
	socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
		value, code, _ := nativeChatProof(req, "w6:p3")
		return value, code
	})
	runtime, db, seat := nativeChatSeat(t, socket, "live")
	seat = nativeChatHost(t, db, seat, true)
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_SOCKET_PATH", "")
	dir := t.TempDir()
	marker := filepath.Join(dir, "view-ready")
	fifo := filepath.Join(dir, "view-release")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf ready > %q\nread line < %q\n", marker, fifo)
	if err := testjail.WriteExecutable(filepath.Join(dir, "herdr"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := detachedNative(ctx, seat, io.Discard, runtime, true); done <- err }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("view never attached")
		case <-ticker.C:
		}
	}
	lockctx, lockcancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	unlock, lockerr := fleetdb.LockSeat(lockctx, runtime.Paths, seat.SessionID)
	lockcancel()
	if lockerr == nil {
		if err := unlock(); err != nil {
			t.Error(err)
		}
	}
	if err := os.WriteFile(fifo, []byte("done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if lockerr != nil {
		t.Fatalf("live attach held the recovery lock: %v", lockerr)
	}
}
