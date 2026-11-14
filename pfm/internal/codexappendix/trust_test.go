package codexappendix

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const (
	trustCommand = "/home/x/.local/bin/pfm internal resume-unkill"
	trustHash    = "sha256:deadbeef"
)

// fakeAppServer plays `codex app-server` over stdio for every start: initialize,
// then hooks/list with the staged list, then config/value/write; every request
// line is appended to the log. Each start is scripted on its own FakeRunner, so
// every exchange gets fresh pipes, and is kept on Starts.
type fakeAppServer struct {
	*deps.FakeRunner
	list, requestLog string
	// writeReply answers config/value/write; exits makes the app-server exit
	// at start, reading and answering nothing.
	writeReply string
	exits      bool
	mu         sync.Mutex
	starts     []deps.StartCall
}

func (fake *fakeAppServer) Start(ctx context.Context, argv []string, opts deps.StartOptions) (deps.Process, error) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	if fake.exits {
		_ = stdinReader.Close()
		_ = stdoutWriter.Close()
	} else {
		go fake.serve(stdinReader, stdoutWriter)
	}
	child := &deps.FakeRunner{}
	child.ScriptInteractive(
		[]string{argv[0], "app-server"},
		deps.InteractiveScript{Pid: 4242, Stdin: stdinWriter, Stdout: stdoutReader},
	)
	process, err := child.Start(ctx, argv, opts)
	fake.mu.Lock()
	fake.starts = append(fake.starts, child.Starts()...)
	fake.mu.Unlock()
	return process, err
}

// Starts returns every app-server start, in order.
func (fake *fakeAppServer) Starts() []deps.StartCall {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]deps.StartCall(nil), fake.starts...)
}

func (fake *fakeAppServer) serve(stdin *io.PipeReader, stdout *io.PipeWriter) {
	// Replies queue apart from the reads: rpc sends every request before it
	// reads a reply, as a real pipe's buffer lets it.
	replies := make(chan string, 8)
	var serveErr error
	go func() {
		for reply := range replies {
			_, _ = stdout.Write([]byte(reply + "\n"))
		}
		_ = stdout.CloseWithError(serveErr)
	}()
	defer close(replies)
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 65536), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		log, err := os.OpenFile(fake.requestLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, err = log.WriteString(line + "\n")
			err = errors.Join(err, log.Close())
		}
		if err != nil {
			serveErr = fmt.Errorf("log the app-server request in %s: %w", fake.requestLog, err)
			return
		}
		switch {
		case strings.Contains(line, `"method":"initialize"`):
			replies <- `{"id":0,"result":{}}`
		case strings.Contains(line, `"method":"hooks/list"`):
			replies <- fake.list
		case strings.Contains(line, `"method":"config/value/write"`):
			replies <- fake.writeReply
		}
	}
	serveErr = scanner.Err()
}

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

// stageTrustAccount writes the account's hooks.json and points the app-server
// start at a fakeAppServer listing the given hooks; it returns the physical
// account, the codex binary it is started as and the log.
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
	processRunner = &fakeAppServer{
		FakeRunner: &deps.FakeRunner{}, list: string(list), requestLog: requestLog,
		writeReply: `{"id":1,"result":{}}`,
	}
	t.Cleanup(func() { processRunner = nil })
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
	t.Setenv("CODEX_HOME", testjail.Place("/elsewhere"))
	account, binary, _ := stageTrustAccount(t, func(account string) []listedHook {
		return []listedHook{ownHook(account, "untrusted")}
	})
	if err := registerOwn(binary, account); err != nil {
		t.Fatalf("RegisterHookTrust: %v", err)
	}
	starts := processRunner.(*fakeAppServer).Starts()
	for _, start := range starts {
		if want := []string{binary, "app-server"}; !reflect.DeepEqual(start.Argv, want) {
			t.Fatalf("app-server start argv = %q, want %q", start.Argv, want)
		}
	}
	if len(starts) == 0 {
		t.Fatal("RegisterHookTrust started no app-server")
	}
	seen := []byte(strings.Join(starts[len(starts)-1].Opts.Env, "\n") + "\n")
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
		foreignFile.Key, foreignFile.SourcePath = testjail.Place(
			"/elsewhere/hooks.json:session_start:0:0",
		), testjail.Place(
			"/elsewhere/hooks.json",
		)
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
}

func TestRegisterHookTrustErrorsWithTheCountWhenTheHandlerIsNotListed(t *testing.T) {
	account, binary, _ := stageTrustAccount(t, func(account string) []listedHook {
		personal := ownHook(account, "untrusted")
		personal.Command = "echo personal"
		return []listedHook{personal}
	})
	err := registerOwn(binary, account)
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		account,
		"{account}",
	), "hook \"/home/x/.local/bin/pfm internal resume-unkill\" (sessionStart/resume) unavailable in {account}/hooks.json (found 0): check hooks feature, managed policy and hooks.json"; got != want {
		t.Fatalf("err = %q, want %q", got, want)
	}
	if HookTrustRecorded(account) {
		t.Fatal("a failed registration left a receipt")
	}
}

func TestRegisterHookTrustIsANoopWhenTrustedAndReceiptMatches(t *testing.T) {
	for _, test := range []struct {
		name             string
		enabled, receipt bool
		writes           string
	}{
		{"trusted enabled recorded", true, true, ""},
		{"trusted disabled recorded", false, true, ""},
		{"trusted enabled unrecorded", true, false, "{\"id\":1,\"method\":\"config/value/write\",\"params\":{\"keyPath\":\"hooks.state.\\\"{account}/hooks.json:session_start:0:0\\\"\",\"mergeStrategy\":\"replace\",\"value\":{\"enabled\":true,\"trusted_hash\":\"sha256:deadbeef\"}}}"},
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
			if got := strings.ReplaceAll(
				strings.Join(writeRequests(t, requestLog), "\n"),
				account,
				"{account}",
			); got != test.writes {
				t.Fatalf("writes = %q, want %q", got, test.writes)
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
	if err := UnregisterHookTrust(account); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(account, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "model='personal'\n[hooks.state.\"foreign:key\"]\nenabled=true\ntrusted_hash='sha256:foreign'\n"; string(
		got,
	) != want {
		t.Fatalf("config.toml after cleanup = %q, want %q", got, want)
	}
	if HookTrustRecorded(account) {
		t.Fatal("hook receipt survived")
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
	processRunner.(*fakeAppServer).writeReply = `{"id":1,"error":{"code":-1,"message":"denied"}}`
	err := registerOwn(binary, account)
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		account,
		"{account}",
	), "write hook trust {account}/hooks.json:session_start:0:0 for Codex account {account}: native hook API: {\"code\":-1,\"message\":\"denied\"}"; got != want {
		t.Fatalf("registration error = %q, want %q", got, want)
	}
	if len(writeRequests(t, requestLog)) != 1 || HookTrustRecorded(account) {
		t.Fatal("a denied write was recorded as trusted")
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
			switch variant {
			case "malformed":
				processRunner.(*fakeAppServer).list = `{"id":1,"result":{"data":null}}`
			case "failed":
				processRunner.(*fakeAppServer).exits = true
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
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		account,
		"{account}",
	), "hook \"/home/x/.local/bin/pfm internal herdr-state --engine codex working\" (userPromptSubmit/) unavailable in {account}/hooks.json (found 0): check hooks feature, managed policy and hooks.json"; got != want {
		t.Fatalf("err = %q, want %q", got, want)
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
	var states []string
	for _, state := range NativeHookTrustStates(t.Context(), binary, account, []Handler{ownHandler, promptHandler}) {
		states = append(states, fmt.Sprintf("trusted %t: %v", state.Trusted, state.Err))
	}
	if got, want := strings.ReplaceAll(
		strings.Join(states, "\n"),
		account,
		"{account}",
	), "trusted false: hook trust receipt {account}/.professor-hook-trust.json has no valid handler fingerprint\ntrusted false: hook trust receipt {account}/.professor-hook-trust.json has no valid handler fingerprint"; got != want {
		t.Fatalf("states = %q, want %q", got, want)
	}
}

// A cancelled exchange kills the helper's whole process group: the app-server
// and a descendant it started are both gone once rpc returns, within the
// 250 ms pipe bound plus a second of polling.
