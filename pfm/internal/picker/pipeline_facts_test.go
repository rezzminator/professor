package picker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/rowfacts"
)

const openTurnTranscript = `{"type":"assistant","message":{"model":"claude-opus-5-5","role":"assistant","content":[],"stop_reason":"tool_use"}}` + "\n"

func factsFixture(t *testing.T) compose.Output {
	t.Helper()
	transcript := filepath.Join(t.TempDir(), "live.jsonl")
	if err := os.WriteFile(transcript, []byte(openTurnTranscript), 0o600); err != nil {
		t.Fatal(err)
	}
	return compose.Output{Rows: []compose.Row{{Kind: compose.LiveClaude, ID: "live", Name: "live", Path: transcript}}}
}

func TestBuildSnapshotEnrichesRowsOnlyWhenAFactsReaderIsSet(t *testing.T) {
	output := factsFixture(t)
	env := fleet.Env{NowNS: statNowNS(t, output.Rows[0].Path)}
	with := buildSnapshot(context.Background(), env, scanRequest{Facts: rowfacts.NewReader(t.TempDir())}, output)
	if got := with.Rows[0]; got.Model != "claude-opus-5-5" || !got.Working {
		t.Fatalf("the interactive snapshot lacks the row's facts: %+v", got)
	}
	if output.Rows[0].Model != "" || output.Rows[0].Working {
		t.Error("buildSnapshot wrote the composed output")
	}
	without := buildSnapshot(context.Background(), env, scanRequest{}, output)
	if got := without.Rows[0]; got.Model != "" || got.Working || got.AgentsWorking != 0 {
		t.Errorf("a scripted listing must stay as composed: %+v", got)
	}
}

func TestBuildSnapshotNamesAFactsFailureInsteadOfShowingNoFacts(t *testing.T) {
	output := factsFixture(t)
	sid := t.TempDir()
	if err := os.WriteFile(filepath.Join(sid, "statusline-effort-live"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := buildSnapshot(context.Background(), fleet.Env{NowNS: statNowNS(t, output.Rows[0].Path)},
		scanRequest{Facts: rowfacts.NewReader(sid)}, output)
	if got, want := strings.ReplaceAll(
		snapshot.FactsError,
		sid,
		"{sid}",
	), "1 unreadable, first: row \"live\": parse {sid}/statusline-effort-live: invalid character 'b' looking for beginning of object key string"; got != want {
		t.Fatalf("FactsError = %q, want %q", got, want)
	}
}

func statNowNS(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime().UnixNano()
}
