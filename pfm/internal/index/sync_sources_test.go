package index

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// unreadableWarnings returns the index.unreadable records naming path, as
// "kind|err" lines.
func unreadableWarnings(recorder *obs.Recorder, path string) []string {
	var found []string
	for _, record := range recorder.Records() {
		if record.Message != "index.unreadable" || record.Level != "WARN" {
			continue
		}
		if named, _ := record.Field("path"); named != path {
			continue
		}
		kind, _ := record.Field("kind")
		err, _ := record.Field(obs.FieldErr)
		found = append(found, kind.(string)+"|"+err.(string))
	}
	return found
}

// unparsedLineCounts returns the count of every index.unparsed_lines record
// naming path.
func unparsedLineCounts(recorder *obs.Recorder, path string) []any {
	var counts []any
	for _, record := range recorder.Records() {
		if record.Message != "index.unparsed_lines" || record.Level != "WARN" {
			continue
		}
		if named, _ := record.Field("path"); named != path {
			continue
		}
		count, _ := record.Field("count")
		counts = append(counts, count)
	}
	return counts
}

// TestClaudePassKeepsGoingPastAnUnreadableTranscript: one transcript the
// kernel refuses to open (a socket where a file was) costs that transcript
// only: its prior row stays, its sibling is indexed, and the pass warns.
func TestClaudePassKeepsGoingPastAnUnreadableTranscript(t *testing.T) {
	root := testjail.ShortRoot(t) // a socket path must fit sun_path
	broken := filepath.Join(root, "p", "broken.jsonl")
	sibling := filepath.Join(root, "p", "sibling.jsonl")
	userLine := func(text string) string {
		return `{"type":"user","cwd":"/work","message":{"content":"` + text + `"}}`
	}
	writeLines(t, broken, userLine("kept from before"))
	writeLines(t, sibling, userLine("first"))
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx, recorder := obs.Test(t)
	if err := SyncClaude(ctx, database, []string{root}, &Counters{}); err != nil {
		t.Fatalf("first SyncClaude() error = %v", err)
	}

	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", broken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	writeLines(t, sibling, userLine("first"), userLine("second"))
	if err := SyncClaude(ctx, database, []string{root}, &Counters{}); err != nil {
		t.Fatalf("SyncClaude() over an unreadable transcript error = %v, want the pass to go on", err)
	}

	rows, err := database.Transcripts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prompts := map[string]string{}
	for _, row := range rows {
		prompts[row.UUID] = row.LastPrompt
	}
	if len(prompts) != 2 || prompts["broken"] != "kept from before" || prompts["sibling"] != "second" {
		t.Fatalf("transcript last prompts = %v, want broken kept from before and sibling second", prompts)
	}
	// open(2) of a socket fails ENXIO on Linux, EOPNOTSUPP on macOS.
	refusal := map[string]string{
		"linux":  "no such device or address",
		"darwin": "operation not supported on socket",
	}[runtime.GOOS]
	want := `claude transcript|open "` + broken + `": open ` + broken + ": " + refusal
	if got := unreadableWarnings(recorder, broken); len(got) != 1 || got[0] != want {
		t.Fatalf("warnings for %s = %q, want [%q]", broken, got, want)
	}
}

// TestCodexPassKeepsGoingPastAnUnreadableRollout: a compressed rollout whose
// bytes no longer decode costs that rollout only; its prior row stays listed.
func TestCodexPassKeepsGoingPastAnUnreadableRollout(t *testing.T) {
	codexHome := t.TempDir()
	day := filepath.Join(codexHome, "sessions", "2026", "09", "23")
	meta := `{"type":"session_meta","payload":{"source":"cli","thread_source":"user","cwd":"/work/app"}}`
	prompt := func(text string) string {
		return `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
			text + `"}]}}`
	}
	plain := filepath.Join(day, "rollout-2026-09-23T12-07-24-broken.jsonl")
	writeLines(t, plain, meta, prompt("kept from before"))
	broken, err := codexmeta.CompressRollout(plain)
	if err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(day, "rollout-2026-09-23T12-08-00-sibling.jsonl")
	writeLines(t, sibling, meta, prompt("first"))
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx, recorder := obs.Test(t)
	if err := SyncCodex(ctx, database, []string{codexHome}, &Counters{}); err != nil {
		t.Fatalf("first SyncCodex() error = %v", err)
	}

	if err := os.WriteFile(broken, []byte("not zstd at all, a torn write"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLines(t, sibling, meta, prompt("first"), prompt("second"))
	if err := SyncCodex(ctx, database, []string{codexHome}, &Counters{}); err != nil {
		t.Fatalf("SyncCodex() over an unreadable rollout error = %v, want the pass to go on", err)
	}

	for _, want := range []struct {
		id     string
		prompt string
		count  int64
	}{{"broken", "kept from before", 1}, {"sibling", "first", 2}} {
		rollout, found, err := database.Rollout(ctx, want.id)
		if err != nil || !found || rollout.FirstPrompt != want.prompt || rollout.PromptCount != want.count {
			t.Errorf("Rollout(%q) = %#v found=%t err=%v, want first prompt %q and %d prompts",
				want.id, rollout, found, err, want.prompt, want.count)
		}
	}
	want := `codex rollout|read "` + broken + `": invalid input: magic number mismatch`
	if got := unreadableWarnings(recorder, broken); len(got) != 1 || got[0] != want {
		t.Fatalf("warnings for %s = %q, want [%q]", broken, got, want)
	}
}

// TestCodexPassKeepsRowsWhenTheRolloutWalkFails: a sessions tree the walk
// cannot resolve is warned, and the pass prunes no row it could not look for.
func TestCodexPassKeepsRowsWhenTheRolloutWalkFails(t *testing.T) {
	codexHome := t.TempDir()
	sessions := filepath.Join(codexHome, "sessions")
	writeLines(t, filepath.Join(sessions, "rollout-2026-09-23T12-07-24-kept.jsonl"),
		`{"type":"session_meta","payload":{"source":"cli","thread_source":"user","cwd":"/work/app"}}`)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx, recorder := obs.Test(t)
	if err := SyncCodex(ctx, database, []string{codexHome}, &Counters{}); err != nil {
		t.Fatalf("first SyncCodex() error = %v", err)
	}

	if err := os.RemoveAll(sessions); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sessions", sessions); err != nil { // a loop: EvalSymlinks fails
		t.Fatal(err)
	}
	if err := SyncCodex(ctx, database, []string{codexHome}, &Counters{}); err != nil {
		t.Fatalf("SyncCodex() over an unwalkable sessions tree error = %v, want the pass to go on", err)
	}
	if _, found, err := database.Rollout(ctx, "kept"); err != nil || !found {
		t.Fatalf("Rollout(kept) found=%t err=%v, want the row kept", found, err)
	}
	want := `codex rollouts|resolve Codex rollout root "` + sessions + `": EvalSymlinks: too many links`
	if got := unreadableWarnings(recorder, codexHome); len(got) != 1 || got[0] != want {
		t.Fatalf("warnings for %s = %q, want [%q]", codexHome, got, want)
	}
}

// TestCodexPassKeepsRowsWhenTheStateStoreIsUnreadable: a state store with no
// readable generation costs that home's reconcile only: the rollouts still
// index, and a row only the store vouched for is not pruned.
func TestCodexPassKeepsRowsWhenTheStateStoreIsUnreadable(t *testing.T) {
	codexHome := t.TempDir()
	sessions := filepath.Join(codexHome, "sessions")
	meta := `{"type":"session_meta","payload":{"source":"cli","thread_source":"user","cwd":"/work/app"}}`
	prompt := func(text string) string {
		return `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
			text + `"}]}}`
	}
	rollout := filepath.Join(sessions, "rollout-2026-09-23T12-07-24-file.jsonl")
	writeLines(t, rollout, meta, prompt("first"))
	statePath := filepath.Join(codexHome, "state_5.sqlite")
	buildCodexState(t, statePath, codexStateThread{
		ID:          "store-only",
		RolloutPath: filepath.Join(sessions, "rollout-2026-09-23T12-08-00-store-only.jsonl"),
		CWD:         "/work/paginated", FirstUserMessage: "paginated first prompt",
		ThreadSource: "user", HistoryMode: "paginated", CreatedAt: 300, UpdatedAt: 400, TokensUsed: 99,
	})
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx, recorder := obs.Test(t)
	if err := SyncCodex(ctx, database, []string{codexHome}, &Counters{}); err != nil {
		t.Fatalf("first SyncCodex() error = %v", err)
	}

	if err := os.WriteFile(statePath, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLines(t, rollout, meta, prompt("first"), prompt("second"))
	if err := SyncCodex(ctx, database, []string{codexHome}, &Counters{}); err != nil {
		t.Fatalf("SyncCodex() over an unreadable state store error = %v, want the pass to go on", err)
	}

	for _, want := range []struct {
		id     string
		prompt string
		count  int64
	}{{"store-only", "paginated first prompt", 1}, {"file", "first", 2}} {
		row, found, err := database.Rollout(ctx, want.id)
		if err != nil || !found || row.FirstPrompt != want.prompt || row.PromptCount != want.count {
			t.Errorf("Rollout(%q) = %#v found=%t err=%v, want first prompt %q and %d prompts",
				want.id, row, found, err, want.prompt, want.count)
		}
	}
	want := "codex state|read Codex state: all 1 generation(s) were unreadable"
	if got := unreadableWarnings(recorder, codexHome); len(got) != 1 || got[0] != want {
		t.Fatalf("warnings for %s = %q, want [%q]", codexHome, got, want)
	}
}
