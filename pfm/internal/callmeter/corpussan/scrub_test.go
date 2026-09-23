package corpussan

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestScrubText(t *testing.T) {
	s, err := NewSanitizer(testOpts)
	if err != nil {
		t.Fatalf("NewSanitizer: %v", err)
	}
	cases := []struct{ in, want string }{
		{"/srv/fixture/alice/acme", "/tmp/demo-proj"},
		{"/srv/fixture/alice/acme/pfm/store.go", "/tmp/demo-proj/pfm/store.go"},
		{"/srv/fixture/alice", "/tmp/demo-home"},
		{
			"/srv/fixture/alice/.claude2/projects/-srv-fixture-alice-acme/x.jsonl",
			"/tmp/demo-home/.claude2/projects/-tmp-demo-proj/x.jsonl",
		},
		{
			"/srv/fixture/alice/.cc/2/projects/-srv-fixture-alice-acme-pfm",
			"/tmp/demo-home/.cc/2/projects/-tmp-demo-proj-pfm",
		},
		{"/srv/fixture/alicex/acme", "/tmp/demo-home/xxx/xxxxxxx/xxxxxx/acme"},
		{
			"/private/tmp/claude-501/-srv-fixture-alice-acme/scratchpad",
			"/tmp/demo-home/private/tmp/claude-501/-tmp-demo-proj/xxxxxxxxxx",
		},
		{"/Volumes/Secret/data", "/tmp/demo-home/xxxxxxx/xxxxxx/xxxx"},
		{"~/.claude/settings.json", "/tmp/demo-home/.claude/xxxxxxxx.json"},
		{"/tmp/demo-proj/pfm", "/tmp/demo-proj/pfm"},
		{"/dev/null", "/dev/null"},
		{"/usr/bin/time go test ./pfm/...", "/usr/bin/time go test ./pfm/..."},
		{"pfm/store.go", "pfm/store.go"},
		{`git commit -m "fix Alice bug" --author=alice`, `git commit -m "xxx xxxxx xxx" --author=xxxxx`},
		{"sed -n 1,80p store.go | wc -l", "sed -n 1,80p store.go | wc -l"},
		{"cat <<'EOF'\nprivate words here\nEOF", "cat <<'EOF'\nprivate xxxxx xxxx\nEOF"},
		{"curl https://example.org/alice", "curl https://xxxxxxx.xxx/xxxxx"},
		{`F="$HOME/acme"; grep -n Hello "$F"`, `F="$HOME/acme"; grep -n xxxxx "$F"`},
		{"-Alice-", "-xxxxx-"},
	}
	for _, c := range cases {
		if got := s.scrubText(c.in); got != c.want {
			t.Errorf("scrubText(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestFillerOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc", "xxx"},
		{"a\nb\n", "x\nx\n"},
		{"café", "xxxxx"},
		{"tab\there", "xxxxxxxx"},
	}
	for _, c := range cases {
		if got := fillerOf(c.in); got != c.want {
			t.Errorf("fillerOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAllowFromTree(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"pfm/internal/store.go", "docs/read-me.md", ".git/objects/zz", ".worktrees/wt/secretname.go"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, p), nil, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	got, err := AllowFromTree(root)
	if err != nil {
		t.Fatalf("AllowFromTree: %v", err)
	}
	for _, w := range []string{"pfm", "internal", "store", "go", "docs", "read", "me", "md"} {
		if !slices.Contains(got, w) {
			t.Errorf("AllowFromTree lacks %q: %v", w, got)
		}
	}
	for _, w := range []string{"objects", "zz", "wt", "secretname"} {
		if slices.Contains(got, w) {
			t.Errorf("AllowFromTree took %q from a skipped dir: %v", w, got)
		}
	}
	if !slices.IsSorted(got) {
		t.Errorf("AllowFromTree is not sorted: %v", got)
	}
	if _, err := AllowFromTree(filepath.Join(root, "missing")); err == nil {
		t.Error("AllowFromTree on a missing root returned no error")
	}
}
