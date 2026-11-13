package workbench

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStageOpenCodeSeatPlugin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seat", "plugin.mjs")
	if err := StageOpenCodeSeatPlugin(path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Unix(100, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := StageOpenCodeSeatPlugin(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("unchanged plugin rewritten: %v, %v", info, err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := StageOpenCodeSeatPlugin(path); err != nil {
		t.Fatal(err)
	}
	replaced, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(replaced, body) {
		t.Fatalf("stale plugin not replaced: %s, %v", replaced, err)
	}
}

func TestStageOpenCodeSeatPluginWriteFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "plugin.mjs")
	if got, want := strings.ReplaceAll(
		fmt.Sprint(StageOpenCodeSeatPlugin(path)),
		blocker,
		"{blocker}",
	), "read OpenCode workbench plugin {blocker}/plugin.mjs: open {blocker}/plugin.mjs: not a directory"; got != want {
		t.Fatalf("write failure = %q, want %q", got, want)
	}
}
