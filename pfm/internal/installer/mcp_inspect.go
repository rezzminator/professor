package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	pfmengine "hostops/pfm/internal/engine"
)

const (
	MCPClientAbsent              = "absent"
	MCPClientPFM                 = "pfm"
	MCPClientLegacyStandalone    = "legacy-standalone"
	MCPClientForeignRegistration = "foreign-registration"
	MCPClientPartial             = "partial"
	MCPClientUnreadable          = "unreadable"
)

// MCPClientCutover is one consumer's visible route for one server. Unreadable
// is a first-class state so doctor cannot mistake a failed inspection for
// cutover. Name is the server this report classifies ("harvester", "chat");
// callers that only ever inspect one server may ignore it.
type MCPClientCutover struct {
	Client string
	Name   string
	Path   string
	State  string
	Error  error
}

type mcpClientRegistration struct {
	Type    string            `json:"type" toml:"type"`
	Command string            `json:"command" toml:"command"`
	Args    []string          `json:"args" toml:"args"`
	URL     string            `json:"url" toml:"url"`
	Headers map[string]string `json:"headers" toml:"headers"`
	Env     map[string]string `json:"env" toml:"env"`
}

// InspectHarvesterClientCutover inspects both supported client config files.
// It never mutates a foreign registration; doctor turns non-PFM states into an
// actionable warning for the operator completing the standalone migration.
// registries is mandatory: a nil list is a programming error (the caller must
// resolve the actual registry roster — installer.ClaudeUserRegistries for
// Claude's account fanout — never a silent single-file guess), and reports a
// single MCPClientUnreadable naming the missing list rather than inspecting
// an unrelated default. An explicitly empty (non-nil) slice means "no Claude
// registries to inspect" and is valid.
func InspectHarvesterClientCutover(home string, port int, registries, codexHomes []string) []MCPClientCutover {
	if registries == nil {
		return []MCPClientCutover{{
			Client: pfmengine.MustLookup(pfmengine.Claude).LongName,
			Name:   mcpServerHarvester,
			State:  MCPClientUnreadable,
			Error:  errors.New("no Claude registries supplied"),
		}}
	}
	if codexHomes == nil {
		codexHomes = []string{filepath.Join(home, ".codex")}
	}
	reports := []MCPClientCutover{}
	seen := map[string]bool{}
	for _, path := range registries {
		if !seen[path] {
			reports = append(reports, inspectClaudeServers(path, port, mcpServerHarvester)...)
			seen[path] = true
		}
	}
	for _, dir := range codexHomes {
		reports = append(reports, inspectCodexHarvester(filepath.Join(dir, "config.toml"), port))
	}
	// Root .mcp.json is historical/project-scope evidence, not Claude user scope.
	reports = append(reports, inspectClaudeServers(filepath.Join(home, ".mcp.json"), port, mcpServerHarvester)...)
	return reports
}

// InspectClaudeServers is InspectHarvesterClientCutover's per-registry,
// per-server primitive exported for doctor's registry+reason row, which needs
// both "harvester" and "chat" classified for the same path in one call.
func InspectClaudeServers(path string, port int, names ...string) []MCPClientCutover {
	return inspectClaudeServers(path, port, names...)
}

// InspectOpenCodeServers classifies the two machine-scope OpenCode MCP
// registrations. OpenCode accepts JSONC, so this uses the same parser as the
// writer and reports malformed config as unreadable instead of absent.
func InspectOpenCodeServers(path, home string, port int, names ...string) []MCPClientCutover {
	base := func(name string) MCPClientCutover {
		return MCPClientCutover{
			Client: pfmengine.MustLookup(pfmengine.OpenCode).LongName,
			Name:   name,
			Path:   path,
			State:  MCPClientAbsent,
		}
	}
	uniform := func(state string, err error) []MCPClientCutover {
		reports := make([]MCPClientCutover, len(names))
		for index, name := range names {
			report := base(name)
			report.State, report.Error = state, err
			reports[index] = report
		}
		return reports
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return uniform(MCPClientAbsent, nil)
	}
	if err != nil {
		return uniform(MCPClientUnreadable, fmt.Errorf("read %s: %w", path, err))
	}
	document, err := decodeJSONCObject(raw)
	if err != nil {
		return uniform(MCPClientUnreadable, fmt.Errorf("parse %s: %w", path, err))
	}
	servers := map[string]any{}
	if value, present := document["mcp"]; present {
		var ok bool
		servers, ok = value.(map[string]any)
		if !ok || servers == nil {
			return uniform(MCPClientUnreadable, fmt.Errorf("parse %s: mcp must be an object", path))
		}
	}
	reports := make([]MCPClientCutover, 0, len(names))
	for _, name := range names {
		report := base(name)
		registration, present := servers[name]
		if present {
			shape, ok := registration.(map[string]any)
			if !ok || shape == nil {
				report.State = MCPClientUnreadable
				report.Error = fmt.Errorf("parse %s %s registration: expected object", path, name)
			} else {
				report.State = classifyOpenCodeRegistration(name, shape, home, port)
			}
		}
		reports = append(reports, report)
	}
	return reports
}

func classifyOpenCodeRegistration(name string, registration map[string]any, home string, port int) string {
	_, hasType := registration["type"]
	_, hasEnabled := registration["enabled"]
	if !hasType || !hasEnabled {
		return MCPClientPartial
	}
	enabled, enabledOK := registration["enabled"].(bool)
	typeName, typeOK := registration["type"].(string)
	if !enabledOK || !enabled || !typeOK {
		return MCPClientForeignRegistration
	}
	switch name {
	case chatName:
		if typeName != "local" {
			return MCPClientForeignRegistration
		}
		value, present := registration["command"]
		if !present {
			return MCPClientPartial
		}
		command, ok := value.([]any)
		if !ok || len(command) != 4 {
			return MCPClientForeignRegistration
		}
		for index, want := range []string{
			filepath.Join(home, ".local", "bin", "pfm"), mcpCommand, chatName, mcpServeCommand,
		} {
			got, ok := command[index].(string)
			if !ok || got != want {
				return MCPClientForeignRegistration
			}
		}
		if len(registration) != 3 {
			return MCPClientForeignRegistration
		}
		return MCPClientPFM
	case mcpServerHarvester:
		value, present := registration["url"]
		if !present {
			return MCPClientPartial
		}
		url, ok := value.(string)
		if !ok || typeName != "remote" || url != fmt.Sprintf("http://127.0.0.1:%d/mcp/%s", port, name) {
			return MCPClientForeignRegistration
		}
		if len(registration) != 3 {
			return MCPClientForeignRegistration
		}
		return MCPClientPFM
	default:
		return MCPClientForeignRegistration
	}
}

// inspectClaudeServers classifies every name's registration in path's
// mcpServers object, one report per name (in the order given). A missing or
// unreadable file/document reports every name Absent/Unreadable identically —
// there is only one file to blame, not one per server.
func inspectClaudeServers(path string, port int, names ...string) []MCPClientCutover {
	client := pfmengine.MustLookup(pfmengine.Claude).LongName
	base := func(name string) MCPClientCutover {
		return MCPClientCutover{Client: client, Name: name, State: MCPClientAbsent, Path: path}
	}
	uniform := func(state string, err error) []MCPClientCutover {
		reports := make([]MCPClientCutover, len(names))
		for index, name := range names {
			report := base(name)
			report.State, report.Error = state, err
			reports[index] = report
		}
		return reports
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return uniform(MCPClientAbsent, nil)
	}
	if err != nil {
		return uniform(MCPClientUnreadable, fmt.Errorf("read %s: %w", path, err))
	}
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return uniform(MCPClientUnreadable, fmt.Errorf("parse %s: %w", path, err))
	}
	reports := make([]MCPClientCutover, 0, len(names))
	for _, name := range names {
		report := base(name)
		encoded, present := document.Servers[name]
		if present {
			var registration mcpClientRegistration
			if err := json.Unmarshal(encoded, &registration); err != nil {
				report.State, report.Error = MCPClientUnreadable, fmt.Errorf(
					"parse %s %s registration: %w",
					path,
					name,
					err,
				)
			} else {
				report.State = classifyRegistration(name, registration, port)
			}
		}
		reports = append(reports, report)
	}
	return reports
}

func inspectCodexHarvester(path string, port int) MCPClientCutover {
	report := MCPClientCutover{
		Client: pfmengine.MustLookup(pfmengine.Codex).LongName,
		State:  MCPClientAbsent,
		Path:   path,
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return report
	}
	if err != nil {
		report.State, report.Error = MCPClientUnreadable, fmt.Errorf("read %s: %w", path, err)
		return report
	}
	var document struct {
		Servers map[string]mcpClientRegistration `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(raw), &document); err != nil {
		report.State, report.Error = MCPClientUnreadable, fmt.Errorf("parse %s: %w", path, err)
		return report
	}
	registration, present := document.Servers[mcpServerHarvester]
	if !present {
		return report
	}
	report.State = classifyRegistration(mcpServerHarvester, registration, port)
	return report
}

// classifyRegistration is the one implementation shared by every server
// inspection (Claude's stdio "chat", Claude's and Codex's HTTP "harvester"):
// name binds both the expected HTTP URL (/mcp/<name>) and the expected stdio
// argv (mcp <name> serve) to the server actually being classified, so a
// second copy never drifts as a new MCP server is added.
func classifyRegistration(name string, registration mcpClientRegistration, port int) string {
	wantedURL := fmt.Sprintf("http://127.0.0.1:%d/mcp/%s", port, name)
	typeName := strings.ToLower(strings.TrimSpace(registration.Type))
	command := strings.TrimSpace(registration.Command)
	if command != "" {
		command = filepath.Base(command)
	}
	command = strings.ToLower(command)
	noExtras := len(registration.Headers) == 0 && len(registration.Env) == 0
	if registration.URL == wantedURL && (typeName == "" || typeName == httpProtocol) && command == "" &&
		len(registration.Args) == 0 &&
		noExtras {
		return MCPClientPFM
	}
	if command == MCPClientPFM && registration.URL == "" && noExtras &&
		containsArgumentSequence(registration.Args, mcpCommand, name, mcpServeCommand) {
		return MCPClientPFM
	}
	if name != mcpServerHarvester {
		return MCPClientForeignRegistration
	}
	joined := strings.ToLower(strings.Join(registration.Args, " "))
	if strings.Contains(command, "harvest") || strings.Contains(joined, "harvest") || command == "uv" {
		return MCPClientLegacyStandalone
	}
	return MCPClientForeignRegistration
}

func containsArgumentSequence(args []string, sequence ...string) bool {
	if len(sequence) == 0 || len(args) < len(sequence) {
		return false
	}
	for start := 0; start <= len(args)-len(sequence); start++ {
		matched := true
		for offset, wanted := range sequence {
			if !strings.EqualFold(strings.TrimSpace(args[start+offset]), wanted) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
