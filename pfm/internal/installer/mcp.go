package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const (
	mcpOwnershipName = "mcp-ownership.json"
	mcpFenceBegin    = "# BEGIN pfm mcp_servers — installer-owned"
	mcpFenceEnd      = "# END pfm mcp_servers — installer-owned"
	chatName         = "chat"
	mcpCommand       = "mcp"
	mcpServeCommand  = "serve"
	httpProtocol     = "http"
	stdioProtocol    = "stdio"
	openCodeLocal    = "local"
)

// mcpStdioArgs is the stdio serve argv every family registration extends with
// its --family (mcpFamilyStdioArgs).
var mcpStdioArgs = []string{mcpCommand, mcpServeCommand, "--stdio"}

type mcpOwnership struct {
	OpenCodePending       map[string]map[string]any `json:"opencodePending,omitempty"`
	OpenCodeRegistrations map[string]map[string]any `json:"opencodeRegistrations,omitempty"`
}

func (installer *engine) mcpAnyEnabled() bool {
	for _, enabled := range installer.options.MCPEnabled {
		if enabled {
			return true
		}
	}
	return false
}

func (installer *engine) mcpOwnershipPath() string {
	return filepath.Join(installer.managedRoot, mcpOwnershipName)
}

// wireMCP wires every MCP client; one registry or client that fails costs
// itself alone.
func (installer *engine) wireMCP() error {
	if installer.options.Mode == ModeUninstall || !installer.mcpAnyEnabled() {
		return installer.removeMCPClientRegistrations()
	}
	names := enabledMCPNames(installer.options.MCPEnabled)
	return errors.Join(
		installer.writeMCPCodeConfig(names),
		installer.writeMCPOpenCodeJSON(names),
	)
}

func (installer *engine) writeMCPCodeConfig(names []string) error {
	// One unreadable or unparseable home costs itself alone.
	var failures []error
	for _, home := range installer.codexHomes() {
		if err := installer.writeMCPCodeConfigAt(filepath.Join(home, "config.toml"), names); err != nil {
			failures = append(failures, installer.fail(err))
		}
	}
	return errors.Join(failures...)
}

func (installer *engine) writeMCPCodeConfigAt(path string, names []string) error {
	raw, existed, err := readMCPFile(path)
	if err != nil {
		return err
	}
	kept, foreign, _, err := installer.codexMCPYield(string(raw))
	if err != nil {
		return fmt.Errorf("read Codex MCP config %s: %w", path, err)
	}
	var generated []string
	for _, name := range names {
		body, err := codexStdioBody(mcpFamilyBinary(installer.options.Home), name)
		if err != nil {
			return fmt.Errorf("encode Codex MCP registration %s: %w", name, err)
		}
		if _, present := foreign[name]; present {
			installer.skip("preserve conflicting manual MCP client " + name + " in " + path)
			continue
		}
		generated = append(generated, "[mcp_servers."+name+"]")
		generated = append(generated, body...)
	}
	wantedLines := append([]string{}, kept...)
	if len(generated) > 0 {
		wantedLines = append(wantedLines, mcpFenceBegin)
		wantedLines = append(wantedLines, generated...)
		wantedLines = append(wantedLines, mcpFenceEnd)
	}
	wanted := strings.TrimRight(strings.Join(wantedLines, "\n"), "\n") + "\n"
	if string(raw) == wanted {
		installer.ok(path + " wiring")
		return nil
	}
	return installer.changeMCPFile(changeDescription(path, existed), path, raw, []byte(wanted), existed)
}

// codexMCPYield reads the hand-written view before any install step decodes
// the complete config. A matching manual table makes pfm yield its fenced
// registration; wireMCP uses the same view to preserve that table.
func (installer *engine) codexMCPYield(raw string) ([]string, map[string]any, string, error) {
	var lines []string
	if raw != "" {
		lines = strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	}
	kept, err := stripPFMCodexLines(lines, installer.options.Home)
	if err != nil {
		return nil, nil, "", err
	}
	var foreign struct {
		Servers map[string]any `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(strings.Join(kept, "\n"), &foreign); err != nil {
		return nil, nil, "", fmt.Errorf("parse unmanaged Codex MCP config: %w", err)
	}
	yielded := raw
	inside := false
	for _, line := range lines {
		switch line {
		case mcpFenceBegin:
			inside = true
		case mcpFenceEnd:
			inside = false
		default:
			if inside && strings.HasPrefix(line, "[mcp_servers.") && strings.HasSuffix(line, "]") {
				name := strings.TrimSuffix(strings.TrimPrefix(line, "[mcp_servers."), "]")
				if _, present := foreign.Servers[name]; present {
					yielded = strings.TrimRight(strings.Join(kept, "\n"), "\n") + "\n"
				}
			}
		}
	}
	return kept, foreign.Servers, yielded, nil
}

// stripPFMCodexLines removes every line pfm owns in a Codex config.toml: each
// paired installer fence with its body, every orphan fence marker, and a
// family table left outside a fence (its END line deleted by hand) whose whole
// body is the one install writes (codexStdioBody) — each with one blank line
// right after it, and only with no [mcp_servers.<key>.*] sub-table anywhere in
// the file. A run of blank lines a removal leaves collapses to one. Every other
// line — a hand-written table included — is returned byte-identical. Install
// strips with it, and doctor calls a Codex table pfm's only when it goes.
func stripPFMCodexLines(lines []string, home string) ([]string, error) {
	name := pfmconfig.MCPRegistrationName(chatName)
	body, err := codexStdioBody(mcpFamilyBinary(home), name)
	if err != nil {
		return nil, fmt.Errorf("encode Codex MCP registration %s: %w", name, err)
	}
	removed := make([]bool, len(lines))
	open := -1
	for index, line := range lines {
		switch line {
		case mcpFenceBegin:
			open = index
		case mcpFenceEnd:
			if open >= 0 {
				for inner := open; inner <= index; inner++ {
					removed[inner] = true
				}
				open = -1
			}
		}
	}
	for index, line := range lines {
		if line == mcpFenceBegin || line == mcpFenceEnd {
			removed[index] = true
		}
	}
	removeExactCodexTable(lines, removed, name, body)
	kept := make([]string, 0, len(lines))
	cut := false
	for index, line := range lines {
		if removed[index] {
			cut = true
			continue
		}
		blank := strings.TrimSpace(line) == ""
		if blank && cut && len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
			continue
		}
		if !blank {
			cut = false
		}
		kept = append(kept, line)
	}
	return kept, nil
}

// removeExactCodexTable marks removed every [mcp_servers.<name>] table not
// already removed whose non-blank body is exactly body, in order, and that has
// no sub-table, together with one blank line right after its last body line.
func removeExactCodexTable(lines []string, removed []bool, name string, body []string) {
	header := "[mcp_servers." + name + "]"
	for index, line := range lines {
		if removed[index] || line != header {
			continue
		}
		found := []int{}
		for next := index + 1; next < len(lines) && !isTOMLTableHeader(lines[next]); next++ {
			if !removed[next] && strings.TrimSpace(lines[next]) != "" {
				found = append(found, next)
			}
		}
		if len(found) != len(body) || hasTOMLSubTable(lines, removed, "mcp_servers."+name) {
			continue
		}
		exact := true
		for position, at := range found {
			exact = exact && lines[at] == body[position]
		}
		if !exact {
			continue
		}
		removed[index] = true
		for _, at := range found {
			removed[at] = true
		}
		if last := found[len(found)-1]; last+1 < len(lines) && !removed[last+1] &&
			strings.TrimSpace(lines[last+1]) == "" {
			removed[last+1] = true
		}
	}
}

func isTOMLTableHeader(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "[")
}

// hasTOMLSubTable is true when a line not already removed opens a sub-table of
// table ([table.x] or [[table.x]]): that sub-table is part of table's body.
func hasTOMLSubTable(lines []string, removed []bool, table string) bool {
	for index, line := range lines {
		trimmed := strings.TrimLeft(strings.TrimSpace(line), "[")
		if !removed[index] && isTOMLTableHeader(line) && strings.HasPrefix(trimmed, table+".") {
			return true
		}
	}
	return false
}

// OpenCodeConfigPath is the machine-scope JSONC registry OpenCode reads.
// OpenCode has one user config, unlike Codex's per-account TOML homes.
func OpenCodeConfigPath(home string) string {
	return filepath.Join(home, ".config", pfmengine.MustLookup(pfmengine.OpenCode).LongName, "opencode.jsonc")
}

func (installer *engine) mcpOpenCodeRegistration(name string) map[string]any {
	return map[string]any{
		"type":           openCodeLocal,
		configCommandKey: append([]string{mcpFamilyBinary(installer.options.Home)}, mcpFamilyStdioArgs(name)...),
		"enabled":        true,
	}
}

func (installer *engine) writeMCPOpenCodeJSON(names []string) error {
	path := strings.TrimSpace(installer.options.OpenCodeConfigPath)
	if path == "" {
		return nil
	}
	path = physicalSettingsPath(path)
	ownership, err := installer.loadMCPOwnership()
	if err != nil {
		return err
	}
	if ownership.OpenCodeRegistrations == nil {
		ownership.OpenCodeRegistrations = map[string]map[string]any{}
	}
	if ownership.OpenCodePending == nil {
		ownership.OpenCodePending = map[string]map[string]any{}
	}
	original, existed, err := readMCPFile(path)
	if err != nil {
		return fmt.Errorf("read OpenCode MCP config %s: %w", path, err)
	}
	base := original
	if !existed {
		base = []byte("{}\n")
	}
	document, err := decodeJSONCObject(base)
	if err != nil {
		return fmt.Errorf("parse OpenCode MCP config %s: %w", path, err)
	}
	servers := map[string]any{}
	if value, present := document["mcp"]; present {
		var ok bool
		servers, ok = value.(map[string]any)
		if !ok || servers == nil {
			return fmt.Errorf("OpenCode MCP config %s: mcp must be an object", path)
		}
	}
	owned := ownership.OpenCodeRegistrations[path]
	if owned == nil {
		owned = map[string]any{}
	}
	for name, registration := range ownership.OpenCodePending[path] {
		if sameJSONValue(servers[name], registration) {
			owned[name] = registration
		}
	}
	wanted := map[string]bool{}
	for _, name := range names {
		if isFamilyRegistration(name) {
			wanted[name] = true
		}
	}
	removeNames := map[string]bool{}
	setNames := map[string]map[string]any{}
	next := map[string]any{}
	for name, registration := range owned {
		current, present := servers[name]
		if !present || !sameJSONValue(current, registration) {
			continue
		}
		if wanted[name] {
			desired := installer.mcpOpenCodeRegistration(name)
			next[name] = desired
			if !sameJSONValue(current, desired) {
				setNames[name] = desired
			}
			continue
		}
		removeNames[name] = true
	}
	for name := range wanted {
		registration := installer.mcpOpenCodeRegistration(name)
		current, present := servers[name]
		_, ours := next[name]
		if present && !ours {
			installer.skip("preserve conflicting manual OpenCode MCP client " + name)
			continue
		}
		next[name] = registration
		if !present || !sameJSONValue(current, registration) {
			setNames[name] = registration
		}
	}
	wantedRaw, err := rewriteOpenCodeMCP(base, removeNames, setNames)
	if err != nil {
		return fmt.Errorf("plan OpenCode MCP config %s: %w", path, err)
	}
	ownership.OpenCodePending[path] = next
	changed := !bytes.Equal(base, wantedRaw)
	if changed {
		if err := installer.saveMCPOwnership(ownership); err != nil {
			return err
		}
		if err := installer.changeMCPFile(
			changeDescription(path, existed),
			path,
			original,
			wantedRaw,
			existed,
		); err != nil {
			return err
		}
	} else {
		installer.ok(path + " OpenCode MCP wiring")
	}
	if len(next) > 0 {
		ownership.OpenCodeRegistrations[path] = next
	} else {
		delete(ownership.OpenCodeRegistrations, path)
	}
	delete(ownership.OpenCodePending, path)
	return installer.saveMCPOwnership(ownership)
}

func (installer *engine) removeMCPOpenCodeJSON() error {
	path := strings.TrimSpace(installer.options.OpenCodeConfigPath)
	if path == "" {
		return nil
	}
	ownership, err := installer.loadMCPOwnership()
	if err != nil {
		return err
	}
	if ownership.OpenCodeRegistrations == nil && ownership.OpenCodePending == nil {
		return nil
	}
	return installer.writeMCPOpenCodeJSON(nil)
}

func (installer *engine) loadMCPOwnership() (mcpOwnership, error) {
	return readMCPOwnership(installer.mcpOwnershipPath())
}

// readMCPOwnership reads the install's record of every MCP registration it
// wrote. A ledger that is not there records nothing yet and is no error; one
// that cannot be read or decoded names the failure, so no caller reads "could
// not look" as "pfm owns nothing here".
func readMCPOwnership(path string) (mcpOwnership, error) {
	ownership := mcpOwnership{}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ownership, nil
	}
	if err != nil {
		return ownership, fmt.Errorf("read MCP ownership %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &ownership); err != nil {
		return ownership, fmt.Errorf("decode MCP ownership %s: %w", path, err)
	}
	return ownership, nil
}

// rewriteOpenCodeMCP edits only the top-level `mcp` object and leaves every
// comment and unowned top-level/server property byte-for-byte intact.
func rewriteOpenCodeMCP(raw []byte, removeNames map[string]bool, setNames map[string]map[string]any) ([]byte, error) {
	result := append([]byte(nil), raw...)
	var err error
	remove := make([]string, 0, len(removeNames))
	for name := range removeNames {
		remove = append(remove, name)
	}
	sort.Strings(remove)
	for _, name := range remove {
		result, err = editOpenCodeServer(result, name, nil, true)
		if err != nil {
			return nil, err
		}
	}
	set := make([]string, 0, len(setNames))
	for name := range setNames {
		set = append(set, name)
	}
	sort.Strings(set)
	for _, name := range set {
		registration := setNames[name]
		encoded, err := json.Marshal(registration)
		if err != nil {
			return nil, err
		}
		result, err = editOpenCodeServer(result, name, encoded, false)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func editOpenCodeServer(raw []byte, name string, value []byte, remove bool) ([]byte, error) {
	top, err := parseJSONCObject(raw, 0)
	if err != nil {
		return nil, err
	}
	mcpProperty, found := top.byName["mcp"]
	if !found {
		if remove {
			return raw, nil
		}
		// The caller's bytes go in verbatim, as the existing-object branch
		// writes them, so a second edit finds nothing to change.
		if !json.Valid(value) {
			return nil, errors.New("OpenCode mcp registration is not valid JSON")
		}
		encoded, err := json.Marshal(map[string]json.RawMessage{name: value})
		if err != nil {
			return nil, err
		}
		return setJSONCProperty(raw, 0, "mcp", encoded)
	}
	document, err := decodeJSONCObject(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := document["mcp"].(map[string]any); !ok {
		return nil, errors.New("OpenCode mcp property must be an object")
	}
	if remove {
		return removeJSONCProperty(raw, mcpProperty.valueStart, name)
	}
	return setJSONCProperty(raw, mcpProperty.valueStart, name, value)
}

func (installer *engine) removeMCPClientRegistrations() error {
	if err := installer.removeMCPCodeConfig(); err != nil {
		return err
	}
	return installer.removeMCPOpenCodeJSON()
}

func (installer *engine) removeMCPCodeConfig() error {
	for _, home := range installer.codexHomes() {
		if err := installer.removeMCPCodeConfigAt(filepath.Join(home, "config.toml")); err != nil {
			return err
		}
	}
	return nil
}

func (installer *engine) removeMCPCodeConfigAt(path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Codex MCP config for removal: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	kept, err := stripPFMCodexLines(lines, installer.options.Home)
	if err != nil {
		return fmt.Errorf("strip Codex MCP config %s: %w", path, err)
	}
	wanted := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if wanted != "" {
		wanted += "\n"
	}
	if wanted == string(raw) {
		return nil
	}
	return installer.changeMCPFile("rewrite "+path+" (remove pfm MCP registration)", path, raw, []byte(wanted), true)
}

func sameJSONValue(left, right any) bool {
	a, err := json.Marshal(left)
	if err != nil {
		return false
	}
	b, err := json.Marshal(right)
	return err == nil && bytes.Equal(a, b)
}
