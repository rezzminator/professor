package seat

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
)

const (
	SeatModel  = "gpt-5.6-luna"
	SeatEffort = "xhigh"

	// MCPVerificationLimitation is deliberately part of the typed result and
	// structured event. RULING-03 requires the gate to say what its instrument
	// cannot see, not merely what passed.
	MCPVerificationLimitation = "apps, plugins, skill search, and workspace dependencies are pinned false but are not observable through codex mcp list"
)

var mcpConfigName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// CommandResult preserves the distinction between a command that ran and
// exited non-zero and a command that could not be invoked at all.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// CommandRunner is the seat's only subprocess boundary outside tmux.
type CommandRunner interface {
	Run(context.Context, string, string, ...string) (CommandResult, error)
}

// ExecCommandRunner invokes the installed Codex binary. A process exit is a
// result (and is checked by the paired MCP gate); an invocation failure is an
// error.
type ExecCommandRunner struct {
	Binary string
}

func (runner ExecCommandRunner) Run(
	ctx context.Context,
	directory string,
	name string,
	arguments ...string,
) (CommandResult, error) {
	if runner.Binary != "" {
		name = runner.Binary
	}
	command := exec.CommandContext(ctx, deps.Executable(name), arguments...)
	command.Dir = directory
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("run %s: %w", name, err)
}

// PinnedConfig is the complete, ordered command-line configuration for both
// seats. Overrides is passed unchanged to the verifier and TUI builder.
type PinnedConfig struct {
	Model     string
	Effort    string
	Servers   []string
	Overrides []string
}

// Verification records both halves of the MCP gate. ConfigLoaded is never
// inferred from an empty roster: only exit zero establishes it.
type Verification struct {
	ConfigLoaded bool
	ExitCode     int
	Tables       int
	Servers      []MCPServer
	Enabled      int
	Disabled     int
	Overrides    []string
	Limitation   string
}

// MCPServer is one parsed row from either codex mcp list table schema.
type MCPServer struct {
	Name   string
	Status string
}

// MCPRoster is a schema-checked enumeration, including the number of table
// headers that proved the command emitted a recognized shape.
type MCPRoster struct {
	Servers []MCPServer
	Tables  int
}

func discoveryOverrides() []string {
	return []string{
		"features.apps=false",
		"features.plugins=false",
		"features.skill_search=false",
		"features.workspace_dependencies=false",
	}
}

func buildPinnedConfig(servers []string, projectRoot string) (PinnedConfig, error) {
	if projectRoot == "" || !filepath.IsAbs(projectRoot) || filepath.Clean(projectRoot) != projectRoot {
		return PinnedConfig{}, fmt.Errorf("seat project root must be a clean absolute path: %q", projectRoot)
	}
	serverNames := append([]string(nil), servers...)
	sort.Strings(serverNames)
	for index, name := range serverNames {
		if !mcpConfigName.MatchString(name) {
			return PinnedConfig{}, fmt.Errorf(
				"MCP server name %q cannot be represented as a safe config key",
				name,
			)
		}
		if index > 0 && name == serverNames[index-1] {
			return PinnedConfig{}, fmt.Errorf("duplicate MCP server %q", name)
		}
	}

	overrides := []string{
		`model="` + SeatModel + `"`,
		`model_reasoning_effort="` + SeatEffort + `"`,
		`approval_policy="never"`,
		`sandbox_mode="workspace-write"`,
		"sandbox_workspace_write.exclude_slash_tmp=true",
		"sandbox_workspace_write.exclude_tmpdir_env_var=true",
		"sandbox_workspace_write.network_access=false",
		"features.apps=false",
		"features.plugins=false",
		"features.hooks=false",
		"features.multi_agent=false",
		"agents.enabled=false",
		"features.goals=false",
		"features.memories=false",
		"features.fast_mode=false",
		"features.network_proxy=false",
		"features.skill_mcp_dependency_install=false",
		"features.skill_search=false",
		"features.workspace_dependencies=false",
		"features.shell_tool=true",
		"features.unified_exec=true",
		`web_search="disabled"`,
		"check_for_update_on_startup=false",
		"projects={" + tomlQuotedKey(projectRoot) + `={trust_level="trusted"}}`,
	}
	for _, name := range serverNames {
		overrides = append(overrides, "mcp_servers."+name+".enabled=false")
	}
	return PinnedConfig{
		Model:     SeatModel,
		Effort:    SeatEffort,
		Servers:   serverNames,
		Overrides: overrides,
	}, nil
}

// DiscoverAndVerifyConfig performs the RULING-03 pair: derive the effective
// configured roster with apps/plugins disabled, build per-server disables,
// then run the same list command with the exact seat override list. Both a
// clean exit and zero exact `enabled` status tokens are required.
func DiscoverAndVerifyConfig(
	ctx context.Context,
	runner CommandRunner,
	projectRoot string,
) (PinnedConfig, Verification, error) {
	if runner == nil {
		return PinnedConfig{}, Verification{}, errors.New("seat config requires a command runner")
	}
	discovery, err := runMCPList(ctx, runner, projectRoot, discoveryOverrides())
	if err != nil {
		return PinnedConfig{}, Verification{}, fmt.Errorf("derive configured MCP roster: %w", err)
	}

	serverNames := make([]string, 0, len(discovery.Servers))
	for _, server := range discovery.Servers {
		serverNames = append(serverNames, server.Name)
	}
	config, err := buildPinnedConfig(serverNames, projectRoot)
	if err != nil {
		return PinnedConfig{}, Verification{}, err
	}

	result, invokeErr := runner.Run(
		ctx,
		projectRoot,
		pfmengine.MustLookup(pfmengine.Codex).Binary,
		mcpListArguments(config.Overrides)...)
	verification := Verification{
		ExitCode:   result.ExitCode,
		Overrides:  append([]string(nil), config.Overrides...),
		Limitation: MCPVerificationLimitation,
	}
	if invokeErr != nil {
		return config, verification, fmt.Errorf("verify pinned MCP configuration: %w", invokeErr)
	}
	if result.ExitCode != 0 {
		return config, verification, fmt.Errorf(
			"verify pinned MCP configuration: codex mcp list exited %d: %s",
			result.ExitCode,
			strings.TrimSpace(result.Stderr),
		)
	}
	verification.ConfigLoaded = true

	roster, err := ParseMCPList(result.Stdout)
	if err != nil {
		return config, verification, fmt.Errorf("verify pinned MCP configuration: %w", err)
	}
	verification.Tables = roster.Tables
	verification.Servers = append([]MCPServer(nil), roster.Servers...)
	for _, server := range roster.Servers {
		switch server.Status {
		case "enabled":
			verification.Enabled++
		case "disabled":
			verification.Disabled++
		default:
			return config, verification, fmt.Errorf(
				"verify pinned MCP configuration: server %q has unknown status %q",
				server.Name,
				server.Status,
			)
		}
	}
	if verification.Enabled != 0 {
		return config, verification, fmt.Errorf(
			"verify pinned MCP configuration: %d server rows are exactly enabled",
			verification.Enabled,
		)
	}
	actualNames := make([]string, 0, len(roster.Servers))
	for _, server := range roster.Servers {
		actualNames = append(actualNames, server.Name)
	}
	sort.Strings(actualNames)
	if strings.Join(actualNames, "\x00") != strings.Join(config.Servers, "\x00") {
		return config, verification, fmt.Errorf(
			"verify pinned MCP configuration: roster changed between discovery and verification (want %q, got %q)",
			config.Servers,
			actualNames,
		)
	}
	return config, verification, nil
}

// tomlQuotedKey returns one TOML basic-string key segment. Seat stages live
// under arbitrary repository paths, so project trust is pinned without
// assuming the path is a TOML bare key.
func tomlQuotedKey(value string) string {
	var encoded strings.Builder
	encoded.WriteByte('"')
	for _, character := range value {
		switch character {
		case '\\':
			encoded.WriteString(`\\`)
		case '"':
			encoded.WriteString(`\"`)
		case '\b':
			encoded.WriteString(`\b`)
		case '\t':
			encoded.WriteString(`\t`)
		case '\n':
			encoded.WriteString(`\n`)
		case '\f':
			encoded.WriteString(`\f`)
		case '\r':
			encoded.WriteString(`\r`)
		default:
			if character < 0x20 || character == 0x7f {
				fmt.Fprintf(&encoded, `\u%04X`, character)
				continue
			}
			encoded.WriteRune(character)
		}
	}
	encoded.WriteByte('"')
	return encoded.String()
}

func runMCPList(
	ctx context.Context,
	runner CommandRunner,
	directory string,
	overrides []string,
) (MCPRoster, error) {
	result, err := runner.Run(
		ctx,
		directory,
		pfmengine.MustLookup(pfmengine.Codex).Binary,
		mcpListArguments(overrides)...)
	if err != nil {
		return MCPRoster{}, err
	}
	if result.ExitCode != 0 {
		return MCPRoster{}, fmt.Errorf(
			"codex mcp list exited %d: %s",
			result.ExitCode,
			strings.TrimSpace(result.Stderr),
		)
	}
	roster, err := ParseMCPList(result.Stdout)
	if err != nil {
		return MCPRoster{}, err
	}
	return roster, nil
}

func mcpListArguments(overrides []string) []string {
	arguments := []string{"mcp", "list"}
	for _, override := range overrides {
		arguments = append(arguments, "-c", override)
	}
	return arguments
}

// ParseMCPList parses both schemas codex-cli 0.147 prints (command servers
// and URL servers). It uses header column offsets because Args contains spaces;
// strings.Fields would silently read the wrong status column.
func ParseMCPList(output string) (MCPRoster, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "No MCP servers configured yet. Try `codex mcp add my-tool -- my-command`." {
		return MCPRoster{}, nil
	}
	if trimmed == "" {
		return MCPRoster{}, errors.New("codex mcp list returned empty output")
	}

	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	var (
		roster      MCPRoster
		nameStart   = -1
		nameEnd     = -1
		statusStart = -1
		statusEnd   = -1
		seen        = make(map[string]struct{})
	)
	for lineNumber, line := range lines {
		if strings.TrimSpace(line) == "" {
			nameStart, nameEnd, statusStart, statusEnd = -1, -1, -1, -1
			continue
		}
		columns := headerColumns(line)
		if len(columns) > 0 {
			var ok bool
			nameStart, nameEnd, ok = columnBounds(columns, "Name")
			if !ok {
				return MCPRoster{}, fmt.Errorf("MCP table header on line %d has no Name column", lineNumber+1)
			}
			statusStart, statusEnd, ok = columnBounds(columns, "Status")
			if !ok {
				return MCPRoster{}, fmt.Errorf("MCP table header on line %d has no Status column", lineNumber+1)
			}
			roster.Tables++
			continue
		}
		if statusStart < 0 {
			return MCPRoster{}, fmt.Errorf("unrecognized codex mcp list schema on line %d: %q", lineNumber+1, line)
		}
		name := sliceColumn(line, nameStart, nameEnd)
		status := sliceColumn(line, statusStart, statusEnd)
		if name == "" || status == "" {
			return MCPRoster{}, fmt.Errorf("incomplete MCP row on line %d", lineNumber+1)
		}
		if status != "enabled" && status != "disabled" {
			return MCPRoster{}, fmt.Errorf(
				"MCP row on line %d has unknown status %q",
				lineNumber+1,
				status,
			)
		}
		if _, duplicate := seen[name]; duplicate {
			return MCPRoster{}, fmt.Errorf("duplicate MCP server row %q", name)
		}
		seen[name] = struct{}{}
		roster.Servers = append(roster.Servers, MCPServer{Name: name, Status: status})
	}
	if roster.Tables == 0 {
		return MCPRoster{}, errors.New("codex mcp list returned no recognized table header")
	}
	sort.Slice(roster.Servers, func(left, right int) bool {
		return roster.Servers[left].Name < roster.Servers[right].Name
	})
	return roster, nil
}

type tableColumn struct {
	name  string
	start int
}

func headerColumns(line string) []tableColumn {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "Name" {
		return nil
	}
	columns := make([]tableColumn, 0, len(fields))
	searchFrom := 0
	for _, field := range fields {
		index := strings.Index(line[searchFrom:], field)
		if index < 0 {
			return nil
		}
		start := searchFrom + index
		columns = append(columns, tableColumn{name: field, start: start})
		searchFrom = start + len(field)
	}
	return columns
}

func columnBounds(columns []tableColumn, name string) (int, int, bool) {
	for index, column := range columns {
		if column.name != name {
			continue
		}
		end := -1
		if index+1 < len(columns) {
			end = columns[index+1].start
		}
		return column.start, end, true
	}
	return 0, 0, false
}

func sliceColumn(line string, start, end int) string {
	if start < 0 || start >= len(line) {
		return ""
	}
	if end < 0 || end > len(line) {
		end = len(line)
	}
	if end < start {
		return ""
	}
	return strings.TrimSpace(line[start:end])
}
