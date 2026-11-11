package installer

import (
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
	MCPClientForeignRegistration = "foreign-registration"
	MCPClientUnreadable          = "unreadable"
)

// MCPClientCutover is one consumer's visible route for one server. Unreadable
// is a first-class state so doctor cannot mistake a failed inspection for
// cutover. Name is the server this report classifies ("chat-professor");
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

// InspectOpenCodeServers classifies machine-scope OpenCode MCP registrations.
// Malformed JSONC is unreadable rather than absent.
func InspectOpenCodeServers(path, home string, names ...string) []MCPClientCutover {
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
				report.State = classifyOpenCodeRegistration(name, shape, home)
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

// classifyOpenCodeRegistration names one OpenCode entry's state: a family
// registration is pfm only in the exact local stdio shape install writes;
// anything else is foreign.
func classifyOpenCodeRegistration(name string, registration map[string]any, home string) string {
	if len(registration) != 3 || registration["enabled"] != true || registration[configTypeKey] != openCodeLocal {
		return MCPClientForeignRegistration
	}
	command, _ := registration["command"].([]any)
	if len(command) == 0 {
		return MCPClientForeignRegistration
	}
	argv := make([]string, len(command))
	for index, value := range command {
		text, ok := value.(string)
		if !ok {
			return MCPClientForeignRegistration
		}
		argv[index] = text
	}
	state := classifyRegistration(name, mcpClientRegistration{Type: stdioProtocol, Command: argv[0], Args: argv[1:]})
	if state == MCPClientPFM && argv[0] == mcpFamilyBinary(home) {
		return MCPClientPFM
	}
	return MCPClientForeignRegistration
}

// InspectCodexServers classifies every name's table in one Codex config.toml
// `mcp_servers`, one report per name (in the order given). A missing file
// reports every name absent; a read or parse failure reports every name
// unreadable with the error. A family table is pfm only in the exact table
// install writes (`command = "<home>/.local/bin/pfm"`, its family stdio args,
// nothing else). pfm prescribes `pfm install --yes`, so a table install's own
// strip keeps (stripPFMCodexLines) is not pfm's to reinstall away: foreign.
func InspectCodexServers(path, home string, names ...string) []MCPClientCutover {
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
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	kept, err := stripPFMCodexLines(lines, home)
	if err != nil {
		return uniform(MCPClientUnreadable, fmt.Errorf("strip pfm's Codex MCP table for %s: %w", path, err))
	}
	var stripped struct {
		Servers map[string]any `toml:"mcp_servers"`
	}
	_, strippedErr := toml.Decode(strings.Join(kept, "\n"), &stripped)
	reports := make([]MCPClientCutover, 0, len(names))
	for _, name := range names {
		report := base(name)
		registration, present := document.Servers[name]
		if present {
			report.State = classifyRegistration(name, registration)
			if report.State == MCPClientPFM && registration.Command != mcpFamilyBinary(home) {
				report.State = MCPClientForeignRegistration
			}
		}
		if report.State == MCPClientPFM {
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

// classifyRegistration is shared by Codex and OpenCode family inspection: a
// family registration is pfm only in its stdio `pfm mcp serve --stdio
// --family <family>` shape; anything else is foreign.
func classifyRegistration(name string, registration mcpClientRegistration) string {
	typeName := strings.ToLower(strings.TrimSpace(registration.Type))
	command := strings.TrimSpace(registration.Command)
	if command != "" {
		command = filepath.Base(command)
	}
	command = strings.ToLower(command)
	noExtras := len(registration.Headers) == 0 && len(registration.Env) == 0
	if isFamilyRegistration(name) && command == MCPClientPFM && registration.URL == "" && noExtras &&
		(typeName == "" || typeName == stdioProtocol) && slices.Equal(registration.Args, mcpFamilyStdioArgs(name)) {
		return MCPClientPFM
	}
	return MCPClientForeignRegistration
}
