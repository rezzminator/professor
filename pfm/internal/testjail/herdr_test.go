package testjail

import (
	"os"
	"os/exec"
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

func TestStageHerdrShieldAnswersListOnly(t *testing.T) {
	runs := StageHerdrShield(t, `{"enumeration":"OK"}`, 0)
	for _, tc := range []struct {
		name, socket, inner, want string
		exit                      int
	}{
		{name: "upstream socket", socket: "/fixture/herdr.sock", want: "{\"enumeration\":\"OK\"}\n"},
		{
			name: "held shell", socket: "/fixture/herdr.sock", inner: "h1", exit: 1,
			want: `{"enumeration":"ERROR","error":"recursive shield invocation refused","holders":[],"panes":[]}` + "\n",
		},
		{
			name: "no socket", exit: 1,
			want: `{"enumeration":"ERROR","error":"HERDR_SOCKET_PATH required","holders":[],"panes":[]}` + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			list := exec.Command("herdr-shield", "list", "--json")
			list.Env = []string{"PATH=" + paths.OSEnv{}.Get("PATH")}
			if tc.socket != "" {
				list.Env = append(list.Env, "HERDR_SOCKET_PATH="+tc.socket)
			}
			if tc.inner != "" {
				list.Env = append(list.Env, "HERDR_SHIELD_INNER="+tc.inner)
			}
			out, err := list.Output()
			if string(out) != tc.want || list.ProcessState.ExitCode() != tc.exit {
				t.Fatalf("herdr-shield list --json = %q, exit %d (%v); want %q, exit %d",
					out, list.ProcessState.ExitCode(), err, tc.want, tc.exit)
			}
		})
	}
	if err := exec.Command("herdr-shield", "kill", "x").Run(); err == nil {
		t.Fatal("herdr-shield kill ran; the fake answers list --json only")
	}
	if got := runs(); got != 4 {
		t.Fatalf("herdr-shield run count = %d; want 4 (three listings and the refused kill)", got)
	}
}
