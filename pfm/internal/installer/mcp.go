package installer

import (
	"bytes"
	"encoding/hex"
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
	mcpCredentialName  = "mcp-auth-token"
	mcpOwnershipName   = "mcp-ownership.json"
	mcpFenceBegin      = "# BEGIN pfm mcp_servers — installer-owned"
	mcpFenceEnd        = "# END pfm mcp_servers — installer-owned"
	chatName           = "chat"
	mcpCommand         = "mcp"
	mcpServeCommand    = "serve"
	mcpServerHarvester = "harvester"
	httpProtocol       = "http"
	professorName      = pfmconfig.MCPServerProfessor
	stdioProtocol      = "stdio"
	openCodeLocal      = "local"
)

// mcpStdioArgs is the argv every engine registers after the absolute pfm
// path: the one stdio server, `pfm mcp serve --stdio`.
var mcpStdioArgs = []string{mcpCommand, mcpServeCommand, "--stdio"}

// mcpLegacyChatArgs are the argvs pfm's own legacy stdio chat ran after the
// absolute pfm path: `pfm mcp chat serve`, and the older bare `pfm mcp`.
var mcpLegacyChatArgs = [][]string{{mcpCommand, chatName, mcpServeCommand}, {mcpCommand}}

// mcpLegacyNames are the keys pfm registered before the one professor
// server; install and uninstall remove pfm's own exact shapes under them.
var mcpLegacyNames = []string{chatName, mcpServerHarvester}

type mcpOwnership struct {
	Pending               map[string]map[string]any `json:"pending,omitempty"`
	Clients               []string                  `json:"clients,omitempty"`
	Registrations         map[string]map[string]any `json:"registrations,omitempty"`
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

func (installer *engine) mcpCredentialPath() string {
	return filepath.Join(installer.managedRoot, mcpCredentialName)
}

func (installer *engine) mcpOwnershipPath() string {
	return filepath.Join(installer.managedRoot, mcpOwnershipName)
}

func (installer *engine) wireMCP() error {
	if installer.options.Mode == ModeUninstall {
		return installer.removeMCPClientRegistrations()
	}
	if !installer.mcpAnyEnabled() {
		return installer.removeMCPClientRegistrations()
	}
	if err := installer.removeLegacyMCPConfigAuth(); err != nil {
		return err
	}
	names := enabledMCPNames(installer.options.MCPEnabled)
	wiredNames, err := installer.writeMCPClientJSON(names)
	if err != nil {
		return err
	}
	if err := installer.writeMCPCodeConfig(wiredNames); err != nil {
		return err
	}
	if err := installer.writeMCPOpenCodeJSON(wiredNames); err != nil {
		return err
	}
	if err := installer.removeLegacyMCPCredential(); err != nil {
		return err
	}
	return nil
}

// enabledMCPNames is the one professor registration when either family is
// enabled, and nothing otherwise.
func enabledMCPNames(servers map[string]bool) []string {
	if servers[chatName] || servers[mcpServerHarvester] {
		return []string{professorName}
	}
	return nil
}

func (installer *engine) removeLegacyMCPCredential() error {
	path := installer.mcpCredentialPath()
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect retired MCP credential %s: %w", path, err)
	}
	return installer.change("remove retired "+path, func() error { return os.Remove(path) })
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

// mcpClientRegistration is the Claude registration of professor. Every engine
// registers the stdio command; Codex's _meta.threadId passes through the
// forwarder unchanged.
func (installer *engine) mcpClientRegistration() map[string]any {
	return map[string]any{
		configTypeKey:    stdioProtocol,
		configCommandKey: installer.mcpChatCommand(),
		configArgsKey:    append([]string{}, mcpStdioArgs...),
	}
}

// mcpChatCommand is the absolute path to the pfm binary this install owns —
// the same canonical ~/.local/bin/pfm path canonicalBinaryOwnershipContent
// records and updateCodexHooks already migrates hook commands to. The
// installer knows this path (it is what it stages and records ownership of),
// so the stdio professor registration uses it rather than a bare "pfm" that
// depends on the launching shell's PATH containing the install dir.
func (installer *engine) mcpChatCommand() string {
	return filepath.Join(installer.options.Home, ".local", "bin", "pfm")
}

// isPFMClient recognizes a registration as pfm's OWN, in whichever of the two
// shapes mcpClientRegistration produces for name, so writeMCPClientJSON can
// tell "ours, safe to maintain" from "a manual conflict, preserve as-is."
func (installer *engine) isPFMClient(name string, registration map[string]any) bool {
	return installer.isPFMHTTPClient(name, registration) || installer.isPFMStdioClient(name, registration)
}

// isPFMStdioClient recognizes pfm's own stdio "professor" registration — the
// shape mcpClientRegistration writes — so a later install can maintain it
// instead of forever treating it as a manual conflict. A registration that
// merely LOOKS similar (a hand-written entry using a bare "pfm" command, say)
// does not match this exact shape and is correctly left as a manual conflict —
// recognizing only what this installer itself would write is the whole point.
func (installer *engine) isPFMStdioClient(name string, registration map[string]any) bool {
	return name == professorName && installer.isExactStdioRegistration(registration, mcpStdioArgs)
}

// isPFMLegacyClient recognizes pfm's own registrations under the keys it wrote
// before the one professor server: the stdio chat entry (mcpLegacyChatArgs),
// and the loopback HTTP chat and harvester entries (bearer shape included).
func (installer *engine) isPFMLegacyClient(name string, registration map[string]any) bool {
	return isPFMLegacyClaudeShape(name, registration, installer.mcpChatCommand(), installer.options.MCPPort)
}

// isPFMLegacyClaudeShape is the one exact test of pfm's legacy Claude shapes,
// shared by install (which removes what it matches) and doctor (which
// prescribes that install only for what it matches).
func isPFMLegacyClaudeShape(name string, registration map[string]any, bin string, port int) bool {
	switch name {
	case chatName:
		for _, args := range mcpLegacyChatArgs {
			if isExactStdioShape(registration, bin, args) {
				return true
			}
		}
		return isPFMHTTPShape(registration, legacyMCPURL(port, name))
	case mcpServerHarvester:
		return isPFMHTTPShape(registration, legacyMCPURL(port, name))
	}
	return false
}

// isExactStdioRegistration is true for exactly {type: stdio, command: the
// absolute pfm path, args: want} and nothing more.
func (installer *engine) isExactStdioRegistration(registration map[string]any, want []string) bool {
	return isExactStdioShape(registration, installer.mcpChatCommand(), want)
}

func isExactStdioShape(registration map[string]any, bin string, want []string) bool {
	registration = withoutEmptyEnv(registration)
	if len(registration) != 3 {
		return false
	}
	if registration[configTypeKey] != stdioProtocol || registration[configCommandKey] != bin {
		return false
	}
	args, ok := registration[configArgsKey].([]any)
	return ok && sameStrings(args, want)
}

// isPFMLegacyOpenCodeClient recognizes pfm's own OpenCode registrations under
// the legacy keys: the local `pfm mcp chat serve` chat entry and the loopback
// remote chat and harvester entries, each exactly three keys.
func (installer *engine) isPFMLegacyOpenCodeClient(name string, registration map[string]any) bool {
	if (name != chatName && name != mcpServerHarvester) || len(registration) != 3 || registration["enabled"] != true {
		return false
	}
	switch registration[configTypeKey] {
	case openCodeLocal:
		command, ok := registration["command"].([]any)
		return name == chatName && ok &&
			sameStrings(command, []string{installer.mcpChatCommand(), mcpCommand, chatName, mcpServeCommand})
	case "remote":
		return registration["url"] == installer.mcpURL(name)
	}
	return false
}

func sameStrings(values []any, want []string) bool {
	if len(values) != len(want) {
		return false
	}
	for index, value := range values {
		if got, ok := value.(string); !ok || got != want[index] {
			return false
		}
	}
	return true
}

func (installer *engine) isPFMHTTPClient(name string, registration map[string]any) bool {
	return isPFMHTTPShape(registration, installer.mcpURL(name))
}

func isPFMHTTPShape(registration map[string]any, url string) bool {
	registration = withoutEmptyEnv(registration)
	if registration[configTypeKey] != httpProtocol || registration["url"] != url {
		return false
	}
	if len(registration) == 2 {
		return true
	}
	if len(registration) != 3 {
		return false
	}
	// Recognize only PFM's retired exact bearer shape so an owned registration
	// can be migrated. Foreign headers remain a manual conflict.
	headers, ok := registration["headers"].(map[string]any)
	if !ok || len(headers) != 1 {
		return false
	}
	authorization, ok := headers["Authorization"].(string)
	if !ok || !strings.HasPrefix(authorization, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	return len(token) == 64 && isHex(token)
}

func (installer *engine) writeMCPCodeConfig(names []string) error {
	for _, home := range installer.codexHomes() {
		if err := installer.writeMCPCodeConfigAt(filepath.Join(home, "config.toml"), names); err != nil {
			return err
		}
	}
	return nil
}

func (installer *engine) writeMCPCodeConfigAt(path string, names []string) error {
	raw, existed, err := readMCPFile(path)
	if err != nil {
		return err
	}
	var lines []string
	if len(raw) != 0 {
		lines = strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	}
	body, err := codexStdioBody(installer.mcpChatCommand())
	if err != nil {
		return fmt.Errorf("encode Codex MCP registration for %s: %w", path, err)
	}
	kept := stripPFMCodexLines(lines, installer.options.MCPPort, body)
	var foreign struct {
		Servers map[string]any `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(strings.Join(kept, "\n"), &foreign); err != nil {
		return fmt.Errorf("parse unmanaged Codex MCP config %s: %w", path, err)
	}
	var generated []string
	for _, name := range names {
		if _, present := foreign.Servers[name]; present {
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
	return installer.change(changeDescription(path, existed), func() error {
		return installer.writeMCPFile(path, raw, []byte(wanted), existed)
	})
}

// codexStdioBody is the body pfm writes under a Codex [mcp_servers.<name>]
// table — `command = "<bin>"` and `args = ["mcp", "serve", "--stdio"]` as the
// TOML encoder renders them — so the writer and the strip agree byte for byte.
func codexStdioBody(bin string) ([]string, error) {
	body := make([]string, 0, 2)
	for _, value := range []map[string]any{{configCommandKey: bin}, {configArgsKey: mcpStdioArgs}} {
		encoded, err := encodeCodexValues(value)
		if err != nil {
			return nil, err
		}
		body = append(body, strings.TrimSuffix(encoded, "\n"))
	}
	return body, nil
}

// stripPFMCodexLines removes every line pfm owns in a Codex config.toml: each
// paired installer fence with its body, every orphan fence marker, each
// legacy [mcp_servers.chat] / [mcp_servers.harvester] table whose whole body
// is the one loopback url line pfm wrote, and a [mcp_servers.professor]
// table left outside a fence (its END line deleted by hand) whose whole body
// is professorBody (codexStdioBody) — each with one blank line right after
// it, and only with no [mcp_servers.<key>.*] sub-table anywhere in the file.
// A run of blank lines a removal leaves collapses to one. Every other line —
// a hand-written table included — is returned byte-identical. Install strips
// with it, and doctor calls a Codex table pfm's only when it goes.
func stripPFMCodexLines(lines []string, port int, professorBody []string) []string {
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
	for _, name := range mcpLegacyNames {
		removeExactCodexTable(lines, removed, name, []string{"url = \"" + legacyMCPURL(port, name) + "\""})
	}
	if len(professorBody) > 0 {
		removeExactCodexTable(lines, removed, professorName, professorBody)
	}
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
	return kept
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

func (installer *engine) mcpURL(name string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/mcp/%s", installer.options.MCPPort, name)
}

// OpenCodeConfigPath is the machine-scope JSONC registry OpenCode reads.
// OpenCode has one user config, unlike Codex's per-account TOML homes.
func OpenCodeConfigPath(home string) string {
	return filepath.Join(home, ".config", pfmengine.MustLookup(pfmengine.OpenCode).LongName, "opencode.jsonc")
}

func (installer *engine) mcpOpenCodeRegistration() map[string]any {
	return map[string]any{
		"type":    openCodeLocal,
		"command": append([]string{installer.mcpChatCommand()}, mcpStdioArgs...),
		"enabled": true,
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
		if name == professorName {
			wanted[name] = true
		}
	}
	removeNames := map[string]bool{}
	removedLegacy := []string{}
	for _, name := range mcpLegacyNames {
		if registration, ok := servers[name].(map[string]any); ok &&
			installer.isPFMLegacyOpenCodeClient(name, registration) {
			removeNames[name] = true
			removedLegacy = append(removedLegacy, name)
			delete(owned, name)
		}
	}
	setNames := map[string]map[string]any{}
	next := map[string]any{}
	for name, registration := range owned {
		current, present := servers[name]
		if !present || !sameJSONValue(current, registration) {
			continue
		}
		if wanted[name] {
			desired := installer.mcpOpenCodeRegistration()
			next[name] = desired
			if !sameJSONValue(current, desired) {
				setNames[name] = desired
			}
			continue
		}
		removeNames[name] = true
	}
	for name := range wanted {
		registration := installer.mcpOpenCodeRegistration()
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
		message := changeDescription(path, existed)
		if len(removedLegacy) > 0 {
			message += " — remove pfm's legacy MCP clients " + strings.Join(removedLegacy, ",")
		}
		if err := installer.change(message, func() error {
			return installer.writeMCPFile(path, original, wantedRaw, existed)
		}); err != nil {
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
	// pfm's legacy entries go whether or not the ledger lists them, so only a
	// config that is not there and a ledger that owns nothing skip the pass.
	if ownership.OpenCodeRegistrations == nil && ownership.OpenCodePending == nil {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("inspect OpenCode MCP config %s: %w", path, err)
		}
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
		servers := map[string]any{}
		var registration map[string]any
		if err := json.Unmarshal(value, &registration); err != nil {
			return nil, err
		}
		servers[name] = registration
		encoded, err := json.Marshal(map[string]any{"mcp": servers})
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
	if _, err := installer.writeMCPClientJSON(nil); err != nil {
		return err
	}
	if err := installer.removeMCPCodeConfig(); err != nil {
		return err
	}
	if err := installer.removeMCPOpenCodeJSON(); err != nil {
		return err
	}
	if err := installer.removeLegacyMCPConfigAuth(); err != nil {
		return err
	}
	return installer.removeLegacyMCPCredential()
}

func (installer *engine) removeLegacyMCPConfigAuth() error {
	if installer.options.MCPConfigPath == "" {
		return nil
	}
	effective, err := pfmconfig.Load(installer.options.MCPConfigPath, installer.options.Home, nil)
	if err != nil {
		return fmt.Errorf("load MCP config for legacy auth cleanup: %w", err)
	}
	changed, err := pfmconfig.MCPAuthTokenPresent(effective)
	if err != nil {
		return fmt.Errorf("plan retired MCP installer credential removal: %w", err)
	}
	if !changed {
		return nil
	}
	return installer.change("remove retired MCP authToken from "+installer.options.MCPConfigPath, func() error {
		_, err := pfmconfig.RemoveMCPAuthToken(effective)
		return err
	})
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
	body, err := codexStdioBody(installer.mcpChatCommand())
	if err != nil {
		return fmt.Errorf("encode Codex MCP registration for %s: %w", path, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	kept := stripPFMCodexLines(lines, installer.options.MCPPort, body)
	wanted := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if wanted != "" {
		wanted += "\n"
	}
	if wanted == string(raw) {
		return nil
	}
	return installer.change("rewrite "+path+" (remove pfm MCP registration)", func() error {
		return installer.writeMCPFile(path, raw, []byte(wanted), true)
	})
}

func sameJSONValue(left, right any) bool {
	a, err := json.Marshal(left)
	if err != nil {
		return false
	}
	b, err := json.Marshal(right)
	return err == nil && bytes.Equal(a, b)
}

// withoutEmptyEnv is the one normalization every exact-shape comparison of a
// Claude registration applies: Claude Code adds `"env": {}` whenever it
// rewrites its config, so an env that is present but empty ({} or null) is
// shape-neutral and is dropped from the copy returned. A non-empty env, or any
// other value under the key, is kept, so the entry stays someone else's.
func withoutEmptyEnv(registration map[string]any) map[string]any {
	env, present := registration[configEnvKey]
	if !present {
		return registration
	}
	if env != nil {
		if values, ok := env.(map[string]any); !ok || len(values) != 0 {
			return registration
		}
	}
	neutral := make(map[string]any, len(registration)-1)
	for key, value := range registration {
		if key != configEnvKey {
			neutral[key] = value
		}
	}
	return neutral
}

// sameClaudeRegistration compares a registry's entry with a ledger receipt
// through withoutEmptyEnv, so Claude's own `"env": {}` never turns pfm's
// receipt into a manual conflict.
func sameClaudeRegistration(current, recorded any) bool {
	if registration, ok := current.(map[string]any); ok {
		current = withoutEmptyEnv(registration)
	}
	if registration, ok := recorded.(map[string]any); ok {
		recorded = withoutEmptyEnv(registration)
	}
	return sameJSONValue(current, recorded)
}
