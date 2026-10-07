package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// Claude's fullscreen boot canary lives in each account's .claude.json: every
// fullscreen boot records fullscreenBootPending[pid]; a later boot counts the
// dead pids as fullscreenBootStrikes, and at two strikes writes
// fullscreenAutoDisabled and falls back to the classic renderer for good.
// fullscreenBootPending holds live pids and is never touched.
const (
	fullscreenAutoDisabledKey = "fullscreenAutoDisabled"
	fullscreenBootStrikesKey  = "fullscreenBootStrikes"
)

// FullscreenAutoDisable is the verdict Claude's boot canary recorded.
type FullscreenAutoDisable struct {
	Version string
	Strikes string
}

// ClaudeRegistryFor is the account's user-scope registry (.claude.json) as
// ClaudeUserRegistries resolves it.
func ClaudeRegistryFor(home string, account pfmconfig.Account) string {
	return ClaudeUserRegistries(home, []pfmconfig.Account{account}, "")[0].Path
}

// ReadFullscreenAutoDisable reads the canary verdict from the registry at
// path: nil when the file or the key is absent, an error when the file cannot
// be read or parsed — its real state is then unknown.
func ReadFullscreenAutoDisable(path string) (*FullscreenAutoDisable, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	encoded, present := document[fullscreenAutoDisabledKey]
	if !present {
		return nil, nil
	}
	var verdict struct {
		Version any `json:"version"`
		Strikes any `json:"strikes"`
	}
	if err := json.Unmarshal(encoded, &verdict); err != nil {
		return nil, fmt.Errorf("parse %s: %s: %w", path, fullscreenAutoDisabledKey, err)
	}
	return &FullscreenAutoDisable{Version: fmt.Sprint(verdict.Version), Strikes: fmt.Sprint(verdict.Strikes)}, nil
}

// clearFullscreenAutoDisable removes the canary's verdict (fullscreenAutoDisabled
// and fullscreenBootStrikes) from the registry of every account that asks for
// fullscreen (claudelaunch.WantsFullscreen), once per physical file, writing
// through a symlinked registry to the file it names. Every other byte of the
// registry is kept; a registry without either key is not written.
func (installer *engine) clearFullscreenAutoDisable() error {
	seen := map[string]bool{}
	var failures []error
	for _, account := range installer.options.ClaudeAccounts {
		wants, err := claudelaunch.WantsFullscreen(account.ConfigDir)
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"claude fullscreen canary for account %d: settings unreadable, state unknown: %w", account.ID, err,
			)))
			continue
		}
		if !wants {
			continue
		}
		path := ClaudeRegistryFor(installer.options.Home, account)
		physical := physicalSettingsPath(path)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		file, err := os.Open(physical)
		if errors.Is(err, fs.ErrNotExist) {
			installer.ok("claude fullscreen renderer not auto-disabled in " + path + " (no registry yet)")
			continue
		}
		var raw []byte
		var mode fs.FileMode
		if err == nil {
			var info fs.FileInfo
			info, err = file.Stat()
			if err == nil {
				mode = info.Mode().Perm()
				raw, err = io.ReadAll(file)
			}
			err = errors.Join(err, file.Close())
		}
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"claude fullscreen canary in %s: registry unreadable, state unknown: %w", path, err,
			)))
			continue
		}
		cleared, removed, err := removeTopLevelKeys(raw, fullscreenAutoDisabledKey, fullscreenBootStrikesKey)
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"claude fullscreen canary in %s: registry unparsable, state unknown: %w", path, err,
			)))
			continue
		}
		if !removed {
			installer.ok("claude fullscreen renderer not auto-disabled in " + path)
			continue
		}
		live, err := liveChatPIDs(installer.options.ProcRoot, account.ConfigDir)
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"claude fullscreen canary in %s: read live chats in %s: %w", path, account.ConfigDir, err,
			)))
			continue
		}
		if len(live) > 0 {
			installer.skip(fmt.Sprintf(
				"claude fullscreen canary in %s: live chats %s on %s — close them and rerun pfm install --yes",
				path, strings.Join(live, ","), account.ConfigDir,
			))
			continue
		}
		if err := installer.change(
			"clear Claude's fullscreen auto-disable in "+physical,
			func() error { return atomicfile.Write(physical, cleared, mode) },
		); err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"clear claude fullscreen auto-disable in %s: %w", physical, err,
			)))
		}
	}
	return errors.Join(failures...)
}

// jsonMember is one top-level member of a JSON object: its key and the byte
// span from the key's opening quote to the end of its value.
type jsonMember struct {
	key        string
	start, end int
}

// removeTopLevelKeys splices every member named in keys out of the JSON object
// raw, leaving every other byte as it was, and reports whether it removed any.
func removeTopLevelKeys(raw []byte, keys ...string) ([]byte, bool, error) {
	removed := false
	for {
		members, err := topLevelMembers(raw)
		if err != nil {
			return nil, false, err
		}
		index := -1
		for position, member := range members {
			for _, key := range keys {
				if member.key == key {
					index = position
					break
				}
			}
			if index >= 0 {
				break
			}
		}
		if index < 0 {
			return raw, removed, nil
		}
		start, end := members[index].start, members[index].end
		switch {
		case index < len(members)-1:
			end = members[index+1].start
		case index > 0:
			start = members[index-1].end
		}
		next := make([]byte, 0, len(raw)-(end-start))
		next = append(append(next, raw[:start]...), raw[end:]...)
		if !json.Valid(next) {
			return nil, false, fmt.Errorf("removing %q left invalid JSON", members[index].key)
		}
		raw, removed = next, true
	}
}

// topLevelMembers lists the members of the JSON object raw in order.
func topLevelMembers(raw []byte) ([]jsonMember, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	open, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if open != json.Delim('{') {
		return nil, fmt.Errorf("decode: top level is %v, not an object", open)
	}
	var members []jsonMember
	previous := int(decoder.InputOffset())
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode key: %w", err)
		}
		key, isString := token.(string)
		if !isString {
			return nil, fmt.Errorf("decode key: %v is not a string", token)
		}
		quote := bytes.IndexByte(raw[previous:decoder.InputOffset()], '"')
		if quote < 0 {
			return nil, fmt.Errorf("decode key %q: opening quote not found", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode %q: %w", key, err)
		}
		end := int(decoder.InputOffset())
		members = append(members, jsonMember{key: key, start: previous + quote, end: end})
		previous = end
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return members, nil
}
