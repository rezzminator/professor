package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeStatsReplacesAccountSymlink(t *testing.T) {
	fix := newFixture(t)
	target := filepath.Join(fix.root, "shared-stats.json")
	path := filepath.Join(fix.configDir, "stats-cache.json")
	if err := os.WriteFile(target, []byte(`{"sentinel":"shared stats"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	session := fix.startTUI(engineClaude, nil, nil)
	session.waitFrame("composer", func(frame string) bool { return frame != "" })
	session.typeLine("/stats")
	session.waitFrame(
		"statistics receipt",
		func(frame string) bool { return strings.Contains(frame, "Statistics updated") },
	)
	actualRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected map[string]any
	if err := json.Unmarshal(actualRaw, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(readFile(t, "testdata/stats-cache.json")), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s: parsed stats=%v, want testdata/stats-cache.json=%v", path, actual, expected)
	}
	if actual := readFile(t, target); actual != `{"sentinel":"shared stats"}` {
		t.Fatalf("%s: shared target=%q, want untouched sentinel", target, actual)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s: mode=%s, want regular stats file", path, info.Mode())
	}
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
}
