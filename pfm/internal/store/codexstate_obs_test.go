package store

import (
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestReadCodexThreadsRecordsForeignReadsUnderTheDBComponent: the Codex
// state store is read through the db door — kind=codex records for the
// column probe and the threads read; the first user message (a prompt) in
// the store never reaches the file.
func TestReadCodexThreadsRecordsForeignReadsUnderTheDBComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	buildCodexState(t, filepath.Join(root, "state_4.sqlite"), codexStateThread{
		ID: "thread-1", RolloutPath: "/codex/sessions/rollout.jsonl", CWD: "/work", Name: "n",
		FirstUserMessage: "PLANTED first prompt", ThreadSource: "user", CreatedAt: 100, UpdatedAt: 100,
	})
	if _, err := ReadCodexThreads(ctx, []string{filepath.Join(root, "state_4.sqlite")}); err != nil {
		t.Fatal(err)
	}
	var pragma, threads int
	for _, record := range recorder.Records() {
		kind, _ := record.Field("kind")
		if record.Message != "db.statement" || kind != "codex" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v", comp)
		}
		switch op, _ := record.Field("op"); op {
		case "pragma":
			pragma++
		case "select":
			if table, _ := record.Field("table"); table != "threads" {
				t.Fatalf("table = %v, want threads", table)
			}
			threads++
		}
	}
	if pragma != 1 || threads != 1 {
		t.Fatalf("pragma=%d threads=%d, want one each: %s", pragma, threads, recorder.Raw())
	}
	if strings.Contains(recorder.Raw(), "PLANTED") {
		t.Fatalf("store content reached the file: %s", recorder.Raw())
	}
}
