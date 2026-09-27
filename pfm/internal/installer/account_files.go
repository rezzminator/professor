package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

func accountSettingsLeftovers(
	raw []byte,
	home string,
	owned settingsHookCounts,
	ledgerReadable bool,
) ([]string, error) {
	document, err := parseAccountDocument(raw)
	if err != nil {
		return nil, err
	}
	if err := validateAccountHooks(document); err != nil {
		return nil, err
	}
	leftovers := settingsLeftovers(document, home, owned, ledgerReadable)
	return leftovers, nil
}

func stripAccountSettings(raw []byte, home string, owned settingsHookCounts) ([]byte, []string, error) {
	document, err := parseAccountDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	if err := validateAccountHooks(document); err != nil {
		return nil, nil, err
	}
	removed := settingsLeftovers(document, home, owned, true)
	if len(removed) == 0 {
		return raw, nil, nil
	}
	commands := map[string]bool{}
	for _, hook := range claudeHookTemplates(home) {
		commands[hook.Command] = true
	}
	counts := settingsHookCounts{}
	for key, count := range countSettingsHookCommands(document) {
		if commands[key.Command] {
			counts[key] = count
		} else if owned[key] > 0 {
			counts[key] = owned[key]
		}
	}
	dropMisplacedTemplateHooks(document, claudeHookTemplates(home), home+"/.local/bin/pfm")
	removeOwnedSettingsHooks(document, counts)
	removeRetiredHookCommands(document, home+"/.local/bin/pfm")
	pruneAccountHooks(document)
	if status, ok := document["statusLine"].(map[string]any); ok && ownedStatusCommand(home, status[configCommandKey]) {
		delete(document, "statusLine")
	}
	if status, ok := document[subagentStatusLineKey].(map[string]any); ok &&
		status[configCommandKey] == claudelaunch.SubagentStatusLineCommand(home) {
		delete(document, subagentStatusLineKey)
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("encode account settings: %w", err)
	}
	return append(updated, '\n'), removed, nil
}

func settingsLeftovers(document map[string]any, home string, owned settingsHookCounts, ledgerReadable bool) []string {
	commands := map[string]bool{}
	for _, hook := range claudeHookTemplates(home) {
		commands[hook.Command] = true
	}
	leftovers := []string{}
	for key, count := range countSettingsHookCommands(document) {
		if count > 0 &&
			(commands[key.Command] || isRetiredHookCommand(key.Command, home+"/.local/bin/pfm") || ledgerReadable && owned[key] > 0) {
			leftovers = append(leftovers, "hooks")
			break
		}
	}
	if status, ok := document["statusLine"].(map[string]any); ok && ownedStatusCommand(home, status[configCommandKey]) {
		leftovers = append(leftovers, "statusLine")
	}
	if status, ok := document[subagentStatusLineKey].(map[string]any); ok &&
		status[configCommandKey] == claudelaunch.SubagentStatusLineCommand(home) {
		leftovers = append(leftovers, "subagentStatusLine")
	}
	return leftovers
}

func ownedStatusCommand(home string, value any) bool {
	command, ok := value.(string)
	return ok && (command == claudelaunch.StatusLineCommand(home) || command == home+"/.local/bin/pfm statusline")
}

func parseAccountDocument(raw []byte) (map[string]any, error) {
	var document map[string]any
	if err := unmarshalKeepingNumbers(raw, &document); err != nil {
		return nil, err
	}
	if document == nil {
		return nil, fmt.Errorf("account file must be an object")
	}
	return document, nil
}

func validateAccountHooks(document map[string]any) error {
	value, present := document["hooks"]
	if !present {
		return nil
	}
	events, ok := value.(map[string]any)
	if !ok || events == nil {
		return fmt.Errorf("hooks must be an object")
	}
	for event, value := range events {
		entries, ok := value.([]any)
		if !ok {
			return fmt.Errorf("hooks.%s must be an array", event)
		}
		for _, value := range entries {
			entry, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("hooks.%s entry must be an object", event)
			}
			hooks, ok := entry["hooks"].([]any)
			if !ok {
				return fmt.Errorf("hooks.%s entry hooks must be an array", event)
			}
			for _, value := range hooks {
				if _, ok := value.(map[string]any); !ok {
					return fmt.Errorf("hooks.%s hook must be an object", event)
				}
			}
		}
	}
	return nil
}

func pruneAccountHooks(document map[string]any) {
	events, ok := document["hooks"].(map[string]any)
	if !ok {
		return
	}
	for event, value := range events {
		entries := value.([]any)
		if len(entries) == 0 {
			delete(events, event)
		}
	}
	if len(events) == 0 {
		delete(document, "hooks")
	}
}

// mcpShaped reports whether a registration under name is in a pfm shape; nil
// judges by the ledger alone.
type mcpShaped func(name string, registration map[string]any) bool

// accountMCPLeftovers names the mcpServers entries pfm owns in a Claude MCP
// file: the ledger-owned names present plus the shape-matched ones.
func accountMCPLeftovers(raw []byte, owned []string, shaped mcpShaped) ([]string, error) {
	document, err := parseAccountDocument(raw)
	if err != nil {
		return nil, err
	}
	servers, err := accountMCPServers(document)
	if err != nil {
		return nil, err
	}
	return ownedMCPNames(servers, owned, shaped), nil
}

func stripAccountMCP(raw []byte, owned []string, shaped mcpShaped) ([]byte, []string, error) {
	document, err := parseAccountDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	servers, err := accountMCPServers(document)
	if err != nil {
		return nil, nil, err
	}
	removed := ownedMCPNames(servers, owned, shaped)
	if len(removed) == 0 {
		return raw, nil, nil
	}
	for _, name := range removed {
		delete(servers, strings.TrimPrefix(name, "mcpServers."))
	}
	if len(servers) == 0 {
		delete(document, "mcpServers")
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("encode account MCP registry: %w", err)
	}
	return append(updated, '\n'), removed, nil
}

func accountMCPServers(document map[string]any) (map[string]any, error) {
	value, present := document["mcpServers"]
	if !present {
		return nil, nil
	}
	servers, ok := value.(map[string]any)
	if !ok || servers == nil {
		return nil, fmt.Errorf("mcpServers must be an object")
	}
	return servers, nil
}

func ownedMCPNames(servers map[string]any, owned []string, shaped mcpShaped) []string {
	seen := map[string]bool{}
	for _, name := range owned {
		if _, present := servers[name]; present {
			seen[name] = true
		}
	}
	if shaped != nil {
		for _, name := range []string{chatName, mcpServerHarvester, professorName} {
			if registration, ok := servers[name].(map[string]any); ok && shaped(name, registration) {
				seen[name] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, "mcpServers."+name)
	}
	sort.Strings(result)
	return result
}

// classifyAccountSettings judges each physical settings file once: accounts
// whose settings.json resolves to one file share it, the first carries the
// verdict for that file and the others report ok, shared. A settings.json link
// is shared settings only when it resolves to a regular file inside HOME.
func classifyAccountSettings(env LayoutEnv) []LayoutFinding {
	dirs := accountDirs(env)
	ledgerPath := settingsHookOwnershipPath(env.ManagedRoot)
	ownership, _, ledgerErr := readSettingsHookOwnership(ledgerPath)
	sharers := map[string][]string{}
	for _, dir := range dirs {
		physical := physicalSettingsPath(filepath.Join(dir, "settings.json"))
		sharers[physical] = append(sharers[physical], dir)
	}
	findings := make([]LayoutFinding, 0, len(dirs))
	for _, dir := range dirs {
		path := filepath.Join(dir, "settings.json")
		physical := physicalSettingsPath(path)
		if first := sharers[physical][0]; first != dir {
			findings = append(findings, LayoutFinding{
				Row: layoutRowAccountSettings, Verdict: VerdictOK, Path: path, Source: physical,
				Detail: "shared with " + filepath.Join(first, "settings.json"),
			})
			continue
		}
		finding, info, exists := layoutLstat(layoutRowAccountSettings, path)
		if ledgerErr != nil {
			finding.Err, finding.Source, finding.Detail = ledgerErr, ledgerPath, layoutOwnershipLedger
		} else if finding.Err == nil && exists {
			judgeAccountSettings(env, &finding, info, ownership[physical])
		}
		live := []string{}
		for _, sharer := range sharers[physical] {
			pids, err := liveChatPIDs(env.ProcRoot, sharer)
			if err != nil {
				finding.Err = err
				break
			}
			live = append(live, pids...)
		}
		if finding.Err == nil && len(live) > 0 {
			finding.Verdict, finding.Detail = VerdictRefuse, "live chats: "+strings.Join(live, ",")
		}
		findings = append(findings, finding)
	}
	return findings
}

// judgeAccountSettings marks an existing account settings.json strip when it
// carries pfm-owned entries, reading through a link to its shared target.
func judgeAccountSettings(env LayoutEnv, finding *LayoutFinding, info fs.FileInfo, owned settingsHookCounts) {
	if info.Mode()&os.ModeSymlink != 0 {
		target, refusal, err := accountSettingsLink(env, finding.Path)
		finding.Source = target
		if err != nil || refusal != "" {
			finding.Err = err
			if refusal != "" {
				finding.Verdict, finding.Detail = VerdictRefuse, refusal
			}
			return
		}
	} else if !info.Mode().IsRegular() {
		finding.Verdict, finding.Detail = VerdictRefuse, layoutNotRegular
		return
	}
	raw, err := os.ReadFile(finding.Path)
	if err != nil {
		finding.Err = err
		return
	}
	leftovers, err := accountSettingsLeftovers(raw, env.Home, owned, true)
	if err != nil {
		finding.Err = err
	} else if len(leftovers) > 0 {
		finding.Verdict, finding.Detail = VerdictStrip, strings.Join(leftovers, ",")
	}
}

// accountSettingsLink resolves an account settings.json link: its target when
// that is a regular file inside HOME, otherwise the reason it is refused.
func accountSettingsLink(env LayoutEnv, path string) (target, refusal string, err error) {
	target, err = filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "dangling link", nil
	}
	if err != nil {
		return "", "", err
	}
	inside := false
	for _, home := range layoutHomes(env) {
		inside = inside || pathWithin(target, home)
	}
	if !inside {
		return target, "link outside HOME: " + target, nil
	}
	info, err := os.Stat(target)
	if err != nil {
		return target, "", err
	}
	if !info.Mode().IsRegular() {
		return target, layoutNotRegular, nil
	}
	return target, "", nil
}
