package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// beyondFloat64 is 2^53+1, the first integer a float64 cannot hold: decoded
// into a Go `any` without UseNumber it comes back as 9007199254740992.
const beyondFloat64 = "9007199254740993"

func requireKeepsBeyondFloat64(t *testing.T, door string, rewritten []byte) {
	t.Helper()
	if !strings.Contains(string(rewritten), beyondFloat64) {
		t.Fatalf("%s rewrote a user-owned number %s as something else: %s", door, beyondFloat64, rewritten)
	}
}

func TestUnmarshalKeepingNumbersIsAsStrictAsUnmarshal(t *testing.T) {
	var document map[string]any
	if err := unmarshalKeepingNumbers([]byte(`{"counter":`+beyondFloat64+`}`), &document); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(document["counter"]) != beyondFloat64 {
		t.Fatalf("counter decoded as %v, want %s", document["counter"], beyondFloat64)
	}
	for _, raw := range []string{``, `{`, `{} {}`, `{} trailing`, `{"a":1}]`} {
		if err := unmarshalKeepingNumbers([]byte(raw), &document); err == nil {
			t.Errorf("unmarshalKeepingNumbers(%q) accepted input json.Unmarshal refuses", raw)
		}
	}
	if err := unmarshalKeepingNumbers([]byte("{}\n  \n"), &document); err != nil {
		t.Errorf("trailing whitespace refused: %v", err)
	}
}

func TestMCPRegistryRewriteKeepsIntegersBeyondFloat64(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, ".claude")
	registry := filepath.Join(home, ".claude.json")
	writeFixture(
		t,
		registry,
		`{"counter":`+beyondFloat64+`,"mcpServers":{"foreign":{"type":"stdio","command":"foreign"}}}`,
	)
	options := Options{
		Home:       home,
		ConfigDir:  primary,
		ConfigDirs: []string{primary},
		CodexHomes: []string{},
		Mode:       ModeApply,
		Runner:     &fakeRunner{},
		Stdout:     io.Discard,
		MCPEnabled: map[string]bool{"chat": true},
		MCPPort:    8377,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	installed := readFixture(t, registry)
	if !strings.Contains(installed, `"chat"`) {
		t.Fatalf("install did not register chat: %s", installed)
	}
	requireKeepsBeyondFloat64(t, "MCP install", []byte(installed))
	options.Mode = ModeUninstall
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	uninstalled := readFixture(t, registry)
	if strings.Contains(uninstalled, `"chat"`) {
		t.Fatalf("uninstall left the chat registration: %s", uninstalled)
	}
	requireKeepsBeyondFloat64(t, "MCP uninstall", []byte(uninstalled))
}

func TestClaudeSettingsRewriteKeepsIntegersBeyondFloat64(t *testing.T) {
	updated, changed, _, err := updateSettings([]byte(`{"counter":`+beyondFloat64+`}`), t.TempDir(), false, nil)
	if err != nil || !changed {
		t.Fatalf("updateSettings changed=%v err=%v; want a rewrite", changed, err)
	}
	requireKeepsBeyondFloat64(t, "updateSettings", updated)
}

func TestCodexHooksRewriteKeepsIntegersBeyondFloat64(t *testing.T) {
	updated, changed, _, err := updateCodexHooks([]byte(`{"counter":`+beyondFloat64+`}`), t.TempDir(), false, nil)
	if err != nil || !changed {
		t.Fatalf("updateCodexHooks changed=%v err=%v; want a rewrite", changed, err)
	}
	requireKeepsBeyondFloat64(t, "updateCodexHooks", updated)
}

func TestMemoryHelperHookRewriteKeepsIntegersBeyondFloat64(t *testing.T) {
	home := t.TempDir()
	oldPath := filepath.Join(home, ".claude", "scripts", "cc-memory-wire.sh")
	newPath := filepath.Join(home, ".claude", "scripts", "memory-wire.sh")
	raw := fmt.Sprintf(
		`{"counter":%s,"hooks":{"Stop":[{"hooks":[{"type":"command","command":%q}]}]}}`,
		beyondFloat64,
		oldPath,
	)
	updated, changed, err := rewriteMemoryHelperHookPaths([]byte(raw), map[string]string{oldPath: newPath}, home)
	if err != nil || !changed {
		t.Fatalf("rewriteMemoryHelperHookPaths changed=%v err=%v; want a rewrite", changed, err)
	}
	if !strings.Contains(string(updated), newPath) {
		t.Fatalf("hook command not rewritten: %s", updated)
	}
	requireKeepsBeyondFloat64(t, "rewriteMemoryHelperHookPaths", updated)
}

func TestJSONNumberIsMatchesEitherDecodedForm(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{
		{json.Number("10"), true},
		{json.Number("10.0"), true},
		{float64(10), true},
		{json.Number("11"), false},
		{float64(9), false},
		{"10", false},
		{nil, false},
	} {
		if got := jsonNumberIs(tc.value, 10); got != tc.want {
			t.Errorf("jsonNumberIs(%#v, 10) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
