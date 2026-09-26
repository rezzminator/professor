package command

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestChatIndexNamesAbsentIndexNamesNothing(t *testing.T) {
	names := &chatIndexNames{ctx: context.Background(), path: filepath.Join(t.TempDir(), "pfm.db")}
	if name, err := names.nameOf("sess-1"); name != "" || err != nil {
		t.Fatalf("nameOf over an absent index = %q, %v; want blank and no error", name, err)
	}
}

func TestChatIndexNamesUnreadableIndexErrsOnEveryCall(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	names := &chatIndexNames{ctx: context.Background(), path: filepath.Join(file, "pfm.db")}
	for call := range 2 {
		if _, err := names.nameOf("sess-1"); err == nil || !strings.Contains(err.Error(), "read chat names from") {
			t.Fatalf("call %d: nameOf over an unreadable index = %v, want the read error", call+1, err)
		}
	}
	exitCode := 0
	names.close(&stderr, &exitCode)
	if exitCode != 0 {
		t.Fatalf("close with nothing opened set exit %d\nstderr:\n%s", exitCode, stderr.String())
	}
}

// TestCallmeterReportNamesChatsReadOnly: a report's name lookup reads the
// transcript index and writes nothing — no shared fleet store is created and
// the index file keeps its bytes and mtime.
func TestCallmeterReportNamesChatsReadOnly(t *testing.T) {
	root := t.TempDir()
	index := filepath.Join(root, "pfm.db")
	t.Setenv(paths.EnvDB, index)
	t.Setenv(paths.EnvFleetDB, filepath.Join(root, "seed", "fleet.db"))
	ctx := context.Background()
	seed, err := store.OpenContext(ctx)
	if err != nil {
		t.Fatalf("open seed index: %v", err)
	}
	transcript := store.Transcript{UUID: "sess-1", Path: filepath.Join(root, "sess-1.jsonl"), CustomTitle: "named chat"}
	if err := seed.UpsertTranscript(ctx, transcript); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed index: %v", err)
	}
	fleetDB := filepath.Join(root, "report", "fleet.db")
	t.Setenv(paths.EnvFleetDB, fleetDB)
	beforeInfo, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	names := &chatIndexNames{ctx: ctx, path: index}
	name, err := names.nameOf("sess-1")
	exitCode := 0
	names.close(&stderr, &exitCode)
	if name != "named chat" || err != nil || exitCode != 0 {
		t.Fatalf("nameOf = %q, %v (close exit %d); want %q and no error\nstderr:\n%s",
			name, err, exitCode, "named chat", stderr.String())
	}
	if _, err := os.Stat(fleetDB); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("report opened the fleet store %s for a name lookup (stat err = %v)", fleetDB, err)
	}
	afterInfo, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatalf("report changed the index %s: mtime %v -> %v, bytes equal %v",
			index, beforeInfo.ModTime(), afterInfo.ModTime(), bytes.Equal(beforeBytes, afterBytes))
	}
}
