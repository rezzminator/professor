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
		Keys struct {
			PreviousWorkspace string `toml:"previous_workspace"`
			NextWorkspace     string `toml:"next_workspace"`
			Command           []struct {
				Key string `toml:"key"`
			} `toml:"command"`
		}
	}
	if _, err := toml.Decode(string(raw), &parsed); err != nil {
		t.Fatal(err)
	}
	wrapper := paths.HerdrShellWrapper(home)
	if parsed.Terminal.DefaultShell != wrapper || !strings.Contains(string(raw), "# >>> pfm herdr shell >>>") {
		t.Fatalf("fixture is not marked wrapper wiring: %s", raw)
	}
	if parsed.Keys.PreviousWorkspace != "prefix+i" || parsed.Keys.NextWorkspace != "prefix+m" ||
		len(parsed.Keys.Command) != 1 || parsed.Keys.Command[0].Key != "prefix+a" {
		t.Fatalf("fixture lacks pfm's keys block: %+v in %s", parsed.Keys, raw)
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

func TestHerdrWorkspaceReplyCreatesTheAskedLabel(t *testing.T) {
	listed, handled := HerdrWorkspaceReply("workspace.list", "", "w6")
	if !handled || len(listed["workspaces"].([]any)) != 0 {
		t.Fatalf("list = %v handled=%v, want no workspace", listed, handled)
	}
	created, handled := HerdrWorkspaceReply("workspace.create", "fixture", "w6")
	workspace := created["workspace"].(map[string]any)
	if !handled || workspace["workspace_id"] != "w6" || workspace["label"] != "fixture" {
		t.Fatalf("create = %v handled=%v", created, handled)
	}
	if _, handled := HerdrWorkspaceReply("pane.get", "", "w6"); handled {
		t.Fatal("a pane request was answered as placement")
	}
}
