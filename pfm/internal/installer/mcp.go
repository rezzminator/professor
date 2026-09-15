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
)

const (
	mcpCredentialName = "mcp-auth-token"
	mcpOwnershipName  = "mcp-ownership.json"
	mcpFenceBegin     = "# BEGIN pfm mcp_servers — installer-owned"
	mcpFenceEnd       = "# END pfm mcp_servers — installer-owned"
)

type mcpOwnership struct {
	Pending       map[string]map[string]any `json:"pending,omitempty"`
	Clients       []string                  `json:"clients,omitempty"`
	Registrations map[string]map[string]any `json:"registrations,omitempty"`
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
	if err := installer.removeLegacyMCPCredential(); err != nil {
		return err
	}
	return nil
}

func enabledMCPNames(servers map[string]bool) []string {
	names := make([]string, 0, len(servers))
	for name, enabled := range servers {
		if enabled && (name == "chat" || name == "harvester") {
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
	if name == "chat" {
		return map[string]any{
			"type":    "stdio",
			"command": installer.mcpChatCommand(),
			"args":    []string{"mcp", "chat", "serve"},
		}
	}
	return map[string]any{
		"type": "http",
		"url":  installer.mcpURL(name),
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
	if name != "chat" || len(registration) != 3 {
		return false
	}
	if registration["type"] != "stdio" || registration["command"] != installer.mcpChatCommand() {
		return false
	}
	args, ok := registration["args"].([]any)
	if !ok || len(args) != 3 {
		return false
	}
	for index, want := range []string{"mcp", "chat", "serve"} {
		if got, ok := args[index].(string); !ok || got != want {
			return false
		}
	}
	return true
}

func (installer *engine) isPFMHTTPClient(name string, registration map[string]any) bool {
	if registration["type"] != "http" || registration["url"] != installer.mcpURL(name) {
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

func (installer *engine) removeMCPClientRegistrations() error {
	if _, err := installer.writeMCPClientJSON(nil); err != nil {
		return err
	}
	if err := installer.removeMCPCodeConfig(); err != nil {
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
