package call

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestHerdrKeysCapabilities(t *testing.T) {
	supported := map[string]string{
		"Enter":  "Enter",
		"C-c":    "ctrl+c",
		"C-d":    "ctrl+d",
		"C-M-a":  "ctrl+alt+a",
		"S-Tab":  "shift+Tab",
		"Escape": "Escape",
		"Space":  "Space",
		"Tab":    "Tab",
		"BTab":   "shift+tab",
		"BSpace": "backspace",
		"Up":     "Up",
		"Down":   "Down",
		"Left":   "Left",
		"Right":  "Right",
		"C-+":    "ctrl+plus",
		"M--":    "alt+minus",
	}
	for i := 1; i <= 12; i++ {
		key := fmt.Sprintf("F%d", i)
		supported[key] = key
	}
	for key, want := range supported {
		t.Run(key, func(t *testing.T) {
			socket := newNativeSocket(
				t,
				func(nativeRequest) (any, string) { return map[string]string{"type": "ok"}, "" },
			)
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2"}
			if err := nativeEngine(t, target).SendKey(context.Background(), target, key); err != nil {
				t.Fatal(err)
			}
			calls := socket.calls()
			if len(calls) != 1 || !reflect.DeepEqual(calls[0].Params["keys"], []any{want}) {
				t.Fatalf("translated key: %+v want %s", calls, want)
			}
		})
	}
	for _, key := range []string{"Delete", "DC", "Insert", "IC", "Home", "End", "PageUp", "PPage", "PageDown", "NPage", "PgUp", "PgDn", "C-Home", "not-a-key"} {
		t.Run(key, func(t *testing.T) {
			socket := newNativeSocket(
				t,
				func(nativeRequest) (any, string) { return map[string]string{"type": "ok"}, "" },
			)
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2"}
			err := nativeEngine(t, target).SendKey(context.Background(), target, key)
			if err == nil || !strings.Contains(err.Error(), "Herdr") {
				t.Fatalf("unsupported key %s: %v", key, err)
			}
			if len(socket.calls()) != 0 {
				t.Fatal("unsupported key reached wire")
			}
		})
	}
}
