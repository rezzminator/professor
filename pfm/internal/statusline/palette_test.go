package statusline

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestMainLineWearsTheSubagentPalette(t *testing.T) {
	l1, l2 := renderStyled(t, styledPayload)
	for _, want := range []struct{ role, line, text string }{
		{"model", l1, cModel + "◆ Opus 4"},
		{"effort", l1, cEffort + "💠 high"},
		{"label", l1, cLabel + "🔖 BUILDER:1"},
		{"tokens", l2, cTokens + "🧮10.3K"},
		{"elapsed", l2, cElapsed + "⏳ 5m32s"},
	} {
		if !strings.Contains(want.line, want.text) {
			t.Fatalf("%s: line lacks %q:\n%q", want.role, want.text, want.line)
		}
	}

	root := t.TempDir()
	now := time.Unix(1_786_838_400, 0)
	live := filepath.Join(root, "live.jsonl")
	turn := `{"type":"user","timestamp":"` + now.Add(-2*time.Minute).UTC().Format(time.RFC3339Nano) + `"}` + "\n"
	if err := os.WriteFile(live, []byte(turn), 0o600); err != nil {
		t.Fatal(err)
	}
	quiet := filepath.Join(root, "quiet.jsonl")
	if err := os.WriteFile(quiet, []byte(`{"type":"assistant"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cacheRuntime := Runtime{Home: root, CacheDir: filepath.Join(root, "cache"), Env: map[string]string{}}
	for _, want := range []struct{ role, path, color string }{
		{"cache good", live, cGood + "💾1h✓"},
		{"cache warn", quiet, cWarn + "💾1h∞"},
		{"cache bad", "", cBad + "💾1h!"},
	} {
		if got := cacheWindowSegment(cacheRuntime, now, want.path); !strings.Contains(got, want.color) {
			t.Fatalf("%s: segment %q lacks %q", want.role, got, want.color)
		}
	}

	rollout := filepath.Join(root, "rollout.jsonl")
	body := `{"payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":68000},"model_context_window":272000}}}` + "\n"
	if err := os.WriteFile(rollout, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rollout)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(context.Background(), []byte(`{"transcript_path":`+string(encoded)+`}`), Runtime{
		Home: root, CacheDir: filepath.Join(root, "cache"), TmuxDir: filepath.Join(root, "tmux"),
		ProcRoot: filepath.Join(root, "proc"), Columns: 120, UID: 1000, Engine: pfmengine.Codex,
		Env: map[string]string{}, Command: quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, cWindow+"of 272.0K") {
		t.Fatalf("context window: render lacks %q:\n%q", cWindow+"of 272.0K", got)
	}
}

// TestPaletteHasOneDefinition pins palette.go as the only file declaring the
// palette: a second copy in subagents.go or the main line would drift.
func TestPaletteHasOneDefinition(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]bool{
		"cModel": true, "cEffort": true, "cTokens": true, "cWindow": true, "cElapsed": true,
		"cGood": true, "cWarn": true, "cBad": true, "cLabel": true,
	}
	seen := map[string]string{}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.ValueSpec:
				for _, name := range node.Names {
					if roles[name.Name] {
						seen[name.Name] = source
					}
				}
			case *ast.BasicLit:
				if node.Kind == token.STRING && strings.Contains(node.Value, "38;5;") && source != "palette.go" {
					t.Errorf("%s carries a 256-colour literal %s outside palette.go", source, node.Value)
				}
			}
			return true
		})
	}
	for role := range roles {
		if seen[role] != "palette.go" {
			t.Errorf("%s is declared in %q, want palette.go", role, seen[role])
		}
	}
}
