package statusline

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var styleANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// renderStyled renders one Claude main statusline over a throwaway runtime and
// returns the raw first two lines.
func renderStyled(t *testing.T, payload string) (string, string) {
	t.Helper()
	root := t.TempDir()
	got, err := Render(context.Background(), []byte(payload), Runtime{
		Now:      func() time.Time { return time.Unix(1_786_838_400, 0) },
		Home:     root,
		CacheDir: filepath.Join(root, "cache"),
		TmuxDir:  filepath.Join(root, "tmux"),
		ProcRoot: filepath.Join(root, "proc"),
		Columns:  120,
		UID:      1000,
		Env:      map[string]string{},
		Command:  quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("render = %q, want at least two lines", got)
	}
	return lines[0], lines[1]
}

const styledPayload = `{
  "model":{"display_name":"Opus 4"},
  "workspace":{"current_dir":"/work/sample"},
  "context_window":{"used_percentage":42,"current_usage":{"cache_read_input_tokens":8000,"cache_creation_input_tokens":2000,"input_tokens":345}},
  "cost":{"total_duration_ms":332000},
  "effort":{"level":"high"},
  "thinking":{"enabled":true},
  "session_name":"BUILDER:1"
}`

func TestSessionLabelRendersSecondFromLast(t *testing.T) {
	l1, _ := renderStyled(t, styledPayload)
	segments := strings.Split(styleANSI.ReplaceAllString(l1, ""), " │ ")
	if len(segments) < 3 || segments[len(segments)-2] != "🔖 BUILDER:1" {
		t.Fatalf("line 1 segments = %q, want 🔖 BUILDER:1 second from last", segments)
	}
}
