package obs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestActionBeatRefreshesAfterProcessBirth(t *testing.T) {
	directory := t.TempDir()
	channel := filepath.Join(directory, "beat.json")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(channel, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"beat":"A","run":"synthetic-A"}`)
	path := filepath.Join(directory, "activity.jsonl")
	ctx, finish := OpenLog(context.Background(), Settings{
		Cmd: "picker", Args: []string{}, Path: path,
		Env: &paths.MapEnv{Values: map[string]string{EnvBeatFile: channel, EnvBeat: "A", EnvRun: "synthetic-A"}},
	})
	write(`{"beat":"B","run":"synthetic-B"}`)
	Action(ctx, "picker.quit", DoorPicker)
	if err := os.Remove(channel); err != nil {
		t.Fatal(err)
	}
	Action(ctx, "picker.open", DoorPicker)
	finish(0)
	records := readLog(t, path)
	if len(records) < 3 {
		t.Fatalf("%s: records=%v, want an action after the beat changed", path, records)
	}
	requireField(t, records[2], FieldBeat, "B")
	if len(records) != 6 {
		t.Fatalf("%s: records=%v, want start, quit, failure, open, exit", path, records)
	}
	requireField(t, records[1], FieldBeat, "A")
	requireField(t, records[2], FieldAction, "picker.quit")
	requireField(t, records[2], FieldBeat, "B")
	requireField(t, records[2], FieldRun, "synthetic-B")
	for _, record := range records[4:5] {
		requireField(t, record, FieldBeat, "unattributed")
		requireField(t, record, FieldRun, "unattributed")
	}
	if records[3].Level != "ERROR" || records[3].Message != "action.unattributed" {
		t.Fatalf("%s: failure=%v", path, records[3])
	}
	if reason, _ := records[3].Field(FieldErr); !strings.Contains(reason.(string), channel) {
		t.Fatalf("%s: error=%v does not name channel", path, reason)
	}
}

func TestActionBeatRefusesUnreadableOrMalformedChannel(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "beat.json")
	for _, content := range []string{`{`, `{}`, `{"beat":"B"}`, `{"run":"synthetic"}`, `{"beat":2,"run":"synthetic"}`} {
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		beat, run, err := readActionBeat(file)
		if beat != "unattributed" || run != "unattributed" || err == nil {
			t.Fatalf("%s: %q yielded %q, %q, %v", file, content, beat, run, err)
		}
	}
	for _, path := range []string{dir, "relative.json"} {
		beat, run, err := readActionBeat(path)
		if beat != "unattributed" || run != "unattributed" || err == nil {
			t.Fatalf("%s: yielded %q, %q, %v", path, beat, run, err)
		}
	}
}
