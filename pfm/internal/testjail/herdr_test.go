package testjail

import (
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestStageHerdrShell(t *testing.T) {
	home := t.TempDir()
	config := StageHerdrShell(t, home)
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Terminal struct {
			DefaultShell string `toml:"default_shell"`
		}
	}
	if _, err := toml.Decode(string(raw), &parsed); err != nil {
		t.Fatal(err)
	}
	wrapper := paths.HerdrShellWrapper(home)
	if parsed.Terminal.DefaultShell != wrapper || !strings.Contains(string(raw), "# >>> pfm herdr shell >>>") {
		t.Fatalf("fixture is not marked wrapper wiring: %s", raw)
	}
	info, err := os.Stat(wrapper)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("fixture wrapper is not executable: info=%v err=%v", info, err)
	}
}

func TestHerdrPaneInfoIsolatesPerRequestOverrides(t *testing.T) {
	first := HerdrPaneInfo("w2:p3")
	first["pane"].(map[string]any)["terminal_id"] = "moved-terminal"
	second := HerdrPaneInfo("w2:p3")
	if second["pane"].(map[string]any)["terminal_id"] == "moved-terminal" {
		t.Fatal("one request's terminal override leaked into another fixture")
	}
}
