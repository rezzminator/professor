package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const fakeCodexAppServer = `#!/bin/sh
LOG='@LOG@'
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$LOG"
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' '{"id":0,"result":{}}' ;;
    *'"method":"hooks/list"'*)
      status=untrusted
      if grep -q '"method":"config/value/write"' "$LOG"; then status=trusted; fi
      printf '{"id":1,"result":{"data":[{"hooks":[{"key":"@ACCOUNT@/hooks.json:session_start:0:1","command":"echo personal","sourcePath":"@ACCOUNT@/hooks.json","source":"user","currentHash":"sha256:other","eventName":"sessionStart","matcher":"resume","enabled":true,"trustStatus":"untrusted"},{"key":"@ACCOUNT@/hooks.json:session_start:0:0","command":"@COMMAND@","sourcePath":"@ACCOUNT@/hooks.json","source":"user","currentHash":"sha256:deadbeef","eventName":"sessionStart","matcher":"resume","enabled":true,"trustStatus":"%s"}]}]}}\n' "$status" ;;
    *'"method":"config/value/write"'*) printf '%s\n' '{"id":1,"result":{}}' ;;
  esac
done
`

const personalStopHook = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo personal"}]}]}}`

// stageResumeUnkillAccount lays down a Codex account whose hooks.json holds one
// personal Stop hook, and the fake `codex app-server` that answers hook
// discovery with the handler's real key shape. It returns the physical account,
// the fake binary and the request log it appends to.
func stageResumeUnkillAccount(t *testing.T, home string) (account, binary, requestLog string) {
	t.Helper()
	account = filepath.Join(home, ".codex")
	writeFixture(t, filepath.Join(account, "hooks.json"), personalStopHook)
	physical, err := filepath.EvalSymlinks(account)
	if err != nil {
		t.Fatal(err)
	}
	requestLog = filepath.Join(home, "app-server.log")
	script := strings.NewReplacer(
		"@LOG@", requestLog, "@ACCOUNT@", physical, "@COMMAND@", resumeUnkillCommand(home),
	).Replace(fakeCodexAppServer)
	binary = filepath.Join(home, "fake-codex")
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return physical, binary, requestLog
}

func resumeUnkillCommand(home string) string {
	return filepath.Join(home, ".local", "bin", "pfm") + " internal resume-unkill"
}

func resumeUnkillEngine(home, account, binary string, mode Mode, stdout *bytes.Buffer) *engine {
	return &engine{
		options: Options{
			Mode: mode, Home: home, CodexHomes: []string{account}, CodexBinary: binary, Stdout: stdout,
		},
		apply:       true,
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
}

func TestCodexResumeUnkillHookIsWrittenOnceKeepsPersonalHooksAndIsOwned(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, _, _ := stageResumeUnkillAccount(t, home)
	command := resumeUnkillCommand(home)
	path := filepath.Join(account, "hooks.json")

	var first bytes.Buffer
	if err := resumeUnkillEngine(home, account, "", ModeApply, &first).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	raw := readFixture(t, path)
	if got := hookCommandCount(t, raw, "SessionStart", command); got != 1 {
		t.Fatalf("resume-unkill handler count=%d, want 1:\n%s", got, raw)
	}
	var document struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	entries := document.Hooks["SessionStart"]
	if len(entries) != 1 || entries[0].Matcher != "resume" || len(entries[0].Hooks) != 1 ||
		entries[0].Hooks[0].Type != "command" || entries[0].Hooks[0].Timeout != 30 {
		t.Fatalf("SessionStart entries=%+v, want one resume entry holding one command handler, timeout 30", entries)
	}
	if got := hookCommandCount(t, raw, "Stop", "echo personal"); got != 1 {
		t.Fatalf("personal Stop hook lost:\n%s", raw)
	}
	ownership, _, err := readSettingsHookOwnership(settingsHookOwnershipPath(filepath.Join(
		home, ".local", "share", "pfm", "install",
	)))
	if err != nil {
		t.Fatal(err)
	}
	key := settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: command}
	if got := ownership[physicalSettingsPath(path)][key]; got != 1 {
		t.Fatalf("ownership ledger counts %d for the resume-unkill hook, want 1: %v", got, ownership)
	}
	if !strings.Contains(first.String(), "skip    Codex hook trust not recorded for "+account+
		": no Codex binary configured — the resume-unkill hook stays untrusted") {
		t.Fatalf("a missing Codex binary was not said out loud:\n%s", first.String())
	}

	var second bytes.Buffer
	if err := resumeUnkillEngine(home, account, "", ModeApply, &second).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, path); got != raw {
		t.Fatalf("a second run rewrote hooks.json:\n%s\n--- was ---\n%s", got, raw)
	}
	if strings.Contains(second.String(), "change  ") {
		t.Fatalf("a second run reported a change:\n%s", second.String())
	}
}

func TestCodexResumeUnkillHookTrustIsRecordedThroughTheNativeAPI(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, binary, requestLog := stageResumeUnkillAccount(t, home)
	key := account + "/hooks.json:session_start:0:0"

	var transcript bytes.Buffer
	if err := resumeUnkillEngine(home, account, binary, ModeApply, &transcript).wireCodexHooks(); err != nil {
		t.Fatalf("%v\n%s", err, transcript.String())
	}
	var receipt map[string]string
	receiptRaw := readFixture(t, filepath.Join(account, ".professor-hook-trust.json"))
	if err := json.Unmarshal([]byte(receiptRaw), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt) != 1 || receipt[key] != "sha256:deadbeef" {
		t.Fatalf("receipt=%v, want only %s -> sha256:deadbeef", receipt, key)
	}
	logged := readFixture(t, requestLog)
	if got := strings.Count(logged, `"method":"config/value/write"`); got != 1 ||
		!strings.Contains(logged, `"keyPath":"hooks.state.\"`+key+`\""`) ||
		!strings.Contains(logged, `"trusted_hash":"sha256:deadbeef"`) {
		t.Fatalf("config/value/write requests=%d, want one carrying the hooks.state key and hash:\n%s", got, logged)
	}
	if !strings.Contains(transcript.String(), "change  trust resume-unkill hook "+account) {
		t.Fatalf("trust step not reported:\n%s", transcript.String())
	}

	var again bytes.Buffer
	if err := resumeUnkillEngine(home, account, binary, ModeApply, &again).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(readFixture(t, requestLog), `"method":"config/value/write"`); got != 1 {
		t.Fatalf("a trusted hook was written again: %d config/value/write requests", got)
	}
}

func TestCodexResumeUnkillHookAndTrustAreRemovedOnUninstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, binary, _ := stageResumeUnkillAccount(t, home)
	key := account + "/hooks.json:session_start:0:0"
	if err := resumeUnkillEngine(home, account, binary, ModeApply, &bytes.Buffer{}).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	// Codex wrote the trust table on the install's behalf; a personal one sits beside it.
	writeFixture(t, filepath.Join(account, "config.toml"),
		"model = 'personal'\n[hooks.state.\""+key+"\"]\nenabled = true\ntrusted_hash = 'sha256:deadbeef'\n"+
			"[hooks.state.\"other:key\"]\nenabled = false\ntrusted_hash = 'sha256:foreign'\n")

	var transcript bytes.Buffer
	if err := resumeUnkillEngine(home, account, binary, ModeUninstall, &transcript).wireCodexHooks(); err != nil {
		t.Fatalf("%v\n%s", err, transcript.String())
	}
	raw := readFixture(t, filepath.Join(account, "hooks.json"))
	if got := hookCommandCount(t, raw, "SessionStart", resumeUnkillCommand(home)); got != 0 {
		t.Fatalf("uninstall left %d resume-unkill handlers:\n%s", got, raw)
	}
	if got := hookCommandCount(t, raw, "Stop", "echo personal"); got != 1 {
		t.Fatalf("uninstall lost the personal hook:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(account, ".professor-hook-trust.json")); !os.IsNotExist(err) {
		t.Fatalf("hook trust receipt survived uninstall: %v", err)
	}
	config := readFixture(t, filepath.Join(account, "config.toml"))
	if strings.Contains(config, "sha256:deadbeef") || !strings.Contains(config, "sha256:foreign") ||
		!strings.Contains(config, "model = 'personal'") {
		t.Fatalf("uninstall edited config.toml wrongly:\n%s", config)
	}
	if !strings.Contains(transcript.String(), "change  remove resume-unkill hook trust "+account) {
		t.Fatalf("trust removal not reported:\n%s", transcript.String())
	}
}
