package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	raw, _, err := atomicfile.ReadRegular(path)
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
		wants, err := claudelaunch.WantsFullscreen(account.ConfigDir, installer.options.ClaudeSettings)
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
		raw, info, err := atomicfile.ReadRegular(physical)
		if errors.Is(err, fs.ErrNotExist) {
			installer.ok("claude fullscreen renderer not auto-disabled in " + path + " (no registry yet)")
			continue
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
		// Unguarded: the clear never held the account guard, so its busy lock or an
		// unresolved launch claim is no new way for this step to fail.
		chats, err := installer.openAccountChats(account.ConfigDir, false)
		var live []string
		if err == nil {
			live, err = chats.live()
		}
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
			func() error {
				locks := installer.claudeMCPLockAliases(path, physical)
				return withClaudeMCPLocks(locks, func(check func() error) error {
					return installer.writeRegistryFileIf(path, raw, cleared, true, info.Mode().Perm(), check)
				})
			},
		); err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"clear claude fullscreen auto-disable in %s: %w", physical, err,
			)))
		}
	}
	return errors.Join(failures...)
}
