package mockengine

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

type codexNativeHook struct {
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

// codexHookRPC models native trust using the account's real hook source and
// persisted config. A handler edit invalidates its fingerprint across processes.
func codexHookRPC(proc *process, method string, params json.RawMessage) (any, error) {
	home := proc.env("CODEX_HOME")
	if home == "" {
		home = filepath.Join(proc.env("HOME"), ".codex")
	}
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolve Codex home: %w", err)
	}
	configPath := filepath.Join(home, "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read native hook state: %w", err)
	}
	config := map[string]any{}
	if _, err := toml.Decode(string(raw), &config); err != nil {
		return nil, fmt.Errorf("parse native hook state: %w", err)
	}
	hooks, err := codexNativeHooks(home, config)
	if err != nil {
		return nil, err
	}
	if method == "hooks/list" {
		var input struct {
			CWDs []string `json:"cwds"`
		}
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("decode hooks/list params: %w", err)
		}
		if len(input.CWDs) == 0 {
			return nil, errors.New("hooks/list requires cwds")
		}
		data := make([]map[string]any, 0, len(input.CWDs))
		for _, cwd := range input.CWDs {
			data = append(data, map[string]any{"cwd": cwd, "hooks": hooks})
		}
		return map[string]any{"data": data}, nil
	}
	var input struct {
		KeyPath string `json:"keyPath"`
		Value   struct {
			Enabled     bool   `json:"enabled"`
			TrustedHash string `json:"trusted_hash"`
		} `json:"value"`
		MergeStrategy string `json:"mergeStrategy"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("decode config/value/write params: %w", err)
	}
	var key string
	if !strings.HasPrefix(input.KeyPath, "hooks.state.") || input.MergeStrategy != "replace" {
		return nil, errors.New("mock config write only supports replacing a native hooks.state entry")
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(input.KeyPath, "hooks.state.")), &key); err != nil {
		return nil, fmt.Errorf("decode native hook key: %w", err)
	}
	found := false
	for index := range hooks {
		hook := &hooks[index]
		if hook.Key == key && hook.CurrentHash == input.Value.TrustedHash {
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("native hook key or current hash does not match source")
	}
	updated, err := replaceCodexHookState(string(raw), key, map[string]any{
		"enabled": input.Value.Enabled, "trusted_hash": input.Value.TrustedHash,
	})
	if err != nil {
		return nil, fmt.Errorf("edit native hook state: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		return nil, fmt.Errorf("persist native hook state: %w", err)
	}
	return map[string]any{}, nil
}

// replaceCodexHookState models Codex's targeted config edit, not a whole-file
// re-encode: comments include installer ownership fences, and losing them turns
// the next install's own fleet prompt into an apparently foreign declaration.
func replaceCodexHookState(raw, key string, value map[string]any) (string, error) {
	quoted, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("quote hook key: %w", err)
	}
	var encoded bytes.Buffer
	fmt.Fprintf(&encoded, "[hooks.state.%s]\n", quoted)
	if err := toml.NewEncoder(&encoded).Encode(value); err != nil {
		return "", fmt.Errorf("encode hook state: %w", err)
	}
	start, end, offset := -1, len(raw), 0
	for _, line := range strings.SplitAfter(raw, "\n") {
		boundary := offset
		offset += len(line)
		if !strings.HasPrefix(strings.TrimSpace(line), "[") {
			continue
		}
		var prefix map[string]any
		metadata, err := toml.Decode(raw[:offset], &prefix)
		if err != nil {
			// A header-looking line inside a multiline string is not a table.
			continue
		}
		if start >= 0 {
			end = boundary
			break
		}
		keys := metadata.Keys()
		if len(keys) > 0 {
			last := keys[len(keys)-1]
			if len(last) == 3 && last[0] == "hooks" && last[1] == "state" && last[2] == key {
				start = boundary
			}
		}
	}
	updated := raw
	if start >= 0 {
		// Trailing comments may open the next installer's ownership fence.
		// They are not hook-state values, even when no table follows them.
		for end > start {
			before := strings.TrimSuffix(raw[:end], "\n")
			boundary := strings.LastIndexByte(before, '\n') + 1
			line := strings.TrimSpace(raw[boundary:end])
			if line != "" && !strings.HasPrefix(line, "#") {
				break
			}
			end = boundary
		}
		updated = raw[:start] + raw[end:]
	}
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += encoded.String()
	var checked map[string]any
	if _, err := toml.Decode(updated, &checked); err != nil {
		return "", fmt.Errorf("validate updated hook config: %w", err)
	}
	return updated, nil
}

func codexNativeHooks(home string, config map[string]any) ([]codexNativeHook, error) {
	path := filepath.Join(home, "hooks.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []codexNativeHook{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read native hooks: %w", err)
	}
	var document hookDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode native hooks: %w", err)
	}
	hookConfig, _ := config["hooks"].(map[string]any)
	state, _ := hookConfig["state"].(map[string]any)
	events := make([]string, 0, len(document.Hooks))
	for event := range document.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	hooks := []codexNativeHook{}
	for _, event := range events {
		if event == "" {
			return nil, errors.New("native hook event name is empty")
		}
		for matcherIndex, entry := range document.Hooks[event] {
			for handlerIndex, handler := range entry.Hooks {
				fingerprint, err := json.Marshal(struct {
					Event, Matcher string
					Handler        any
				}{event, entry.Matcher, handler})
				if err != nil {
					return nil, fmt.Errorf("encode native hook fingerprint: %w", err)
				}
				keyEvent := ""
				for index, char := range event {
					if char >= 'A' && char <= 'Z' && index > 0 {
						keyEvent += "_"
					}
					keyEvent += strings.ToLower(string(char))
				}
				key := fmt.Sprintf("%s:%s:%d:%d", path, keyEvent, matcherIndex, handlerIndex)
				hash := fmt.Sprintf("sha256:%x", sha256.Sum256(fingerprint))
				hookState, _ := state[key].(map[string]any)
				enabled := true
				if value, ok := hookState["enabled"].(bool); ok {
					enabled = value
				}
				trust := "untrusted"
				if hookState["trusted_hash"] == hash {
					trust = "trusted"
				}
				hooks = append(hooks, codexNativeHook{
					Key: key, HandlerType: handler.Type, Command: handler.Command,
					Matcher: entry.Matcher, SourcePath: path, Source: "user", CurrentHash: hash,
					EventName: strings.ToLower(event[:1]) + event[1:], Enabled: enabled, TrustStatus: trust,
				})
			}
		}
	}
	return hooks, nil
}
