package callmeter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFindRequests(t *testing.T) {
	path := filepath.Join("testdata", "transcript.jsonl")
	got, err := FindRequests(path, []string{"toolu_demo_A", "toolu_demo_B", "toolu_demo_C", "toolu_absent"})
	if err != nil {
		t.Fatalf("FindRequests: %v", err)
	}
	want := map[string]RequestUsage{
		"toolu_demo_A": {
			MessageID:     "msg_demo_1",
			TS:            1790119005062,
			ContextTokens: 10 + 13689 + 10789,
			OutputTokens:  276,
		},
		"toolu_demo_B": {
			MessageID:     "msg_demo_1",
			TS:            1790119005300,
			ContextTokens: 10 + 13689 + 10789,
			OutputTokens:  276,
		},
	}
	// toolu_demo_C sits on the trailing partial line and toolu_absent nowhere:
	// both are absent, neither is an error.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindRequests = %+v\nwant %+v", got, want)
	}
}

func TestFindRequestsUnreadableIsAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.jsonl")
	got, err := FindRequests(missing, []string{"toolu_demo_A"})
	if err == nil {
		t.Fatalf("FindRequests on a missing file = %v, nil; want an error", got)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not name %s", err, missing)
	}
}

func TestFindRequestsMalformedLineNamesIt(t *testing.T) {
	path := filepath.Join("testdata", "malformed.jsonl")
	_, err := FindRequests(path, []string{"toolu_x"})
	if err == nil {
		t.Fatal("FindRequests on a malformed middle line succeeded")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "at byte 96") {
		t.Fatalf("error %q does not name %s at byte 96, the malformed line holding toolu_x", err, path)
	}
}

func TestSubagentTranscriptPath(t *testing.T) {
	got := SubagentTranscriptPath("/tmp/demo-config/projects/-tmp-demo-proj/sess-1.jsonl", "a1b2")
	if want := "/tmp/demo-config/projects/-tmp-demo-proj/sess-1/subagents/agent-a1b2.jsonl"; got != want {
		t.Fatalf("SubagentTranscriptPath = %q, want %q", got, want)
	}
}

func TestConfigDirOf(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "realDir-config")
	if err := os.MkdirAll(filepath.Join(realDir, "projects", "-tmp-demo-proj"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-config")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	resolvedReal, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	// A second account: its own real dir, its projects/ a symlink into the
	// first's (this machine: ~/.claude3/projects -> ~/.claude/projects). One
	// transcript, so one config dir whichever account wrote the call.
	shared := filepath.Join(root, "shared-account")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(realDir, "projects"), filepath.Join(shared, "projects")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(link, "projects", "-tmp-demo-proj", "sess-1.jsonl"):                          resolvedReal,
		filepath.Join(link, "projects", "-tmp-demo-proj", "sess-1", "subagents", "agent-a1.jsonl"): resolvedReal,
		filepath.Join(root, "gone-config", "projects", "-tmp-demo-proj", "sess-1.jsonl"): filepath.Join(
			root,
			"gone-config",
		),
		filepath.Join(root, "elsewhere", "sess-1.jsonl"):                    "",
		filepath.Join(shared, "projects", "-tmp-demo-proj", "sess-1.jsonl"): resolvedReal,
	}
	for path, want := range cases {
		if got := ConfigDirOf(path); got != want {
			t.Errorf("ConfigDirOf(%q) = %q, want %q", path, got, want)
		}
	}
}

// bigTranscript writes head, then filler user lines past FindRequests' first
// tail window, then tail: the shape of a long chat whose newest request is at
// the end.
func bigTranscript(t *testing.T, head, tail string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(head)
	filler := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 1000) +
		`"},"timestamp":"2026-09-22T23:16:40.000Z"}` + "\n"
	for b.Len() < 3<<20 {
		b.WriteString(filler)
	}
	b.WriteString(tail)
	path := filepath.Join(t.TempDir(), "big.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

const tailRequest = `{"type":"assistant","timestamp":"2026-09-22T23:17:00.000Z","message":{"id":"msg_tail",` +
	`"content":[{"type":"tool_use","id":"toolu_tail"}],"usage":{"input_tokens":1,"cache_read_input_tokens":2,` +
	`"cache_creation_input_tokens":3,"output_tokens":4}}}` + "\n"

// TestFindRequestsReadsTheTailFirst: the hook looks up the request it just
// saw, which sits at the end of a transcript that grows to tens of MB; the
// lookup reads the tail and never parses the head, so a malformed line far
// from the wanted ids blinds nothing.
func TestFindRequestsReadsTheTailFirst(t *testing.T) {
	path := bigTranscript(t, `{"type":"assistant","message":{"id":"msg_broken"`+"\n", tailRequest)
	got, err := FindRequests(path, []string{"toolu_tail"})
	if err != nil {
		t.Fatalf("FindRequests: %v", err)
	}
	want := RequestUsage{MessageID: "msg_tail", TS: 1790119020000, ContextTokens: 6, OutputTokens: 4}
	if got["toolu_tail"] != want {
		t.Fatalf("toolu_tail = %+v, want %+v", got["toolu_tail"], want)
	}
}

// TestFindRequestsFallsBackToTheWholeFile: an id older than the tail window is
// still found.
func TestFindRequestsFallsBackToTheWholeFile(t *testing.T) {
	head := strings.NewReplacer("toolu_tail", "toolu_head", "msg_tail", "msg_head").Replace(tailRequest)
	path := bigTranscript(t, head, "")
	got, err := FindRequests(path, []string{"toolu_head", "toolu_absent"})
	if err != nil {
		t.Fatalf("FindRequests: %v", err)
	}
	if got["toolu_head"].MessageID != "msg_head" || len(got) != 1 {
		t.Fatalf("FindRequests = %+v, want only toolu_head from msg_head", got)
	}
}
