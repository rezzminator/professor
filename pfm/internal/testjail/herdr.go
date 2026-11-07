package testjail

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// StageHerdrShell supplies the config and wrapper of an installed Herdr host.
func StageHerdrShell(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".config", "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	wrapper := paths.HerdrShellWrapper(home)
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(checkoutRoot(), "pfm", "internal", "installer", "assets", "shim", "pfm-herdr-shell.sh")
	raw, err := os.ReadFile(asset)
	if err != nil {
		t.Fatalf("read installed Herdr shell fixture asset: %v", err)
	}
	contentWrapper := strings.NewReplacer("__PFM_REAL_SHELL__", cmdparse.Quote("/bin/sh"), "__PFM_WRAPPER__", cmdparse.Quote(wrapper)).
		Replace(string(raw))
	if err := WriteExecutable(wrapper, []byte(contentWrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(
		"[terminal]\n# >>> pfm herdr shell >>>\ndefault_shell = %q\n# <<< pfm herdr shell <<<\n",
		wrapper,
	)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// HerdrClaudeHook is the SessionStart command `herdr integration install
// claude` (Herdr 0.9.5, script v11) writes for the Claude config dir dir.
func HerdrClaudeHook(dir string) string {
	return "bash '" + dir + "/hooks/herdr-agent-state.sh' session"
}

// GuardedHerdrClaudeHook is HerdrClaudeHook as pfm install leaves it: guarded
// against herdr-shield panes.
func GuardedHerdrClaudeHook(dir string) string {
	return herdr.GuardClaudeHook(HerdrClaudeHook(dir))
}

// HerdrClaudeSettings renders a Claude settings.json holding one Herdr
// SessionStart entry per dir, with Herdr's matcher and timeout.
func HerdrClaudeSettings(dirs ...string) string {
	entries := make([]string, len(dirs))
	for at, dir := range dirs {
		entries[at] = `{"matcher":"^(startup|resume|clear|compact|fork)$","hooks":[{"type":"command","command":` +
			fmt.Sprintf("%q", HerdrClaudeHook(dir)) + `,"timeout":10}]}`
	}
	return `{"theme":"dark","hooks":{"SessionStart":[` + strings.Join(entries, ",") + `]}}`
}

// GuardedHerdrClaudeSettings is HerdrClaudeSettings with every hook guarded.
func GuardedHerdrClaudeSettings(dirs ...string) string {
	text := HerdrClaudeSettings(dirs...)
	for _, dir := range dirs {
		text = strings.ReplaceAll(
			text,
			fmt.Sprintf("%q", HerdrClaudeHook(dir)),
			fmt.Sprintf("%q", GuardedHerdrClaudeHook(dir)),
		)
	}
	return text
}

// StageHerdrClaudeHook adds Herdr's Claude hook for dir to dir's settings.json
// through its link, keeping every other key and adding nothing when the hook
// is there, the way Herdr's installer does: an installed home on a Herdr host
// holds it.
func StageHerdrClaudeHook(t *testing.T, dir string) {
	t.Helper()
	addHerdrClaudeHook(t, dir, false)
}

// StageInstalledHerdrClaudeHook is StageHerdrClaudeHook with the hook guarded
// as pfm install leaves it: an installed home on a Herdr host holds this one.
func StageInstalledHerdrClaudeHook(t *testing.T, dir string) {
	t.Helper()
	addHerdrClaudeHook(t, dir, true)
}

func addHerdrClaudeHook(t *testing.T, dir string, guarded bool) {
	t.Helper()
	path := filepath.Join(dir, "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("stage Herdr Claude hook: %v", err)
	}
	var settings, entry map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("stage Herdr Claude hook: decode %s: %v", path, err)
	}
	if err := json.Unmarshal([]byte(HerdrClaudeSettings(dir)), &entry); err != nil {
		t.Fatal(err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	sessionStart, _ := hooks["SessionStart"].([]any)
	for _, present := range sessionStart {
		if strings.Contains(fmt.Sprint(present), HerdrClaudeHook(dir)) {
			return
		}
	}
	added := entry["hooks"].(map[string]any)["SessionStart"].([]any)
	if guarded {
		command := added[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		command["command"] = GuardedHerdrClaudeHook(dir)
	}
	hooks["SessionStart"] = append(sessionStart, added...)
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("stage Herdr Claude hook: write %s: %v", path, err)
	}
}

// HerdrPaneInfo returns the required Herdr 0.9.5 PaneInfo identity fields.
// Optional fields belong to the test exercising them, not this common fixture.
func HerdrPaneInfo(pane string) map[string]any {
	workspace, _, _ := strings.Cut(pane, ":")
	return map[string]any{
		"type": "pane_info",
		"pane": map[string]any{
			"pane_id": pane, "workspace_id": workspace,
			"terminal_id": "fixture-" + pane, "tab_id": workspace + ":t1", "focused": true,
		},
	}
}

// HerdrWorkspaceReply answers a birth's placement on a fake Herdr server whose
// tabs all land in workspace: workspace.list lists no workspace, so the birth
// creates its place's, and workspace.create answers with workspace, labelled
// label, the label the request asked for; workspace.rename answers that
// workspace now wears label. Any other method is not handled.
func HerdrWorkspaceReply(method, label, workspace string) (map[string]any, bool) {
	switch method {
	case "workspace.list":
		return map[string]any{"type": "workspace_list", "workspaces": []any{}}, true
	case "workspace.create":
		return map[string]any{
			"type":      "workspace_created",
			"workspace": map[string]any{"workspace_id": workspace, "label": label},
		}, true
	case "workspace.rename":
		return map[string]any{
			"type":      "workspace_info",
			"workspace": map[string]any{"workspace_id": workspace, "label": label},
		}, true
	}
	return nil, false
}

// HerdrShieldListing is `herdr-shield list --json` holding one running holder h1
// (pid holderPID, started at holderStart) whose child (pid childPID, started
// at childStart) runs the shell of pane on socket, which it lists PROTECTED.
func HerdrShieldListing(holderPID, holderStart, childPID, childStart int, pane, socket string) string {
	return fmt.Sprintf(
		`{"enumeration":"OK","error":null,"holders":[{"id":"h1","holder_live":true,"state":"running",`+
			`"orphan":false,"holder":{"pid":%d,"start":%d},"child":{"pid":%d,"start":%d},`+
			`"binding":{"pane":%q,"socket":%q}}],"panes":[{"pane_id":%q,"status":"PROTECTED"}]}`,
		holderPID, holderStart, childPID, childStart, pane, socket, pane,
	)
}

// StageHerdrShield puts a fake herdr-shield first on PATH for the rest of t: its
// `list --json` prints listing to stdout and exits 0, or with exit nonzero
// prints listing to stderr and exits exit. Any other argv exits 2. Like
// herdr-shield's endpoint, it refuses with enumeration ERROR and exit 1 when
// HERDR_SOCKET_PATH is no absolute path or HERDR_SHIELD_INNER is set.
func StageHerdrShield(t *testing.T, listing string, exit int) {
	t.Helper()
	dir := t.TempDir()
	stream := ""
	if exit != 0 {
		stream = " >&2"
	}
	script := fmt.Sprintf(
		"#!/bin/sh\n[ \"$*\" = \"list --json\" ] || { echo \"unexpected argv: $*\" >&2; exit 2; }\n"+
			"case \"$HERDR_SOCKET_PATH\" in /*) refusal= ;; *) refusal=\"HERDR_SOCKET_PATH required\" ;; esac\n"+
			"[ -z \"${HERDR_SHIELD_INNER+set}\" ] || refusal=\"recursive shield invocation refused\"\n"+
			"[ -z \"$refusal\" ] || { printf '{\"enumeration\":\"ERROR\",\"error\":\"%%s\",\"holders\":[],\"panes\":[]}\\n' \"$refusal\"; "+
			"echo \"herdr-shield: ERROR: pane enumeration failed; holder list retained\" >&2; exit 1; }\n"+
			"cat%s <<'PFM_SHIELD_LISTING'\n%s\nPFM_SHIELD_LISTING\nexit %d\n",
		stream, listing, exit,
	)
	if err := WriteExecutable(filepath.Join(dir, "herdr-shield"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+paths.OSEnv{}.Get("PATH"))
}
