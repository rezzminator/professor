package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	claudeengine "hostops/pfm/internal/engine/claude"
	codexengine "hostops/pfm/internal/engine/codex"
	opencodeengine "hostops/pfm/internal/engine/opencode"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/testjail"
)

func TestMain(m *testing.M) {
	// The composition root (cmd/pfm/engines.go) wires the engines in
	// production. A package test wires every engine's index source — a scan
	// refuses an engine it cannot index — and the matchers its gathers use.
	index.RegisterSource(pfmengine.Claude, claudeengine.Source{})
	index.RegisterSource(pfmengine.Codex, codexengine.Source{})
	index.RegisterSource(pfmengine.OpenCode, opencodeengine.Source{})
	gather.RegisterMatcher(pfmengine.Claude, claudeengine.Matcher{})
	gather.RegisterMatcher(pfmengine.Codex, codexengine.Matcher{})
	os.Exit(testjail.Run(m))
}

// seedClaudeChat writes a resumable Claude transcript into a jailed fleet's
// Claude root (testjail.Fleet's root): an opening user turn, then records.
// The verbs' own scan indexes it, so the chat is addressable by id with no
// server behind it.
func seedClaudeChat(t *testing.T, root, id string, records ...string) {
	t.Helper()
	directory := filepath.Join(root, "claude", "project")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := append([]string{
		`{"type":"user","cwd":"/work/project","message":{"content":"wire the verb layer through the fleet"}}`,
	}, records...)
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(directory, id+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// assistantSaid is an assistant transcript record carrying text.
func assistantSaid(text string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}`
}

// TestVerbsReportAnUnknownTargetAsTargetError pins the error kinds every
// surface maps: over an empty jailed fleet each verb's target is unknown —
// a *TargetError wrapping ErrUnknownChat — and never a scan failure, a
// zero result, or a missing-transcript answer.
func TestVerbsReportAnUnknownTargetAsTargetError(t *testing.T) {
	testjail.Fleet(t)
	verbs := Verbs{}
	ctx := context.Background()
	_, lastErr := verbs.Last(ctx, LastRequest{Target: "ghost"})
	_, statusErr := verbs.Status(ctx, StatusRequest{Target: "ghost"})
	_, _, _, readErr := ReadEntries(ctx, "ghost", 1, nil)
	for name, err := range map[string]error{"last": lastErr, "status": statusErr, "read": readErr} {
		var target *TargetError
		if !errors.As(err, &target) || !errors.Is(err, ErrUnknownChat) || target.Name != "ghost" {
			t.Errorf("%s error = %v, want a *TargetError for \"ghost\" wrapping ErrUnknownChat", name, err)
		}
		if err != nil && err.Error() != `no chat named "ghost"` {
			t.Errorf("%s error text = %q", name, err.Error())
		}
	}
}
