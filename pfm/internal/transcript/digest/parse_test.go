package digest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
)

func TestParseReadsRecordShapesAndTallies(t *testing.T) {
	engines := []struct{ raw, want string }{
		{`{"type":"session_meta","payload":{}}`, "codex"},
		{`{"type":"event_msg"}`, "codex"},
		{`{"type":"assistant","message":{}}`, "claude"},
		{`{"type":"something","sessionId":"s"}`, "claude"},
		{`{"type":"something"}`, ""},
		{`[1,2]`, ""},
	}
	for _, row := range engines {
		if got := detectEngine([]byte(row.raw)); got != row.want {
			t.Errorf("detectEngine(%s) = %q, want %q", row.raw, got, row.want)
		}
	}
	if got := member(json.RawMessage(`{"a":1,"b":2,"a":3}`), "a"); string(got) != "3" {
		t.Errorf("member of a repeated key = %s, want the last value", got)
	}
	if got := member(json.RawMessage(`[1]`), "a"); got != nil {
		t.Errorf("member of a list = %s, want none", got)
	}
	for _, row := range []struct{ raw, want string }{{`"user"`, "user"}, {`""`, "?"}, {`null`, "?"}, {`7`, "7"}} {
		if got := typeLabel(json.RawMessage(row.raw)); got != row.want {
			t.Errorf("typeLabel(%s) = %q, want %q", row.raw, got, row.want)
		}
	}
	counter := newTally()
	for _, key := range []string{"Read", "Bash", "Bash", "Grep", "Read", "Bash"} {
		counter.add(key)
	}
	if got := counter.mostCommon(); !slices.Equal(got, []string{"Bash", "Read", "Grep"}) {
		t.Errorf("mostCommon = %q", got)
	}
	if counter.total() != 6 {
		t.Errorf("total = %d, want 6", counter.total())
	}
}

// A compressed rollout resolves by its id, from a Codex home and from a
// --root, the plain twin winning where both sit, and digests decompressed.
func TestACompressedRolloutResolvesByIDAndDigests(t *testing.T) {
	t.Chdir(t.TempDir())
	const meta = `{"timestamp":"2026-09-29T20:00:00.000Z","type":"session_meta","payload":{"id":"aaa111","cwd":"/w"}}` + "\n" +
		`{"timestamp":"2026-09-29T20:00:01.000Z","type":"turn_context","payload":{"model":"gpt-5"}}` + "\n"
	write := func(dir, name string) string {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	compress := func(path string) string {
		compressed, err := codexmeta.CompressRollout(path)
		if err != nil {
			t.Fatal(err)
		}
		return resolvedPath(compressed)
	}
	home := t.TempDir()
	inHome := compress(
		write(filepath.Join(home, "sessions", "2026", "09", "29"), "rollout-2026-09-29T20-00-00-aaa111.jsonl"),
	)
	root := t.TempDir()
	inRoot := compress(write(root, "rollout-2026-09-29T20-00-00-bbb222.jsonl"))
	compress(write(root, "rollout-2026-09-29T20-00-00-ccc333.jsonl"))
	twin := resolvedPath(write(root, "rollout-2026-09-29T20-00-00-ccc333.jsonl"))
	env := Env{CodexHomes: []string{home}}
	for _, row := range []struct{ id, want string }{{"aaa111", inHome}, {"bbb222", inRoot}, {"ccc333", twin}} {
		out, err := DigestTranscript(context.Background(), []string{"locate", row.id, "--root", root}, env)
		if err != nil || out.Text != row.want+"\n" {
			t.Errorf("locate %s = %q, %v; want %s", row.id, out.Text, err, row.want)
		}
	}
	out, err := DigestTranscript(context.Background(), []string{"show", "aaa111"}, env)
	if head, _, _ := strings.Cut(out.Text, "\n"); err != nil || head != "TRANSCRIPT codex · aaa111 · gpt-5" {
		t.Fatalf("show %s = %q, %v; want the decompressed digest", inHome, out.Text, err)
	}
}

// TestCodexNullSettingsReadAsAbsent: a null model, effort or cwd is Python's
// None — absent to the header's model and to the ./ shortening.
func TestCodexNullSettingsReadAsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-2026-09-29T20-00-00-s1.jsonl")
	lines := `{"timestamp":"2026-09-29T20:00:00.000Z","type":"session_meta","payload":{"id":"s1","cwd":null}}` + "\n" +
		`{"timestamp":"2026-09-29T20:00:01.000Z","type":"turn_context","payload":{"model":"gpt-5","effort":null,` +
		`"collaboration_mode":{"settings":{"reasoning_effort":null}}}}` + "\n" +
		`{"timestamp":"2026-09-29T20:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user",` +
		`"content":[{"type":"input_text","text":"open None/x"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := DigestTranscript(context.Background(), []string{"show", path}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.ReplaceAll(
		out.Text,
		filepath.Dir(path),
		"{dir}",
	), "TRANSCRIPT codex · s1 · gpt-5\nFILE {dir}/rollout-2026-09-29T20-00-00-s1.jsonl · 423 bytes · 3 records · cwd None (shown as ./)\nSPAN 2026-09-29 20:00:00Z → 20:00:02Z · 2s wall\nCALLS 0, 0 err, 0 without result: none\nFILTER only prompt,reply,call,note,final · results brief · text 500 · shown 1 of 1 events\nL3 20:00:02 PROMPT open None/x\nUNFINISHED — the last turn has no final reply: the run was cut off or is still running\nSKIPPED 0 records | HEADER 2: session_meta 1 · turn_context 1\n"; got != want {
		t.Errorf("digest = %q, want %q", got, want)
	}
}

// The engine is named by the first record that names one, however far in: the
// records before it are digested under it, not lost to a fixed detection window.
func TestParseFindsAnEngineNamedPastTheDetectWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "late.jsonl")
	content := strings.Repeat(`{"foo":1}`+"\n", detectWindow+10) +
		`{"type":"user","timestamp":"2026-09-29T20:00:00.000Z","message":{"content":"late prompt"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := DigestTranscript(context.Background(), []string{"show", path}, Env{})
	if err != nil {
		t.Fatalf("show %s: %v", path, err)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("warnings = %q, want none: a record named the engine", out.Warnings)
	}
	lines := strings.Split(out.Text, "\n")
	if lines[0] != "TRANSCRIPT claude · ? · model ?" || !slices.Contains(lines, "L91 20:00:00 PROMPT late prompt") {
		t.Errorf("digest of %s = %q, want a Claude digest with the prompt at L91", path, out.Text)
	}
}
