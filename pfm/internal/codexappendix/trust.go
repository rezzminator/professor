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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	rpcMethodKey = "method"
	rpcParamsKey = "params"
	trustedState = "trusted"
)

// hook is one entry of the harness's hooks/list answer.
type hook struct {
	Key         string `json:"key"`
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

// HookTrustState distinguishes an absent receipt from one that cannot be inspected.
func HookTrustState(account string, expectedCommand ...string) (recorded bool, err error) {
	path := hookReceiptPath(account)
	_, err = os.Stat(path)
	switch {
	case err == nil:
		receipt, readErr := readHookReceipt(account)
		if readErr != nil {
			return false, readErr
		}
		physical, resolveErr := filepath.EvalSymlinks(account)
		if resolveErr != nil {
			return false, fmt.Errorf("resolve hook trust account %s: %w", account, resolveErr)
		}
		prefix := filepath.Join(physical, "hooks.json") + ":session_start:"
		for key, hash := range receipt {
			if hash == "" || !strings.HasPrefix(key, prefix) {
				continue
			}
			indices := strings.Split(strings.TrimPrefix(key, prefix), ":")
			if len(indices) != 2 {
				continue
			}
			matcher, matcherErr := strconv.Atoi(indices[0])
			handler, handlerErr := strconv.Atoi(indices[1])
			if matcherErr != nil || handlerErr != nil || matcher < 0 || handler < 0 {
				continue
			}
			if len(expectedCommand) > 0 {
				raw, readErr := os.ReadFile(filepath.Join(physical, "hooks.json"))
				if readErr != nil {
					return false, fmt.Errorf("read trusted hook source: %w", readErr)
				}
				var document struct {
					Hooks map[string][]struct {
						Matcher string
						Hooks   []struct{ Command string }
					}
				}
				if err := json.Unmarshal(raw, &document); err != nil {
					return false, fmt.Errorf("parse trusted hook source: %w", err)
				}
				entries := document.Hooks["SessionStart"]
				if matcher >= len(entries) || entries[matcher].Matcher != "resume" ||
					handler >= len(entries[matcher].Hooks) ||
					entries[matcher].Hooks[handler].Command != expectedCommand[0] {
					continue
				}
			}
			return true, nil
		}
		return false, fmt.Errorf("hook trust receipt %s has no valid SessionStart handler fingerprint", path)
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("inspect hook trust receipt %s: %w", path, err)
	}
}

// UnregisterHookTrust removes the hooks.state tables the hook receipt names and
// then the receipt; foreign tables and the appendix receipt stay untouched.
func UnregisterHookTrust(account string) error {
	return unregisterTrust(hookReceiptPath(account), account)
}

// RegisterHookTrust records Codex trust for the one handler whose command,
// matcher and event match, discovered through the installed harness's native
// hooks/list: it asks for the exact trust fingerprint and writes it as
// hooks.state. No model turn or copied config/credential home is involved, and
// only that handler is trusted. event is Codex's spelling ("sessionStart").
func RegisterHookTrust(ctx context.Context, binary, account, event, matcher, command string) error {
	physical, err := filepath.EvalSymlinks(account)
	if err != nil {
		return fmt.Errorf("resolve Codex account %s: %w", account, err)
	}
	expectedSource, err := filepath.EvalSymlinks(filepath.Join(physical, "hooks.json"))
	if err != nil {
		return fmt.Errorf("resolve hooks.json of Codex account %s: %w", physical, err)
	}
	raw, err := rpc(ctx, binary, physical, "hooks/list", map[string]any{"cwds": []string{physical}})
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
	var found []hook
	for _, group := range listed.Data {
		for index := range group.Hooks {
			h := group.Hooks[index]
			source, _ := filepath.EvalSymlinks(h.SourcePath)
			if h.Command == command && h.Matcher == matcher && h.EventName == event && h.Source == "user" &&
				source == expectedSource {
				found = append(found, h)
			}
		}
	}
	if len(found) != 1 || found[0].Key == "" || found[0].CurrentHash == "" {
		return fmt.Errorf(
			"hook %q (%s/%s) unavailable in %s (found %d): check hooks feature, managed policy and hooks.json",
			command, event, matcher, expectedSource, len(found),
		)
	}
	h := found[0]
	receipt, err := readHookReceipt(physical)
	if err != nil {
		return err
	}
	if h.TrustStatus == trustedState && receipt[h.Key] == h.CurrentHash {
		return nil
	}
	key, err := json.Marshal(h.Key)
	if err != nil {
		return fmt.Errorf("quote hook key %q: %w", h.Key, err)
	}
	_, err = rpc(ctx, binary, physical, "config/value/write", map[string]any{
		"keyPath":       "hooks.state." + string(key),
		"value":         map[string]any{"enabled": true, "trusted_hash": h.CurrentHash},
		"mergeStrategy": "replace",
	})
	if err != nil {
		return fmt.Errorf("write hook trust %s for Codex account %s: %w", h.Key, physical, err)
	}
	return saveHookReceipt(physical, receipt, h)
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
	for _, entry := range os.Environ() {
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
