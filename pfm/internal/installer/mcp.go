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

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
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
)

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

func enabledMCPNames(servers map[string]bool) []string {
	names := make([]string, 0, len(servers))
	for name, enabled := range servers {
		if enabled && (name == chatName || name == mcpServerHarvester) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
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

// mcpClientRegistration is the registration writeMCPClientJSON wants for name
// on the Claude side. Every server except "chat" keeps the shared HTTP
// daemon: Codex's own client attaches _meta.threadId on every call, so HTTP
// resolves callers correctly there too (see writeMCPCodeConfig, unchanged),
// and the daemon is the cheaper choice for a server callers don't need to
// self-identify against. "chat" is different: a self-addressed chat_* call
// carries no thread id from Claude, and the daemon serves every chat on the
// box from one process, so it can never derive who's calling (mcpserv's
// callerForRequest, fail-closed by design). `pfm mcp chat serve` run over
// stdio is launched by exactly one chat and inherits its identity instead
// (main.go sets AllowAmbientIdentity for that path only) — the only
// transport that can ever answer a self-addressed chat_* call correctly.
func (installer *engine) mcpClientRegistration(name string) map[string]any {
	if name == chatName {
		return map[string]any{
			configTypeKey:    "stdio",
			configCommandKey: installer.mcpChatCommand(),
			configArgsKey:    []string{mcpCommand, chatName, mcpServeCommand},
		}
	}
	return map[string]any{
		configTypeKey: httpProtocol,
		"url":         installer.mcpURL(name),
	}
}

// mcpChatCommand is the absolute path to the pfm binary this install owns —
// the same canonical ~/.local/bin/pfm path canonicalBinaryOwnershipContent
// records and updateCodexHooks already migrates hook commands to. The
// installer knows this path (it is what it stages and records ownership of),
// so the stdio "chat" registration uses it rather than a bare "pfm" that
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

// isPFMStdioClient recognizes pfm's own stdio "chat" registration — the
// shape mcpClientRegistration now writes — so a later install can maintain
// it (e.g. correct a changed binary path) instead of forever treating it as
// a manual conflict, mirroring isPFMHTTPClient for the HTTP shape every
// other server still uses. A registration that merely LOOKS similar (a
// hand-written entry using a bare "pfm" command, say) does not match this
// exact shape and is correctly left as a manual conflict — recognizing only
// what this installer itself would write is the whole point.
func (installer *engine) isPFMStdioClient(name string, registration map[string]any) bool {
	if name != chatName || len(registration) != 3 {
		return false
	}
	if registration[configTypeKey] != "stdio" || registration[configCommandKey] != installer.mcpChatCommand() {
		return false
	}
	args, ok := registration[configArgsKey].([]any)
	if !ok || len(args) != 3 {
		return false
	}
	for index, want := range []string{mcpCommand, chatName, mcpServeCommand} {
		if got, ok := args[index].(string); !ok || got != want {
			return false
		}
	}
	return true
}

func (installer *engine) isPFMHTTPClient(name string, registration map[string]any) bool {
	if registration[configTypeKey] != httpProtocol || registration["url"] != installer.mcpURL(name) {
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
	start, end := -1, -1
	for index, line := range lines {
		if line == mcpFenceBegin {
			start = index
		}
		if line == mcpFenceEnd && start >= 0 {
			end = index
		}
	}
	kept := lines
	if start >= 0 && end >= start {
		kept = append(append([]string{}, lines[:start]...), lines[end+1:]...)
	}
	var foreign struct {
		Servers map[string]any `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(strings.Join(kept, "\n"), &foreign); err != nil {
		return fmt.Errorf("parse unmanaged Codex MCP config %s: %w", path, err)
	}
	generated := []string{mcpFenceBegin}
	for _, name := range names {
		if _, present := foreign.Servers[name]; present {
			installer.skip("preserve conflicting manual MCP client " + name + " in " + path)
			continue
		}
		generated = append(generated,
			"[mcp_servers."+name+"]",
			"url = \""+installer.mcpURL(name)+"\"",
		)
	}
	generated = append(generated, mcpFenceEnd)
	wantedLines := append([]string{}, kept...)
	wantedLines = append(wantedLines, generated...)
	wanted := strings.TrimRight(strings.Join(wantedLines, "\n"), "\n") + "\n"
	if string(raw) == wanted {
		installer.ok(path + " wiring")
		return nil
	}
	return installer.change(changeDescription(path, existed), func() error {
		return installer.writeMCPFile(path, raw, []byte(wanted), existed)
	})
}

func (installer *engine) mcpURL(name string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/mcp/%s", installer.options.MCPPort, name)
}

// OpenCodeConfigPath is the machine-scope JSONC registry OpenCode reads.
// OpenCode has one user config, unlike Codex's per-account TOML homes.
func OpenCodeConfigPath(home string) string {
	return filepath.Join(home, ".config", pfmengine.MustLookup(pfmengine.OpenCode).LongName, "opencode.jsonc")
}

func (installer *engine) mcpOpenCodeRegistration(name string) map[string]any {
	if name == chatName {
		return map[string]any{
			"type":    "local",
			"command": []string{installer.mcpChatCommand(), mcpCommand, chatName, mcpServeCommand},
			"enabled": true,
		}
	}
	return map[string]any{
		"type":    "remote",
		"url":     installer.mcpURL(name),
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
		if name == chatName || name == mcpServerHarvester {
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
		if err := installer.change(changeDescription(path, existed), func() error {
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
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	start, end := -1, -1
	for index, line := range lines {
		if line == mcpFenceBegin {
			start = index
		}
		if line == mcpFenceEnd && start >= 0 {
			end = index
		}
	}
	if start < 0 || end < start {
		return nil
	}
	kept := append(append([]string{}, lines[:start]...), lines[end+1:]...)
	wanted := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if wanted != "" {
		wanted += "\n"
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
