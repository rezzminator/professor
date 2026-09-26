package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const (
	MCPClientAbsent              = "absent"
	MCPClientPFM                 = "pfm"
	MCPClientLegacyStandalone    = "legacy-standalone"
	MCPClientLegacyPFM           = "legacy-pfm"
	MCPClientForeignRegistration = "foreign-registration"
	MCPClientUnreadable          = "unreadable"
)

// MCPClientCutover is one consumer's visible route for one server. Unreadable
// is a first-class state so doctor cannot mistake a failed inspection for
// cutover. Name is the server this report classifies ("professor", "chat", "harvester");
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
			reports = append(reports, InspectClaudeServers(path, home, port, mcpServerHarvester)...)
			seen[path] = true
		}
	}
	for _, dir := range codexHomes {
		reports = append(
			reports,
			InspectCodexServers(filepath.Join(dir, "config.toml"), home, port, mcpServerHarvester)...)
	}
	// Root .mcp.json is historical/project-scope evidence, not Claude user scope.
	reports = append(reports, InspectClaudeServers(filepath.Join(home, ".mcp.json"), home, port, mcpServerHarvester)...)
	return reports
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
	servers, err := openCodeServers(path)
	if err != nil {
		return uniform(MCPClientUnreadable, err)
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

// openCodeServers reads path's top-level `mcp` object. A file that is not
// there holds no registration, which is an empty roster and no error; a file
// that cannot be read or parsed — or whose `mcp` is not an object — is an
// error naming the file, never an empty roster.
func openCodeServers(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	document, err := decodeJSONCObject(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	value, present := document["mcp"]
	if !present {
		return map[string]any{}, nil
	}
	servers, ok := value.(map[string]any)
	if !ok || servers == nil {
		return nil, fmt.Errorf("parse %s: mcp must be an object", path)
	}
	return servers, nil
}

// OpenCodeUnownedEntries names, of the servers given, every one registered in
// path that pfm install did NOT write: install records each registration it
// owns in its ownership ledger and preserves any entry that ledger does not
// match (writeMCPOpenCodeJSON's "preserve conflicting manual OpenCode MCP
// client"); an entry equal to the ledger's pending record for the path is an
// interrupted install's own, adopted on the next run. So a rerun of `pfm install --yes` can never fix one of these.
// It is doctor's ground for naming a user-owned entry instead of prescribing
// a reinstall that would silently leave it in place. An unreadable config or
// ledger is an error — never an empty answer that reads as "all pfm's".
func OpenCodeUnownedEntries(home, path string, names ...string) ([]string, error) {
	servers, err := openCodeServers(path)
	if err != nil {
		return nil, err
	}
	ownership, err := readMCPOwnership(filepath.Join(managedRootForHome(home), mcpOwnershipName))
	if err != nil {
		return nil, err
	}
	owned := ownership.OpenCodeRegistrations[physicalSettingsPath(path)]
	pending := ownership.OpenCodePending[physicalSettingsPath(path)]
	unowned := []string{}
	for _, name := range names {
		current, present := servers[name]
		if !present {
			continue
		}
		if recorded, claimed := owned[name]; claimed && sameJSONValue(current, recorded) {
			continue
		}
		if recorded, claimed := pending[name]; claimed && sameJSONValue(current, recorded) {
			continue
		}
		unowned = append(unowned, name)
	}
	return unowned, nil
}

// classifyOpenCodeRegistration names one OpenCode entry's state: `professor`
// is pfm only in the exact local stdio shape install writes; pfm's legacy
// `chat` (local `pfm mcp chat serve`, or the loopback remote) and `harvester`
// (the loopback remote) entries are legacy-pfm; anything else is foreign.
func classifyOpenCodeRegistration(name string, registration map[string]any, home string, port int) string {
	if len(registration) != 3 || registration["enabled"] != true {
		return MCPClientForeignRegistration
	}
	bin := filepath.Join(home, ".local", "bin", "pfm")
	command, _ := registration["command"].([]any)
	switch {
	case name == professorName:
		if registration[configTypeKey] == openCodeLocal &&
			sameStrings(command, append([]string{bin}, mcpStdioArgs...)) {
			return MCPClientPFM
		}
	case name == chatName && registration[configTypeKey] == openCodeLocal:
		if sameStrings(command, []string{bin, mcpCommand, chatName, mcpServeCommand}) {
			return MCPClientLegacyPFM
		}
	case name == chatName || name == mcpServerHarvester:
		if registration[configTypeKey] == "remote" && registration["url"] == legacyMCPURL(port, name) {
			return MCPClientLegacyPFM
		}
	}
	return MCPClientForeignRegistration
}

// legacyMCPURL is the loopback URL pfm registered under a legacy key (`chat`,
// `harvester`) before every engine moved to the one stdio `professor`.
func legacyMCPURL(port int, key string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/mcp/%s", port, key)
}

// InspectClaudeServers classifies every name's registration in path's
// mcpServers object, one report per name (in the order given). A missing or
// unreadable file/document reports every name Absent/Unreadable identically —
// there is only one file to blame, not one per server. It is
// InspectHarvesterClientCutover's per-registry, per-server primitive, exported
// for doctor's registry+reason row, which needs "professor" and the legacy
// "chat" and "harvester" classified for the same path in one call.
func InspectClaudeServers(path, home string, port int, names ...string) []MCPClientCutover {
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
			// pfm and legacy-pfm each prescribe `pfm install --yes`, so they
			// name only the exact shapes install writes or removes; a near miss
			// stays someone else's.
			var shape map[string]any
			decodeErr := json.Unmarshal(encoded, &shape)
			bin := filepath.Join(home, ".local", "bin", "pfm")
			if report.State == MCPClientPFM && (decodeErr != nil || !isExactStdioShape(shape, bin, mcpStdioArgs)) {
				report.State = MCPClientForeignRegistration
			}
			if report.State == MCPClientLegacyPFM && (decodeErr != nil ||
				!isPFMLegacyClaudeShape(name, shape, bin, port)) {
				report.State = MCPClientForeignRegistration
			}
		}
		reports = append(reports, report)
	}
	return reports
}

// InspectCodexServers classifies every name's table in one Codex config.toml
// `mcp_servers`, one report per name (in the order given). A missing file
// reports every name absent; a read or parse failure reports every name
// unreadable with the error. `professor` is pfm only in the exact table pfm
// writes (`command = "<home>/.local/bin/pfm"`, `args = mcpStdioArgs`, nothing
// else); `chat` and `harvester` go through classifyRegistration. pfm and
// legacy-pfm each prescribe `pfm install --yes`, so a table install's own
// strip keeps (stripPFMCodexLines) is not pfm's to reinstall away: foreign.
func InspectCodexServers(path, home string, port int, names ...string) []MCPClientCutover {
	base := func(name string) MCPClientCutover {
		return MCPClientCutover{
			Client: pfmengine.MustLookup(pfmengine.Codex).LongName,
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
	var document struct {
		Servers map[string]mcpClientRegistration `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(raw), &document); err != nil {
		return uniform(MCPClientUnreadable, fmt.Errorf("parse %s: %w", path, err))
	}
	bin := filepath.Join(home, ".local", "bin", "pfm")
	body, err := codexStdioBody(bin)
	if err != nil {
		return uniform(MCPClientUnreadable, fmt.Errorf("encode pfm's Codex MCP table for %s: %w", path, err))
	}
	var stripped struct {
		Servers map[string]any `toml:"mcp_servers"`
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	_, strippedErr := toml.Decode(strings.Join(stripPFMCodexLines(lines, port, body), "\n"), &stripped)
	reports := make([]MCPClientCutover, 0, len(names))
	for _, name := range names {
		report := base(name)
		registration, present := document.Servers[name]
		if present {
			report.State = classifyRegistration(name, registration, port)
			if name == professorName && registration.Command != bin {
				report.State = MCPClientForeignRegistration
			}
		}
		if report.State == MCPClientPFM || report.State == MCPClientLegacyPFM {
			if strippedErr != nil {
				report.State, report.Error = MCPClientUnreadable, fmt.Errorf(
					"parse %s without pfm's lines: %w", path, strippedErr)
			} else if _, kept := stripped.Servers[name]; kept {
				report.State = MCPClientForeignRegistration
			}
		}
		reports = append(reports, report)
	}
	return reports
}

// classifyRegistration is the one implementation shared by every Claude and
// Codex server inspection. The transport law: every engine registers the
// stdio `professor` (`<bin> mcp serve --stdio`), so `professor` is pfm only in
// that shape and any HTTP `professor` is foreign; `chat` and `harvester` are
// legacy keys, legacy-pfm only in pfm's own old shapes (the loopback
// `/mcp/<key>` URL, bare or with the retired 64-hex bearer, and the stdio
// chat argvs in mcpLegacyChatArgs); a `uv`/`harvest…` harvester is the standalone one.
func classifyRegistration(name string, registration mcpClientRegistration, port int) string {
	typeName := strings.ToLower(strings.TrimSpace(registration.Type))
	command := strings.TrimSpace(registration.Command)
	if command != "" {
		command = filepath.Base(command)
	}
	command = strings.ToLower(command)
	noExtras := len(registration.Headers) == 0 && len(registration.Env) == 0
	stdioArgs := func(want ...string) bool {
		return command == MCPClientPFM && registration.URL == "" && noExtras &&
			(typeName == "" || typeName == stdioProtocol) && slices.Equal(registration.Args, want)
	}
	switch name {
	case professorName:
		if stdioArgs(mcpStdioArgs...) {
			return MCPClientPFM
		}
		return MCPClientForeignRegistration
	case chatName, mcpServerHarvester:
		if registration.URL == legacyMCPURL(port, name) && (typeName == "" || typeName == httpProtocol) &&
			command == "" && len(registration.Args) == 0 && len(registration.Env) == 0 &&
			isRetiredPFMHeaders(registration.Headers) {
			return MCPClientLegacyPFM
		}
		for _, args := range mcpLegacyChatArgs {
			if name == chatName && stdioArgs(args...) {
				return MCPClientLegacyPFM
			}
		}
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

// isRetiredPFMHeaders is true for no headers, or exactly pfm's retired
// `Authorization: Bearer <64 hex>` (isPFMHTTPClient's shape).
func isRetiredPFMHeaders(headers map[string]string) bool {
	if len(headers) == 0 {
		return true
	}
	authorization, ok := headers["Authorization"]
	if !ok || len(headers) != 1 || !strings.HasPrefix(authorization, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	return len(token) == 64 && isHex(token)
}
