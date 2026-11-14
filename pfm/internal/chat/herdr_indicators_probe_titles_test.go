package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

// TestTabTranscriptRenamedSeesOnlyANewTitle pins the title read of a tabbed
// transcript: a rename or AI title changed past the scan's read is a rename;
// Claude's restatement of a title the scan saw within tabTitleSeed before its
// read is none, one older than that is one (the next scan reads it); a line
// still being written, a title of another shape and another engine's
// transcript are none; a rewritten transcript is one until the next scan.
func TestTabTranscriptRenamedSeesOnlyANewTitle(t *testing.T) {
	const aiTitle, rename = `{"type":"ai-title","aiTitle":"Tabs"}`, `{"type":"custom-title","customTitle":"Mine"}`
	// padded is title then a line that ends the file at size.
	padded := func(title string, size int) string {
		return title + "\n" + `{"p":"` + strings.Repeat("x", size-len(title)-1-len(`{"p":""}`)-1) + `"}` + "\n"
	}
	for _, tc := range []struct {
		name, before, after string
		engine              pfmengine.ID
		want                string
	}{
		{"nothing past the read", aiTitle + "\n", "", pfmengine.Claude, ""},
		{"the AI title restated", aiTitle + "\n", aiTitle + "\n", pfmengine.Claude, ""},
		{"a new AI title", aiTitle + "\n", `{"type":"ai-title","aiTitle":"New"}` + "\n", pfmengine.Claude, "renamed"},
		{"a /rename", aiTitle + "\n", rename + "\n", pfmengine.Claude, "renamed"},
		{"a rename restated", rename + "\n", `{"type":"agent-name","agentName":"Mine"}` + "\n", pfmengine.Claude, ""},
		{"a title one seed back, restated", padded(aiTitle, tabTitleSeed), aiTitle + "\n", pfmengine.Claude, ""},
		{"a title one byte older, restated", padded(aiTitle, tabTitleSeed+1), aiTitle + "\n", pfmengine.Claude, "renamed"},
		{"a title line still being written", aiTitle + "\n", `{"type":"ai-title","aiTitle":"Ne`, pfmengine.Claude, ""},
		{"an AI title that is no string", aiTitle + "\n", `{"type":"ai-title","aiTitle":42}` + "\n", pfmengine.Claude, ""},
		{"another engine's transcript", "{}\n", `{"type":"ai-title","aiTitle":"New"}` + "\n", pfmengine.Codex, ""},
	} {
		path := filepath.Join(t.TempDir(), "s1.jsonl")
		if err := os.WriteFile(path, []byte(tc.before), 0o600); err != nil {
			t.Fatal(err)
		}
		probe := &tabProbe{}
		target := headless.Chat{Path: path, Engine: tc.engine}
		probe.scannedTranscript(&target, int64(len(tc.before)), int64(len(tc.before)))
		if err := os.WriteFile(path, []byte(tc.before+tc.after), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := probe.transcriptRenamed(path, int64(len(tc.before+tc.after))); got != tc.want {
			t.Errorf("%s: transcriptRenamed(%s) = %q, want %q", tc.name, path, got, tc.want)
		}
	}

	path := filepath.Join(t.TempDir(), "s1.jsonl")
	long := strings.Repeat(aiTitle+"\n", 4)
	if err := os.WriteFile(path, []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, target := &tabProbe{}, headless.Chat{Path: path, Engine: pfmengine.Claude}
	probe.scannedTranscript(&target, int64(len(long)), int64(len(long)))
	if got := probe.transcriptRenamed(path, int64(len(long))); got != "" {
		t.Fatalf("before the rewrite: transcriptRenamed(%s) = %q, want \"\"", path, got)
	}
	if err := os.WriteFile(path, []byte(rename+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := probe.transcriptRenamed(path, int64(len(rename)+1)); got != "renamed" {
		t.Fatalf("a rewritten transcript: transcriptRenamed(%s) = %q, want \"renamed\"", path, got)
	}
	probe.scannedTranscript(&target, int64(len(rename)+1), int64(len(rename)+1)) // the scan that follows
	if got := probe.transcriptRenamed(path, int64(len(rename)+1)); got != "" {
		t.Fatalf("the rewritten transcript scanned: transcriptRenamed(%s) = %q, want \"\"", path, got)
	}
}
