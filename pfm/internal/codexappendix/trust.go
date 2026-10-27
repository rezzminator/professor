package codexappendix

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	rpcMethodKey = "method"
	rpcParamsKey = "params"
	trustedState = "trusted"
)

// ErrNativeHookTrustUnknown distinguishes unavailable native readback from a
// failed receipt read. Historical registration can never prove current trust.
var ErrNativeHookTrustUnknown = errors.New("native trust is unknown")

// ErrNativeHookDisabled names a matching handler deliberately disabled by its operator.
var ErrNativeHookDisabled = fmt.Errorf("%w: operator disabled handler", ErrNativeHookUntrusted)

// hook is one entry of the harness's hooks/list answer.
type hook struct {
	Key         string `json:"key"`
	HandlerType string `json:"handlerType"`
	Command     string `json:"command"`
	Matcher     string `json:"matcher"`
	SourcePath  string `json:"sourcePath"`
	Source      string `json:"source"`
	CurrentHash string `json:"currentHash"`
	EventName   string `json:"eventName"`
	Enabled     bool   `json:"enabled"`
	TrustStatus string `json:"trustStatus"`
}

func hookReceiptPath(account string) string {
	return filepath.Join(account, ".professor-hook-trust.json")
}

// HookTrustRecorded reports whether this account carries the receipt of a
// recorded hook trust. An unreadable receipt answers yes, so the caller's
// cleanup runs and reports its own error rather than passing as absence.
func HookTrustRecorded(account string) bool {
	return receiptRecorded(hookReceiptPath(account))
}

// Handler names one native hook handler pfm owns: Event in Codex's own
// spelling ("sessionStart", "userPromptSubmit"), the matcher the handler's
// entry carries ("" for none) and the exact command.
type Handler struct {
	Event   string
	Matcher string
	Command string
}

// documentEvent is the event's spelling in a hooks.json document
// ("sessionStart" -> "SessionStart").
func (handler Handler) documentEvent() string {
	if handler.Event == "" {
		return ""
	}
	return strings.ToUpper(handler.Event[:1]) + handler.Event[1:]
}

// keyEvent is the event's spelling inside a native hook key
// ("userPromptSubmit" -> "user_prompt_submit").
func (handler Handler) keyEvent() string {
	var key strings.Builder
	for _, r := range handler.Event {
		if r >= 'A' && r <= 'Z' {
			key.WriteByte('_')
			r += 'a' - 'A'
		}
		key.WriteRune(r)
	}
	return key.String()
}

func (handler Handler) String() string {
	return fmt.Sprintf("%s/%q %q", handler.Event, handler.Matcher, handler.Command)
}

// HandlerTrust is the verdict on one handler.
type HandlerTrust struct {
	Handler Handler
	Trusted bool
	Err     error
}

// receiptNaming is what the hook receipt records about a set of handlers.
type receiptNaming struct {
	// exists is false when the account has no receipt at all.
	exists bool
	// firstKey is a fingerprint-shaped receipt key ("" when none): any, with no
	// handlers asked about, else one pointing at an asked handler.
	firstKey string
	// named is, per asked handler, the receipt key pointing at it ("" none).
	named []string
}

// readReceiptNaming reads the receipt of an account and judges every
// fingerprint-shaped key against the handlers' places in the current
// hooks.json.
func readReceiptNaming(account string, expected []Handler) (receiptNaming, error) {
	result := receiptNaming{named: make([]string, len(expected))}
	path := hookReceiptPath(account)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return result, fmt.Errorf("inspect hook trust receipt %s: %w", path, err)
	}
	result.exists = true
	receipt, err := readHookReceipt(account)
	if err != nil {
		return result, err
	}
	physical, err := filepath.EvalSymlinks(account)
	if err != nil {
		return result, fmt.Errorf("resolve hook trust account %s: %w", account, err)
	}
	prefix := filepath.Join(physical, "hooks.json") + ":"
	var document map[string][]sourceEntry
	keys := make([]string, 0, len(receipt))
	for key := range receipt {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if receipt[key] == "" || !strings.HasPrefix(key, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(key, prefix), ":")
		if len(parts) != 3 {
			continue
		}
		matcher, matcherErr := strconv.Atoi(parts[1])
		handler, handlerErr := strconv.Atoi(parts[2])
		if matcherErr != nil || handlerErr != nil || matcher < 0 || handler < 0 {
			continue
		}
		if len(expected) == 0 {
			result.firstKey = key
			return result, nil
		}
		if document == nil {
			raw, readErr := os.ReadFile(filepath.Join(physical, "hooks.json"))
			if readErr != nil {
				return result, fmt.Errorf("read trusted hook source: %w", readErr)
			}
			var parsed struct{ Hooks map[string][]sourceEntry }
			if err := json.Unmarshal(raw, &parsed); err != nil {
				return result, fmt.Errorf("parse trusted hook source: %w", err)
			}
			document = parsed.Hooks
			if document == nil {
				document = map[string][]sourceEntry{}
			}
		}
		for index, want := range expected {
			if result.named[index] == "" && receiptKeyNamesHandler(document, parts[0], matcher, handler, want) {
				result.named[index] = key
				if result.firstKey == "" {
					result.firstKey = key
				}
			}
		}
	}
	return result, nil
}

// sourceEntry is one matcher entry of a hooks.json event, as far as the
// receipt check reads it.
type sourceEntry struct {
	Matcher string
	Hooks   []struct{ Command string }
}

// receiptKeyNamesHandler reports whether the receipt key (event, matcher index,
// handler index) points, in the current hooks.json, at the handler.
func receiptKeyNamesHandler(
	document map[string][]sourceEntry,
	keyEvent string,
	matcher, handler int,
	want Handler,
) bool {
	if want.keyEvent() != keyEvent {
		return false
	}
	entries := document[want.documentEvent()]
	return matcher < len(entries) && entries[matcher].Matcher == want.Matcher &&
		handler < len(entries[matcher].Hooks) && entries[matcher].Hooks[handler].Command == want.Command
}

// ErrNativeHookUntrusted marks an inspected native handler that cannot run safely.
var ErrNativeHookUntrusted = errors.New("native hook is untrusted")

// NativeHookTrustState is NativeHookTrustStates for one handler.
func NativeHookTrustState(ctx context.Context, binary, account string, handler Handler) (bool, error) {
	states := NativeHookTrustStates(ctx, binary, account, []Handler{handler})
	return states[0].Trusted, states[0].Err
}

// NativeHookTrustStates verifies, per handler, the historical fingerprint
// against the current native handler with one hooks/list discovery for all of
// them. Discovery is read-only and does not start a model turn.
func NativeHookTrustStates(ctx context.Context, binary, account string, handlers []Handler) []HandlerTrust {
	states := make([]HandlerTrust, len(handlers))
	for index, handler := range handlers {
		states[index].Handler = handler
	}
	naming, err := readReceiptNaming(account, handlers)
	if err == nil && naming.exists && naming.firstKey == "" {
		err = fmt.Errorf("hook trust receipt %s has no valid handler fingerprint", hookReceiptPath(account))
	}
	if err != nil {
		for index := range states {
			states[index].Err = err
		}
		return states
	}
	var pending []int
	for index := range handlers {
		if naming.named[index] != "" {
			pending = append(pending, index)
		}
	}
	if len(pending) == 0 {
		return states
	}
	listed, physical, err := listNativeHooks(ctx, binary, account)
	for _, index := range pending {
		if err != nil {
			states[index].Err = err
			continue
		}
		states[index].Trusted, states[index].Err = judgeNativeHandler(listed, physical, handlers[index])
	}
	return states
}

// nativeListing is the one cwd group of a hooks/list answer.
type nativeListing struct {
	hooks []hook
}

func listNativeHooks(ctx context.Context, binary, account string) (nativeListing, string, error) {
	physical, err := filepath.EvalSymlinks(account)
	if err != nil {
		return nativeListing{}, "", fmt.Errorf("resolve Codex account %s: %w", account, err)
	}
	if binary == "" {
		return nativeListing{}, physical, fmt.Errorf("%w: Codex binary is not configured", ErrNativeHookTrustUnknown)
	}
	raw, err := rpc(ctx, binary, physical, "hooks/list", map[string]any{rpcCWDsKey: []string{physical}})
	if err != nil {
		return nativeListing{}, physical, fmt.Errorf("%w: hooks/list: %w", ErrNativeHookTrustUnknown, err)
	}
	var listed struct {
		Data []struct {
			Hooks    []hook            `json:"hooks"`
			Warnings []json.RawMessage `json:"warnings"`
			Errors   []json.RawMessage `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nativeListing{}, physical, fmt.Errorf("decode native hooks/list: %w", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].Hooks == nil {
		return nativeListing{}, physical, errors.New("malformed native hooks/list: expected one cwd with hooks")
	}
	group := listed.Data[0]
	if len(group.Errors) > 0 || len(group.Warnings) > 0 {
		return nativeListing{}, physical, fmt.Errorf(
			"native hooks/list diagnostics: errors=%s warnings=%s", group.Errors, group.Warnings,
		)
	}
	return nativeListing{hooks: group.Hooks}, physical, nil
}

func judgeNativeHandler(listed nativeListing, physical string, handler Handler) (bool, error) {
	expectedSource, err := filepath.EvalSymlinks(filepath.Join(physical, "hooks.json"))
	if err != nil {
		return false, fmt.Errorf("resolve native hook source: %w", err)
	}
	var found []hook
	for index := range listed.hooks {
		h := &listed.hooks[index]
		if h.HandlerType != "command" || h.Command != handler.Command || h.Matcher != handler.Matcher ||
			h.EventName != handler.Event ||
			h.Source != "user" {
			continue
		}
		source, err := filepath.EvalSymlinks(h.SourcePath)
		if err != nil {
			return false, fmt.Errorf("resolve listed hook source %s: %w", h.SourcePath, err)
		}
		if source == expectedSource {
			found = append(found, *h)
		}
	}
	if len(found) != 1 {
		return false, fmt.Errorf(
			"%w: expected one matching native handler for %s, found %d",
			ErrNativeHookUntrusted,
			handler,
			len(found),
		)
	}
	h := found[0]
	if !h.Enabled {
		return false, fmt.Errorf("%w: native handler %s is disabled", ErrNativeHookDisabled, h.Key)
	}
	receipt, err := readHookReceipt(physical)
	if err != nil {
		return false, err
	}
	if h.Key == "" || h.CurrentHash == "" || receipt[h.Key] != h.CurrentHash {
		return false, fmt.Errorf(
			"%w: native key or current hash of %s differs from recorded fingerprint",
			ErrNativeHookUntrusted,
			handler,
		)
	}
	if h.TrustStatus != trustedState {
		return false, fmt.Errorf("%w: handler %s trustStatus=%q", ErrNativeHookUntrusted, h.Key, h.TrustStatus)
	}
	return true, nil
}

// UnregisterHookTrust removes the hooks.state tables the hook receipt names and
// then the receipt; foreign tables and the appendix receipt stay untouched.
func UnregisterHookTrust(account string) error {
	return unregisterTrust(hookReceiptPath(account), account)
}

// RegisterHookTrust records Codex trust for the one handler whose command,
// matcher and event match; see RegisterHookTrusts. event is Codex's spelling
// ("sessionStart").
func RegisterHookTrust(ctx context.Context, binary, account, event, matcher, command string) error {
	return RegisterHookTrusts(ctx, binary, account, []Handler{{Event: event, Matcher: matcher, Command: command}})
}

// RegisterHookTrusts records Codex trust for each handler, discovered through
// the installed harness's native hooks/list (one discovery for all): it asks
// for the exact trust fingerprint and writes it as hooks.state. No model turn
// or copied config/credential home is involved, and only those handlers are
// trusted. A handler that is not listed fails alone; the others are still
// trusted, and every failure is returned.
func RegisterHookTrusts(ctx context.Context, binary, account string, handlers []Handler) error {
	physical, err := filepath.EvalSymlinks(account)
	if err != nil {
		return fmt.Errorf("resolve Codex account %s: %w", account, err)
	}
	expectedSource, err := filepath.EvalSymlinks(filepath.Join(physical, "hooks.json"))
	if err != nil {
		return fmt.Errorf("resolve hooks.json of Codex account %s: %w", physical, err)
	}
	raw, err := rpc(ctx, binary, physical, "hooks/list", map[string]any{rpcCWDsKey: []string{physical}})
	if err != nil {
		return fmt.Errorf("list hooks of Codex account %s: %w", physical, err)
	}
	var listed struct {
		Data []struct {
			Hooks []hook `json:"hooks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return fmt.Errorf("decode hook discovery of Codex account %s: %w", physical, err)
	}
	receipt, err := readHookReceipt(physical)
	if err != nil {
		return err
	}
	var failures []error
	for _, handler := range handlers {
		var found []hook
		for _, group := range listed.Data {
			for index := range group.Hooks {
				h := group.Hooks[index]
				source, _ := filepath.EvalSymlinks(h.SourcePath)
				if h.Command == handler.Command && h.Matcher == handler.Matcher && h.EventName == handler.Event &&
					h.Source == "user" && source == expectedSource {
					found = append(found, h)
				}
			}
		}
		if len(found) != 1 || found[0].Key == "" || found[0].CurrentHash == "" {
			failures = append(failures, fmt.Errorf(
				"hook %q (%s/%s) unavailable in %s (found %d): check hooks feature, managed policy and hooks.json",
				handler.Command, handler.Event, handler.Matcher, expectedSource, len(found),
			))
			continue
		}
		h := found[0]
		// A listed disabled handler is an operator choice, not missing trust to repair.
		if !h.Enabled || (h.TrustStatus == trustedState && receipt[h.Key] == h.CurrentHash) {
			continue
		}
		key, err := json.Marshal(h.Key)
		if err != nil {
			failures = append(failures, fmt.Errorf("quote hook key %q: %w", h.Key, err))
			continue
		}
		_, err = rpc(ctx, binary, physical, "config/value/write", map[string]any{
			"keyPath":       "hooks.state." + string(key),
			"value":         map[string]any{"enabled": true, "trusted_hash": h.CurrentHash},
			"mergeStrategy": "replace",
		})
		if err != nil {
			failures = append(
				failures,
				fmt.Errorf("write hook trust %s for Codex account %s: %w", h.Key, physical, err),
			)
			continue
		}
		if err := saveHookReceipt(physical, receipt, h); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func readHookReceipt(account string) (map[string]string, error) {
	path := hookReceiptPath(account)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read hook trust receipt %s: %w", path, err)
	}
	var receipt map[string]string
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, fmt.Errorf("parse hook trust receipt %s: %w", path, err)
	}
	if receipt == nil {
		return nil, fmt.Errorf("hook trust receipt %s must be an object", path)
	}
	return receipt, nil
}

func saveHookReceipt(account string, receipt map[string]string, h hook) error {
	receipt[h.Key] = h.CurrentHash
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode hook trust receipt: %w", err)
	}
	return replaceFile(hookReceiptPath(account), append(raw, '\n'))
}

// rpc bounds the complete helper exchange, including shells whose descendants
// keep pipes open. Cancellation kills its private process group and closes pipes.
func rpc(parent context.Context, binary, account, method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "app-server")
	cmd.Dir = account
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 250 * time.Millisecond
	// The helper runs no turn: it is never the chat bound to the parent's Herdr pane.
	for _, entry := range herdr.StripPane(os.Environ()) {
		if !strings.HasPrefix(entry, "CODEX_HOME=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "CODEX_HOME="+account)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdout: %w", err)
	}
	if err = cmd.Start(); err != nil {
		obs.StartFailed(ctx, cmd.Args, err)
		return nil, fmt.Errorf("start hook trust registration: %w", err)
	}
	// A direct process door (spec § Middleware, runner): recorded here until
	// the helper migrates behind deps.Runner.
	finish := obs.Started(ctx, cmd.Args, cmd.Process.Pid)
	defer func() { cancel(); _ = stdin.Close(); _ = stdout.Close(); finish(cmd.Wait()) }()
	type result struct {
		data json.RawMessage
		err  error
	}
	done := make(chan result, 1)
	go func() {
		encoder := json.NewEncoder(stdin)
		requests := []any{
			map[string]any{
				"id":         0,
				rpcMethodKey: "initialize",
				rpcParamsKey: map[string]any{"clientInfo": map[string]string{"name": "professor", "version": "1"}},
			},
			map[string]any{rpcMethodKey: "initialized", rpcParamsKey: nil},
			map[string]any{"id": 1, rpcMethodKey: method, rpcParamsKey: params},
		}
		for _, r := range requests {
			if err := encoder.Encode(r); err != nil {
				done <- result{err: fmt.Errorf("send %s request: %w", method, err)}
				return
			}
		}
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 65536), 1<<20)
		for scanner.Scan() {
			var response struct {
				ID     *int            `json:"id"`
				Error  json.RawMessage `json:"error"`
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
				done <- result{err: fmt.Errorf("decode native hook API line: %w", err)}
				return
			}
			if len(response.Error) > 0 && string(response.Error) != "null" {
				done <- result{err: fmt.Errorf("native hook API: %s", response.Error)}
				return
			}
			if response.ID != nil && *response.ID == 1 {
				done <- result{data: response.Result}
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = fmt.Errorf("native hook API ended without a response")
		}
		done <- result{err: err}
	}()
	select {
	case r := <-done:
		return r.data, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("native hook API: %w", ctx.Err())
	}
}
