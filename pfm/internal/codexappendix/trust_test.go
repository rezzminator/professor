package codexappendix

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const (
	trustCommand = "/home/x/.local/bin/pfm internal resume-unkill"
	trustHash    = "sha256:deadbeef"
)

// fakeAppServer answers `codex app-server` over stdio: initialize, then
// hooks/list with the given status, then config/value/write; every request line
// is appended to the log.
const fakeAppServer = `#!/bin/sh
LOG='@LOG@'
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$LOG"
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' '{"id":0,"result":{}}' ;;
    *'"method":"hooks/list"'*) printf '%s\n' '@LIST@' ;;
    *'"method":"config/value/write"'*) printf '%s\n' '{"id":1,"result":{}}' ;;
  esac
done
`

type listedHook struct {
	Key         string `json:"key"`
	Command     string `json:"command"`
	SourcePath  string `json:"sourcePath"`
	Source      string `json:"source"`
	CurrentHash string `json:"currentHash"`
	EventName   string `json:"eventName"`
	Matcher     string `json:"matcher"`
	TrustStatus string `json:"trustStatus"`
}

func registerOwn(binary, account string) error {
	return RegisterHookTrust(context.Background(), binary, account, "sessionStart", "resume", trustCommand)
}

func ownHook(account, status string) listedHook {
	return listedHook{
		Key: account + "/hooks.json:session_start:0:0", Command: trustCommand,
		SourcePath: account + "/hooks.json", Source: "user", CurrentHash: trustHash,
		EventName: "sessionStart", Matcher: "resume", TrustStatus: status,
	}
}

// stageTrustAccount writes the account's hooks.json and a fake codex binary that
// lists the given hooks; it returns the physical account, the binary and the log.
func stageTrustAccount(t *testing.T, build func(account string) []listedHook) (account, binary, requestLog string) {
	t.Helper()
	root := t.TempDir()
	account = filepath.Join(root, "codex")
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	account, err := filepath.EvalSymlinks(account)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(account, "hooks.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := json.Marshal(map[string]any{
		"id": 1, "result": map[string]any{"data": []any{map[string]any{"hooks": build(account)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestLog = filepath.Join(root, "requests.log")
	binary = filepath.Join(root, "fake-codex")
	script := strings.NewReplacer("@LOG@", requestLog, "@LIST@", string(list)).Replace(fakeAppServer)
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return account, binary, requestLog
}

func writeRequests(t *testing.T, requestLog string) []string {
	t.Helper()
	raw, err := os.ReadFile(requestLog)
	if err != nil {
		t.Fatal(err)
	}
	var writes []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, `"method":"config/value/write"`) {
			writes = append(writes, line)
		}
	}
	return writes
}

func TestRegisterHookTrustPicksOnlyItsOwnHandlerAmongPersonalOnes(t *testing.T) {
	account, binary, requestLog := stageTrustAccount(t, func(account string) []listedHook {
		personal := ownHook(account, "untrusted")
		personal.Key, personal.Command = account+"/hooks.json:session_start:0:1", "echo personal"
		personal.CurrentHash = "sha256:other"
		otherMatcher := ownHook(account, "untrusted")
		otherMatcher.Key, otherMatcher.Matcher = account+"/hooks.json:session_start:1:0", "startup"
		foreignFile := ownHook(account, "untrusted")
		foreignFile.Key, foreignFile.SourcePath = "/elsewhere/hooks.json:session_start:0:0", "/elsewhere/hooks.json"
		return []listedHook{personal, otherMatcher, foreignFile, ownHook(account, "untrusted")}
	})
	if err := registerOwn(binary, account); err != nil {
		t.Fatal(err)
	}
	key := account + "/hooks.json:session_start:0:0"
	raw, err := os.ReadFile(filepath.Join(account, ".professor-hook-trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]string
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt) != 1 || receipt[key] != trustHash {
		t.Fatalf("receipt=%v, want only %s -> %s", receipt, key, trustHash)
	}
	writes := writeRequests(t, requestLog)
	if len(writes) != 1 || !strings.Contains(writes[0], `"keyPath":"hooks.state.\"`+key+`\""`) ||
		!strings.Contains(writes[0], `"trusted_hash":"`+trustHash+`"`) ||
		!strings.Contains(writes[0], `"mergeStrategy":"replace"`) {
		t.Fatalf("config/value/write requests=%v", writes)
	}
	if !HookTrustRecorded(account) {
		t.Fatal("HookTrustRecorded=false after a registration")
	}
	if TrustRecorded(account) {
		t.Fatal("the hook receipt answered the retired appendix's question")
	}
}

func TestRegisterHookTrustErrorsWithTheCountWhenTheHandlerIsNotListed(t *testing.T) {
	account, binary, _ := stageTrustAccount(t, func(account string) []listedHook {
		personal := ownHook(account, "untrusted")
		personal.Command = "echo personal"
		return []listedHook{personal}
	})
	err := registerOwn(binary, account)
	if err == nil || !strings.Contains(err.Error(), "found 0") {
		t.Fatalf("err=%v, want one naming found 0", err)
	}
	if HookTrustRecorded(account) {
		t.Fatal("a failed registration left a receipt")
	}
}

func TestRegisterHookTrustIsANoopWhenTrustedAndReceiptMatches(t *testing.T) {
	account, binary, requestLog := stageTrustAccount(t, func(account string) []listedHook {
		return []listedHook{ownHook(account, "trusted")}
	})
	key := account + "/hooks.json:session_start:0:0"
	writeReceiptFile(t, account, `{"`+key+`":"`+trustHash+`"}`)
	if err := registerOwn(binary, account); err != nil {
		t.Fatal(err)
	}
	if writes := writeRequests(t, requestLog); len(writes) != 0 {
		t.Fatalf("a trusted hook with its receipt was written again: %v", writes)
	}

	// Trusted by hand, no receipt: the receipt is recorded so uninstall can clean it.
	if err := os.Remove(filepath.Join(account, ".professor-hook-trust.json")); err != nil {
		t.Fatal(err)
	}
	if err := registerOwn(binary, account); err != nil {
		t.Fatal(err)
	}
	if !HookTrustRecorded(account) || len(writeRequests(t, requestLog)) != 1 {
		t.Fatal("a hand-trusted hook was not recorded")
	}
}

func TestUnregisterHookTrustRemovesOnlyItsOwnTablesAndReceipt(t *testing.T) {
	account := t.TempDir()
	raw := "model='personal'\n[hooks.state.\"owned:key\"]\nenabled=true\ntrusted_hash='sha256:owned'\n" +
		"[hooks.state.\"foreign:key\"]\nenabled=true\ntrusted_hash='sha256:foreign'\n"
	if err := os.WriteFile(filepath.Join(account, "config.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	writeReceiptFile(t, account, `{"owned:key":"sha256:owned"}`)
	appendixReceipt := filepath.Join(account, ".professor-appendix-trust.json")
	if err := os.WriteFile(appendixReceipt, []byte(`{"foreign:key":"sha256:foreign"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UnregisterHookTrust(account); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(account, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "sha256:owned") || !strings.Contains(string(got), "sha256:foreign") ||
		!strings.Contains(string(got), "model='personal'") {
		t.Fatalf("config.toml after cleanup:\n%s", got)
	}
	if HookTrustRecorded(account) {
		t.Fatal("hook receipt survived")
	}
	if _, err := os.Stat(appendixReceipt); err != nil {
		t.Fatalf("the appendix receipt was touched: %v", err)
	}
	if err := UnregisterHookTrust(account); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}
}

func writeReceiptFile(t *testing.T, account, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(account, ".professor-hook-trust.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
