package professor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := Baseline{
		Version:   BaselineVersion,
		Blueprint: BlueprintPin{Version: "0.65.0", SHA: "abc1234"},
		Files: map[string]FilePin{
			".claude/commands/dev.md": {
				Template:     "project/commands/dev.md",
				TemplateHash: "sha256:fixture",
				PinnedSHA:    "abc1234",
				PinnedAt:     "2026-09-01",
			},
		},
	}
	if err := Save(root, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Version != want.Version || got.Blueprint != want.Blueprint || len(got.Files) != 1 ||
		got.Files[".claude/commands/dev.md"] != want.Files[".claude/commands/dev.md"] {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestBaselineRoundTripNormalizesIgnored(t *testing.T) {
	root := t.TempDir()
	want := Baseline{
		Version: BaselineVersion,
		Files:   map[string]FilePin{},
		Ignored: []string{"project/z.md", "project/a.md", "project/a.md", "project/m.md"},
	}
	if err := Save(root, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	wantIgnored := []string{"project/a.md", "project/m.md", "project/z.md"}
	if len(got.Ignored) != len(wantIgnored) {
		t.Fatalf("Load() Ignored = %#v, want %#v", got.Ignored, wantIgnored)
	}
	for index, template := range wantIgnored {
		if got.Ignored[index] != template {
			t.Fatalf("Load() Ignored = %#v, want %#v", got.Ignored, wantIgnored)
		}
	}
	raw, err := os.ReadFile(BaselinePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "project/a.md") != 1 {
		t.Fatalf("Save() did not dedupe on disk: %s", raw)
	}
}

func TestBaselineMalformedAndUnsupportedAreNamed(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "malformed", content: "{", want: "BASELINE-MALFORMED"},
		{name: "unsupported", content: `{"version":2,"blueprint":{},"files":{}}`, want: "BASELINE-VERSION 2: unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".professor", "baseline.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestNormalizeIgnoredTrimsDropsEmptyAndDedupes is a REGRESSION test for
// normalizeIgnored surviving a hand-edited baseline: watched failing against
// a build where the trim was neutralized (padded and blank entries survived
// the round-trip instead of being dropped or deduped against their trimmed
// twin).
func TestNormalizeIgnoredTrimsDropsEmptyAndDedupes(t *testing.T) {
	got := normalizeIgnored([]string{"", "  ", " project/x.md ", "project/x.md"})
	want := []string{"project/x.md"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("normalizeIgnored() = %#v, want %#v", got, want)
	}
	if got := normalizeIgnored([]string{"", "   ", "\t"}); got != nil {
		t.Fatalf("normalizeIgnored(all-empty) = %#v, want nil", got)
	}
}
