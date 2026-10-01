package installer

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

func TestClaudeHookTemplatesMatchLaunchRegistry(t *testing.T) {
	home := t.TempDir()
	var want []ExpectedHook
	for _, hook := range claudelaunch.HookTemplates(home) {
		want = append(
			want,
			ExpectedHook{
				Event:   hook.Event,
				Matcher: hook.Matcher,
				Command: hook.Command,
				Name:    hook.Name,
				Async:   hook.Async,
			},
		)
	}
	if got := claudeHookTemplates(home); !reflect.DeepEqual(got, want) {
		t.Errorf("installer templates=%#v, registry=%#v", got, want)
	}
}

func TestClaudeHookTemplatesIncludesReloadIntercept(t *testing.T) {
	t.Parallel()
	home := filepath.Join("neutral", "home")
	templates := claudeHookTemplates(home)
	if got := commandByName(templates, "reload-intercept"); got != home+"/.local/bin/pfm internal reload-intercept" {
		t.Fatalf("reload-intercept command=%q", got)
	}
	found := false
	for _, template := range templates {
		if template.Name != "reload-intercept" {
			continue
		}
		found = true
		if template.Event != "UserPromptSubmit" || template.Matcher != "" {
			t.Fatalf("reload-intercept template=%#v, want UserPromptSubmit with an empty matcher", template)
		}
	}
	if !found {
		t.Fatal("claudeHookTemplates dropped the reload-intercept hook")
	}
}

func TestCodexHookWiringStripsALeftoverClearKillHookInEveryShape(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	canonical := filepath.Join(home, ".local", "bin", "pfm") + " internal clear-kill"
	legacyParent := filepath.Join(home, ".local", "bin", "pfm") + ` internal clear-kill --parent "$PPID"`
	raw := []byte(fmt.Sprintf(
		`{"hooks":{"SessionStart":[`+
			`{"matcher":%q,"hooks":[{"type":"prompt","command":%q}]},`+
			`{"matcher":%q,"hooks":[{"type":"command","command":%q}]}`+
			`]}}`,
		codexClearMatcher, canonical,
		codexClearMatcher, legacyParent,
	))
	updated, changed, owned, err := updateCodexHooks(raw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("Codex hook wiring did not strip the leftover clear-kill hook")
	}
	if len(owned) != 0 {
		t.Fatalf("Codex hook ownership=%#v, want none owned", owned)
	}
	if got := hookCommandCount(t, string(updated), "SessionStart", canonical); got != 0 {
		t.Fatalf("canonical clear-kill count=%d, want zero:\n%s", got, updated)
	}
	if got := hookCommandCount(t, string(updated), "SessionStart", legacyParent); got != 0 {
		t.Fatalf("shell-parent clear-kill count=%d, want zero:\n%s", got, updated)
	}
	if strings.Contains(string(updated), "SessionStart") {
		t.Fatalf("SessionStart survived with nothing left to hold: %s", updated)
	}

	// Idempotent: a second pass over the already-converged file changes
	// nothing further.
	again, changedAgain, _, err := updateCodexHooks(updated, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changedAgain {
		t.Fatalf("a converged Codex hooks file was rewritten again:\n%s", again)
	}
}

func TestClaudeHookTemplatesIncludesGitGuard(t *testing.T) {
	t.Parallel()
	home := filepath.Join("neutral", "home")
	found := 0
	for _, template := range claudeHookTemplates(home) {
		if template.Name != "git-guard" {
			continue
		}
		found++
		if template.Event != "PreToolUse" || template.Matcher != "Bash" || template.Async ||
			template.Command != home+"/.local/bin/pfm internal git-guard" {
			t.Fatalf("git-guard template=%#v, want PreToolUse, matcher Bash, not async, "+
				"pfm internal git-guard", template)
		}
	}
	if found != 1 {
		t.Fatalf("git-guard templates=%d, want exactly 1 among the expected hooks", found)
	}
}

func TestClaudeHookTemplatesIncludesExitCloseAndExitIntercept(t *testing.T) {
	t.Parallel()
	home := filepath.Join("neutral", "home")
	templates := claudeHookTemplates(home)

	if got := commandByName(templates, "exit-intercept"); got != home+"/.local/bin/pfm internal exit-intercept" {
		t.Fatalf("exit-intercept command=%q", got)
	}
	if got := commandByName(templates, "exit-close"); got != home+"/.local/bin/pfm internal exit-close" {
		t.Fatalf("exit-close command=%q", got)
	}

	foundIntercept, foundClose := false, false
	for _, template := range templates {
		switch template.Name {
		case "exit-intercept":
			foundIntercept = true
			if template.Event != "UserPromptSubmit" || template.Matcher != "" {
				t.Fatalf("exit-intercept template=%#v, want UserPromptSubmit with an empty matcher", template)
			}
		case "exit-close":
			foundClose = true
			if template.Event != "SessionEnd" || template.Matcher != "" {
				t.Fatalf("exit-close template=%#v, want SessionEnd with an empty matcher", template)
			}
		}
	}
	if !foundIntercept {
		t.Fatal("claudeHookTemplates dropped the exit-intercept hook")
	}
	if !foundClose {
		t.Fatal("claudeHookTemplates dropped the exit-close hook")
	}
}

func commandByName(hooks []ExpectedHook, name string) string {
	for _, hook := range hooks {
		if hook.Name == name {
			return hook.Command
		}
	}
	panic("installer expected hook is missing: " + name)
}
