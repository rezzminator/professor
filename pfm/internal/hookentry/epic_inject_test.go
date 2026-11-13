package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestEpicInjectDedupeFollowsSessionAndEpicRename(t *testing.T) {
	database, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	epicInjectStore = database
	t.Cleanup(func() {
		epicInjectStore = nil
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"alpha", "beta"} {
		path := filepath.Join(root, "docs", "epics", slug, "manifest.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("manifest "+slug+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{
		"transcript_path": filepath.Join(root, "transcript.jsonl"), "cwd": filepath.Join(root, "src", "nested"),
	})
	if err != nil {
		t.Fatal(err)
	}
	window := "E_alpha_chat"
	oldIdentify, oldWindow := epicInjectIdentify, epicInjectWindowName
	epicInjectIdentify = func(context.Context) (resolve.Identity, error) {
		return resolve.Identity{ID: "session-a", SocketPath: "/jail/cc", Pane: "%1"}, nil
	}
	epicInjectWindowName = func(context.Context, resolve.Identity) (string, error) { return window, nil }
	t.Cleanup(func() { epicInjectIdentify, epicInjectWindowName = oldIdentify, oldWindow })
	call := func() string {
		var stdout, stderr bytes.Buffer
		if code := EpicInject(bytes.NewReader(payload), &stdout, &stderr); code != 0 {
			t.Fatalf("epic inject code=%d stderr=%q", code, stderr.String())
		}
		return stdout.String()
	}
	if first := call(); !strings.Contains(first, "INJECTED EPIC alpha/manifest.md") ||
		!strings.Contains(first, "manifest alpha") {
		t.Fatalf("first injection = %q", first)
	}
	if second := call(); second != "" {
		t.Fatalf("same epic injected twice: %q", second)
	}
	window = "E_beta_chat"
	if third := call(); !strings.Contains(third, "INJECTED EPIC beta/manifest.md") {
		t.Fatalf("renamed epic injection = %q", third)
	}
	window = "E_alpha_chat"
	if fourth := call(); fourth != "" {
		t.Fatalf("renamed-back epic injected twice: %q", fourth)
	}
}

// TestEpicInjectNativeWindowNameIsTheSeatName holds I1: a native Herdr chat's
// window name is its seat name, read without a tmux call against the Herdr
// socket.
func TestEpicInjectNativeWindowNameIsTheSeatName(t *testing.T) {
	identity := resolve.Identity{
		Backend:    "herdr",
		SocketPath: filepath.Join(t.TempDir(), "herdr.sock"),
		Pane:       "w1:p2",
		Session:    "E_alpha_native",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	window, err := epicInjectWindowName(ctx, identity)
	if err != nil || window != "E_alpha_native" {
		t.Fatalf("window name = %q, %v; want the seat name %q and no error", window, err, "E_alpha_native")
	}
}
