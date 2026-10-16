package codexappendix

import (
	"context"
	"encoding/json"
	"errors"
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
	HandlerType string `json:"handlerType"`
	Command     string `json:"command"`
	SourcePath  string `json:"sourcePath"`
	Source      string `json:"source"`
	CurrentHash string `json:"currentHash"`
	EventName   string `json:"eventName"`
	Matcher     string `json:"matcher"`
	TrustStatus string `json:"trustStatus"`
	Enabled     bool   `json:"enabled"`
}

var ownHandler = Handler{Event: "sessionStart", Matcher: "resume", Command: trustCommand}

func registerOwn(binary, account string) error {
	return RegisterHookTrust(context.Background(), binary, account, "sessionStart", "resume", trustCommand)
}

func ownHook(account, status string) listedHook {
	return listedHook{
		Key: account + "/hooks.json:session_start:0:0", Command: trustCommand,
		SourcePath: account + "/hooks.json", Source: "user", CurrentHash: trustHash,
		HandlerType: "command", EventName: "sessionStart", Matcher: "resume", TrustStatus: status, Enabled: true,
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

// TestRegisterHookTrustAppServerDropsTheHerdrPane: the trust registration talks
// to `codex app-server` and runs no turn, so it is never the chat bound to the
// parent's Herdr pane; CODEX_HOME still points at the account.
func TestRegisterHookTrustAppServerDropsTheHerdrPane(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/h.sock")
	t.Setenv("HERDR_BIN_PATH", "/usr/local/bin/herdr")
	t.Setenv("CODEX_HOME", "/elsewhere")
	account, binary, _ := stageTrustAccount(t, func(account string) []listedHook {
		return []listedHook{ownHook(account, "untrusted")}
	})
	envLog := filepath.Join(t.TempDir(), "env.log")
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := strings.Replace(string(raw), "#!/bin/sh\n", "#!/bin/sh\n/usr/bin/env > '"+envLog+"'\n", 1)
	if err := testjail.WriteExecutable(binary, []byte(wrapped), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := registerOwn(binary, account); err != nil {
		t.Fatalf("RegisterHookTrust: %v", err)
	}
	seen, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(seen), "CODEX_HOME="+account+"\n") {
		t.Errorf("CODEX_HOME not pointed at %s: %s", account, seen)
	}
	for _, name := range []string{"HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH"} {
		if strings.Contains("\n"+string(seen), "\n"+name+"=") {
			t.Errorf("%s reached the app-server: %s", name, seen)
		}
	}
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
	for _, test := range []struct {
		name             string
		enabled, receipt bool
		writes           int
	}{
		{"trusted enabled recorded", true, true, 0},
		{"trusted disabled recorded", false, true, 0},
		{"trusted enabled unrecorded", true, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			account, binary, requestLog := stageTrustAccount(t, func(account string) []listedHook {
				h := ownHook(account, "trusted")
				h.Enabled = test.enabled
				return []listedHook{h}
			})
			key := account + "/hooks.json:session_start:0:0"
			if test.receipt {
				writeReceiptFile(t, account, `{"`+key+`":"`+trustHash+`"}`)
			}
			if err := registerOwn(binary, account); err != nil {
				t.Fatal(err)
			}
			writes := writeRequests(t, requestLog)
			if len(writes) != test.writes {
				t.Fatalf("writes=%v, want %d", writes, test.writes)
			}
			if test.writes != 0 && !strings.Contains(writes[0], `"enabled":true`) {
				t.Fatalf("owned handler not enabled by registration: %v", writes)
			}
			if !HookTrustRecorded(account) {
				t.Fatal("registration did not preserve hook receipt")
			}
		})
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

func TestRegisterHookTrustRecordsReceiptOnlyAfterAcceptedWrite(t *testing.T) {
	account, binary, requestLog := stageTrustAccount(t, func(account string) []listedHook {
		return []listedHook{ownHook(account, "untrusted")}
	})
	script, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.ReplaceAll(string(script), `{"id":1,"result":{}}`,
		`{"id":1,"error":{"code":-1,"message":"denied"}}`))
	if err := testjail.WriteExecutable(binary, script, 0o700); err != nil {
		t.Fatal(err)
	}
	err = registerOwn(binary, account)
	if err == nil || !strings.HasPrefix(err.Error(), "write hook trust ") || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("registration error=%v, want write hook trust denial", err)
	}
	if len(writeRequests(t, requestLog)) != 1 || HookTrustRecorded(account) {
		t.Fatal("a denied write was recorded as trusted")
	}
}

func TestHookTrustStateDistinguishesMissingRecordedAndUnreadable(t *testing.T) {
	for _, state := range []string{"absent", "file", "self-symlink"} {
		t.Run(state, func(t *testing.T) {
			account := t.TempDir()
			path := hookReceiptPath(account)
			switch state {
			case "file":
				writeReceiptFile(t, account, `{"`+account+`/hooks.json:session_start:0:0":"sha256:abcd"}`)
			case "self-symlink":
				if err := os.Symlink(path, path); err != nil {
					t.Fatal(err)
				}
			}
			recorded, err := HookTrustState(account)
			switch state {
			case "self-symlink":
				if recorded || err == nil || !strings.HasPrefix(err.Error(), "inspect hook trust receipt "+path+": ") {
					t.Fatalf("state=(%v, %v), want unreadable receipt", recorded, err)
				}
				if !HookTrustRecorded(account) {
					t.Fatal("uninstall must still attempt unreadable receipt cleanup")
				}
			case "file":
				if recorded || err == nil || !strings.Contains(err.Error(), "native trust") {
					t.Fatalf("native state=(%v,%v), want visibly unknown", recorded, err)
				}
			default:
				if err != nil || recorded {
					t.Fatalf("state=(%v,%v), want absent receipt", recorded, err)
				}
			}
		})
	}
}

func TestHookTrustStateValidatesReceipt(t *testing.T) {
	for _, raw := range []string{"{", "null", "{}", `{ "foreign": "sha256:a" }`, `{ "@HOOK@": "" }`, `{ "@HOOK@:wrong": "hash" }`, "directory"} {
		t.Run(raw, func(t *testing.T) {
			account := t.TempDir()
			path := hookReceiptPath(account)
			if raw == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				raw = strings.ReplaceAll(raw, "@HOOK@", account+"/hooks.json:session_start:0:0")
				if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			recorded, err := HookTrustState(account)
			if recorded || err == nil {
				t.Fatalf("invalid receipt = %t, %v", recorded, err)
			}
		})
	}
}

func TestHookTrustStateReportsUnknownNativeTrust(t *testing.T) {
	for _, native := range []string{"enabled=false\ntrusted_hash='sha256:old'\n", "enabled=true\ntrusted_hash='sha256:changed'\n"} {
		t.Run(native, func(t *testing.T) {
			account := t.TempDir()
			key := account + "/hooks.json:session_start:0:0"
			writeReceiptFile(t, account, `{"`+key+`":"sha256:old"}`)
			if err := os.WriteFile(
				filepath.Join(account, "hooks.json"),
				[]byte(`{"hooks":{"SessionStart":[{"matcher":"resume","hooks":[{"command":"owned"}]}]}}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(account, "config.toml"),
				[]byte("[hooks.state.\""+key+"\"]\n"+native),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			recorded, err := HookTrustState(
				account,
				Handler{Event: "sessionStart", Matcher: "resume", Command: "owned"},
			)
			if recorded || err == nil || !strings.Contains(err.Error(), "native trust") {
				t.Fatalf("historical receipt presented native trust as healthy: recorded=%v err=%v", recorded, err)
			}
		})
	}
}

func TestRegisterHookTrustPreservesDisabledHandler(t *testing.T) {
	account, binary, log := stageTrustAccount(t, func(account string) []listedHook {
		h := ownHook(account, "trusted")
		h.Enabled = false
		return []listedHook{h}
	})
	writeReceiptFile(t, account, `{"`+account+`/hooks.json:session_start:0:0":"`+trustHash+`"}`)
	if err := registerOwn(binary, account); err != nil {
		t.Fatal(err)
	}
	writes := writeRequests(t, log)
	if len(writes) != 0 {
		t.Fatalf("disabled native handler was changed: %v", writes)
	}
}

func TestNativeHookTrustState(t *testing.T) {
	for _, variant := range []string{
		"trusted", "disabled", "untrusted", "stale", "missing-hash", "wrong-source", "foreign-source", "wrong-key",
		"wrong-command", "wrong-matcher", "wrong-event", "wrong-handler", "duplicate", "malformed", "failed",
	} {
		t.Run(variant, func(t *testing.T) {
			account, binary, _ := stageTrustAccount(t, func(account string) []listedHook {
				h := ownHook(account, "trusted")
				switch variant {
				case "disabled":
					h.Enabled = false
				case "untrusted":
					h.TrustStatus = "untrusted"
				case "stale":
					h.CurrentHash = "sha256:new"
				case "missing-hash":
					h.CurrentHash = ""
				case "wrong-source":
					h.SourcePath += "other"
				case "foreign-source":
					h.Source = "project"
				case "wrong-key":
					h.Key = "different"
				case "wrong-command":
					h.Command = "echo other"
				case "wrong-matcher":
					h.Matcher = "startup"
				case "wrong-event":
					h.EventName = "stop"
				case "wrong-handler":
					h.HandlerType = "function"
				}
				if variant == "duplicate" {
					return []listedHook{h, h}
				}
				return []listedHook{h}
			})
			source := filepath.Join(account, "hooks.json")
			raw, err := json.Marshal(
				map[string]any{
					"hooks": map[string]any{
						"SessionStart": []any{
							map[string]any{
								"matcher": "resume",
								"hooks":   []any{map[string]string{"command": trustCommand}},
							},
						},
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			key := account + "/hooks.json:session_start:0:0"
			writeReceiptFile(t, account, `{"`+key+`":"`+trustHash+`"}`)
			if variant == "malformed" || variant == "failed" {
				script := "#!/bin/sh\nexit 1\n"
				if variant == "malformed" {
					script = "#!/bin/sh\nwhile IFS= read -r line; do\ncase \"$line\" in\n*'\"method\":\"initialize\"'*) echo '{\"id\":0,\"result\":{}}';;\n*'\"method\":\"hooks/list\"'*) echo '{\"id\":1,\"result\":{\"data\":null}}';;\nesac\ndone\n"
				}
				if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			trusted, err := NativeHookTrustState(t.Context(), binary, account, ownHandler)
			if variant == "disabled" && !errors.Is(err, ErrNativeHookDisabled) {
				t.Fatalf("disabled state indistinguishable from repairable trust: %v", err)
			}
			switch variant {
			case "trusted":
				if !trusted || err != nil {
					t.Fatalf("trusted=%v err=%v", trusted, err)
				}
			case "failed":
				if trusted || !errors.Is(err, ErrNativeHookTrustUnknown) {
					t.Fatalf("failed readback=%v %v", trusted, err)
				}
			case "wrong-source", "malformed":
				if trusted || err == nil {
					t.Fatalf("invalid native response=%v %v", trusted, err)
				}
			default:
				if trusted || !errors.Is(err, ErrNativeHookUntrusted) {
					t.Fatalf("untrusted handler=%v %v", trusted, err)
				}
			}
		})
	}
}

// Handlers of events other than SessionStart: Codex spells the event
// camelCase in hooks/list and snake_case in the native key, and the Herdr
// reporter's handlers carry no matcher.
var (
	promptHandler = Handler{
		Event:   "userPromptSubmit",
		Command: "/home/x/.local/bin/pfm internal herdr-state --engine codex working",
	}
	permitHandler = Handler{
		Event:   "permissionRequest",
		Command: "/home/x/.local/bin/pfm internal herdr-state --engine codex blocked",
	}
)

func listedHandler(account, snakeEvent string, handler Handler, status string) listedHook {
	return listedHook{
		Key: account + "/hooks.json:" + snakeEvent + ":0:0", Command: handler.Command,
		SourcePath: account + "/hooks.json", Source: "user", CurrentHash: "sha256:" + snakeEvent,
		HandlerType: "command", EventName: handler.Event, Matcher: handler.Matcher, TrustStatus: status, Enabled: true,
	}
}

func writeOwnedHooksFile(t *testing.T, account string) {
	t.Helper()
	raw := `{"hooks":{"SessionStart":[{"matcher":"resume","hooks":[{"command":"` + trustCommand + `"}]}],` +
		`"UserPromptSubmit":[{"hooks":[{"command":"` + promptHandler.Command + `"}]}],` +
		`"PermissionRequest":[{"hooks":[{"command":"` + permitHandler.Command + `"}]}]}}`
	if err := os.WriteFile(filepath.Join(account, "hooks.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func listRequests(t *testing.T, requestLog string) int {
	t.Helper()
	raw, err := os.ReadFile(requestLog)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), `"method":"hooks/list"`)
}

func TestRegisterHookTrustsTrustsEveryHandlerWithOneDiscovery(t *testing.T) {
	account, binary, requestLog := stageTrustAccount(t, func(account string) []listedHook {
		return []listedHook{
			ownHook(account, "untrusted"),
			listedHandler(account, "user_prompt_submit", promptHandler, "untrusted"),
			listedHandler(account, "permission_request", permitHandler, "untrusted"),
		}
	})
	writeOwnedHooksFile(t, account)
	if err := RegisterHookTrusts(
		t.Context(),
		binary,
		account,
		[]Handler{ownHandler, promptHandler, permitHandler},
	); err != nil {
		t.Fatal(err)
	}
	if lists := listRequests(t, requestLog); lists != 1 {
		t.Fatalf("hooks/list requests=%d, want one discovery for all three handlers", lists)
	}
	if writes := writeRequests(t, requestLog); len(writes) != 3 {
		t.Fatalf("writes=%v, want one per handler", writes)
	}
	raw, err := os.ReadFile(filepath.Join(account, ".professor-hook-trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]string
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"session_start", "user_prompt_submit", "permission_request"} {
		if receipt[account+"/hooks.json:"+key+":0:0"] == "" {
			t.Fatalf("receipt=%v lacks the %s handler", receipt, key)
		}
	}
}

func TestRegisterHookTrustsTrustsTheRestWhenOneHandlerIsNotListed(t *testing.T) {
	account, binary, requestLog := stageTrustAccount(t, func(account string) []listedHook {
		return []listedHook{
			ownHook(account, "untrusted"),
			listedHandler(account, "permission_request", permitHandler, "untrusted"),
		}
	})
	writeOwnedHooksFile(t, account)
	err := RegisterHookTrusts(t.Context(), binary, account, []Handler{ownHandler, promptHandler, permitHandler})
	if err == nil || !strings.Contains(err.Error(), promptHandler.Command) {
		t.Fatalf("err=%v, want one naming the unlisted prompt handler", err)
	}
	if writes := writeRequests(t, requestLog); len(writes) != 2 {
		t.Fatalf("writes=%v, want the two listed handlers trusted", writes)
	}
}

func TestNativeHookTrustStatesJudgeEachHandlerFromOneDiscovery(t *testing.T) {
	account, binary, requestLog := stageTrustAccount(t, func(account string) []listedHook {
		return []listedHook{
			ownHook(account, "trusted"),
			listedHandler(account, "user_prompt_submit", promptHandler, "untrusted"),
			listedHandler(account, "permission_request", permitHandler, "trusted"),
		}
	})
	writeOwnedHooksFile(t, account)
	// The receipt records the resume-unkill and the permission handlers, not the prompt one.
	writeReceiptFile(t, account,
		`{"`+account+`/hooks.json:session_start:0:0":"`+trustHash+`",`+
			`"`+account+`/hooks.json:permission_request:0:0":"sha256:permission_request"}`)
	states := NativeHookTrustStates(t.Context(), binary, account, []Handler{ownHandler, promptHandler, permitHandler})
	if len(states) != 3 {
		t.Fatalf("states=%v", states)
	}
	if !states[0].Trusted || states[0].Err != nil {
		t.Fatalf("resume-unkill state=%+v, want trusted", states[0])
	}
	if states[1].Trusted || states[1].Err != nil {
		t.Fatalf("prompt handler the receipt does not name = %+v, want not recorded (false, nil)", states[1])
	}
	if !states[2].Trusted || states[2].Err != nil {
		t.Fatalf("permission handler state=%+v, want trusted", states[2])
	}
	if lists := listRequests(t, requestLog); lists != 1 {
		t.Fatalf("hooks/list requests=%d, want one", lists)
	}
}

func TestNativeHookTrustStatesNameAReceiptThatRecordsNoOwnedHandler(t *testing.T) {
	account, binary, _ := stageTrustAccount(t, func(string) []listedHook { return nil })
	writeOwnedHooksFile(t, account)
	writeReceiptFile(t, account, `{"`+account+`/hooks.json:stop:0:0":"sha256:stop"}`)
	for _, state := range NativeHookTrustStates(t.Context(), binary, account, []Handler{ownHandler, promptHandler}) {
		if state.Trusted || state.Err == nil || !strings.Contains(state.Err.Error(), "no valid handler fingerprint") {
			t.Fatalf("state=%+v, want the receipt named unreadable for every handler", state)
		}
	}
}
