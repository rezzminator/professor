// Package codexappendix delivers fleet instructions through native session hooks.
package codexappendix

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	Matcher      = "startup|resume|clear|compact"
	rpcMethodKey = "method"
	rpcParamsKey = "params"
)

// Command identifies only Professor's handler, including homes containing shell metacharacters.
func Command(home string) string {
	path := filepath.Join(home, ".local", "bin", "pfm")
	return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "' internal codex-appendix"
}

type hook struct {
	Key         string `json:"key"`
	Command     string `json:"command"`
	SourcePath  string `json:"sourcePath"`
	Source      string `json:"source"`
	CurrentHash string `json:"currentHash"`
	EventName   string `json:"eventName"`
	Enabled     bool   `json:"enabled"`
	TrustStatus string `json:"trustStatus"`
}

// Register asks the installed harness for the exact trust fingerprint. No model
// turn or copied config/credential home is involved. Only our handler is trusted.
func Register(ctx context.Context, binary, home, account string, uninstall bool) error {
	if uninstall {
		return Unregister(account)
	}
	account, err := filepath.EvalSymlinks(account)
	if err != nil {
		return fmt.Errorf("resolve appendix account: %w", err)
	}
	var listed struct {
		Data []struct {
			Hooks []hook `json:"hooks"`
		} `json:"data"`
	}
	raw, err := rpc(ctx, binary, account, "hooks/list", map[string]any{"cwds": []string{account}})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return fmt.Errorf("decode hook discovery: %w", err)
	}
	expectedSource, err := filepath.EvalSymlinks(filepath.Join(account, "hooks.json"))
	if err != nil {
		return err
	}
	var found []hook
	for _, group := range listed.Data {
		for _, h := range group.Hooks {
			source, _ := filepath.EvalSymlinks(h.SourcePath)
			if h.Command == Command(home) && source == expectedSource && h.EventName == "sessionStart" &&
				h.Source == "user" {
				found = append(found, h)
			}
		}
	}
	if len(found) != 1 || found[0].Key == "" || found[0].CurrentHash == "" {
		return fmt.Errorf(
			"professor appendix hook unavailable (found %d): check hooks feature, managed policy and hooks.json",
			len(found),
		)
	}
	h := found[0]
	if err := saveReceipt(account, h); err != nil {
		return err
	}
	value := map[string]any{"enabled": true, "trusted_hash": h.CurrentHash}
	key, _ := json.Marshal(h.Key)
	_, err = rpc(
		ctx,
		binary,
		account,
		"config/value/write",
		map[string]any{"keyPath": "hooks.state." + string(key), "value": value, "mergeStrategy": "replace"},
	)
	if err != nil {
		return fmt.Errorf("write appendix hook trust: %w", err)
	}
	return nil
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
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start appendix hook registration: %w", err)
	}
	defer func() { cancel(); _ = stdin.Close(); _ = stdout.Close(); _ = cmd.Wait() }()
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
				done <- result{err: err}
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
				done <- result{err: err}
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
