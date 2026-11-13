package workbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

func TestStageOpenCodeHerdrPlugin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "herdr.mjs")
	binary := filepath.Join(dir, "bin dir", "pfm")
	if err := StageOpenCodeHerdrPlugin(path, binary); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(binary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "const PFM_BINARY = "+string(encoded)+"\n") {
		t.Fatalf("staged plugin does not carry the pfm binary %s:\n%.400s", encoded, body)
	}
	if strings.Contains(string(body), herdrPluginBinaryToken) {
		t.Fatalf("staged plugin still holds the placeholder %s", herdrPluginBinaryToken)
	}
	old := time.Unix(100, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := StageOpenCodeHerdrPlugin(path, binary); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("unchanged plugin rewritten: %v, %v", info, err)
	}
	other := filepath.Join(dir, "other", "pfm")
	if err := StageOpenCodeHerdrPlugin(path, other); err != nil {
		t.Fatal(err)
	}
	replaced, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(replaced), other) || bytes.Equal(replaced, body) {
		t.Fatalf("plugin for a different binary not restaged: %s, %v", replaced, err)
	}
}

// The plugin kills a reporter at its own timer: the reporter's deadline must
// end, and its error log land, before that.
func TestOpenCodeHerdrPluginKillsOnlyAfterTheReporterDeadline(t *testing.T) {
	want := fmt.Sprintf("const REPORT_TIMEOUT_MS = %d\n", herdr.EngineKillTimeout.Milliseconds())
	if !strings.Contains(openCodeHerdrPlugin, want) {
		t.Fatalf("plugin holds no %q: its kill timer is not herdr.EngineKillTimeout", want)
	}
}

func TestStageOpenCodeHerdrPluginRefusals(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "plugin.mjs")
	if err := StageOpenCodeHerdrPlugin(path, "/bin/pfm"); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("write failure = %v", err)
	}
	if err := StageOpenCodeHerdrPlugin(filepath.Join(t.TempDir(), "plugin.mjs"), "pfm"); err == nil ||
		!strings.Contains(err.Error(), "absolute pfm binary path") {
		t.Fatalf("a bare PATH name was accepted: %v", err)
	}
}
