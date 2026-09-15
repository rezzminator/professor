package seat

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var (
	discoveryMCPOutput = mcpOutput("enabled")
	verifiedMCPOutput  = mcpOutput("disabled")
)

func mcpOutput(status string) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%-22s%-10s%-34s%-10s%s\n", "Name", "Command", "Args", "Status", "Auth")
	fmt.Fprintf(
		&output,
		"%-22s%-10s%-34s%-10s%s\n",
		"chrome-devtools",
		"npx",
		"-y package --browser url",
		status,
		"Unsupported",
	)
	fmt.Fprintf(&output, "%-22s%-10s%-34s%-10s%s\n", "harvester", "uv", "--directory repo run", status, "Unsupported")
	output.WriteByte('\n')
	fmt.Fprintf(&output, "%-22s%-34s%-10s%s\n", "Name", "Url", "Status", "Auth")
	fmt.Fprintf(&output, "%-22s%-34s%-10s%s\n", "atlassiancf", "https://example.invalid/a", status, "OAuth")
	fmt.Fprintf(&output, "%-22s%-34s%-10s%s\n", "notion", "https://example.invalid/n", status, "OAuth")
	fmt.Fprintf(
		&output,
		"%-22s%-34s%-10s%s\n",
		"openaiDeveloperDocs",
		"https://example.invalid/o",
		status,
		"Unsupported",
	)
	return output.String()
}

type commandCall struct {
	directory string
	name      string
	args      []string
}

type scriptedCommands struct {
	results []CommandResult
	errors  []error
	calls   []commandCall
}

func (commands *scriptedCommands) Run(
	_ context.Context,
	directory string,
	name string,
	args ...string,
) (CommandResult, error) {
	commands.calls = append(commands.calls, commandCall{
		directory: directory,
		name:      name,
		args:      append([]string(nil), args...),
	})
	index := len(commands.calls) - 1
	if index >= len(commands.results) {
		return CommandResult{}, context.Canceled
	}
	var err error
	if index < len(commands.errors) {
		err = commands.errors[index]
	}
	return commands.results[index], err
}

func TestParseMCPListUsesStatusColumnAcrossBothSchemas(t *testing.T) {
	roster, err := ParseMCPList(verifiedMCPOutput)
	if err != nil {
		t.Fatalf("ParseMCPList() error = %v", err)
	}
	if roster.Tables != 2 {
		t.Fatalf("Tables = %d, want 2", roster.Tables)
	}
	want := []MCPServer{
		{Name: "atlassiancf", Status: "disabled"},
		{Name: "chrome-devtools", Status: "disabled"},
		{Name: "harvester", Status: "disabled"},
		{Name: "notion", Status: "disabled"},
		{Name: "openaiDeveloperDocs", Status: "disabled"},
	}
	if !reflect.DeepEqual(roster.Servers, want) {
		t.Fatalf("Servers = %#v, want %#v", roster.Servers, want)
	}
}

func TestParseMCPListRefusesAmbiguousOrBrokenShapes(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
	}{
		{name: "empty", output: ""},
		{name: "prose absence", output: "No MCP tools available."},
		{name: "header missing status", output: "Name  Command\none   tool\n"},
		{name: "unknown row status", output: "Name  Status  Auth\none   maybe   none\n"},
		{name: "duplicate across tables", output: "Name  Status  Auth\none   disabled x\n\nName  Status  Auth\none   disabled x\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseMCPList(test.output); err == nil {
				t.Fatal("ParseMCPList() accepted an output that cannot prove the roster")
			}
		})
	}
}

func TestParseMCPListAcceptsExplicitConfiguredEmptySentinel(t *testing.T) {
	roster, err := ParseMCPList("No MCP servers configured yet. Try `codex mcp add my-tool -- my-command`.\n")
	if err != nil {
		t.Fatalf("ParseMCPList() error = %v", err)
	}
	if len(roster.Servers) != 0 || roster.Tables != 0 {
		t.Fatalf("roster = %#v, want proven empty", roster)
	}
}

func TestDiscoverAndVerifyConfigDerivesRosterAndUsesExactPinSet(t *testing.T) {
	commands := &scriptedCommands{results: []CommandResult{
		{Stdout: discoveryMCPOutput, ExitCode: 0},
		{Stdout: verifiedMCPOutput, ExitCode: 0},
	}}
	config, verification, err := DiscoverAndVerifyConfig(context.Background(), commands, "/repo")
	if err != nil {
		t.Fatalf("DiscoverAndVerifyConfig() error = %v", err)
	}
	wantServers := []string{
		"atlassiancf",
		"chrome-devtools",
		"harvester",
		"notion",
		"openaiDeveloperDocs",
	}
	if !reflect.DeepEqual(config.Servers, wantServers) {
		t.Fatalf("Servers = %q, want %q", config.Servers, wantServers)
	}
	for _, required := range []string{
		`model="gpt-5.6-luna"`,
		`model_reasoning_effort="xhigh"`,
		`approval_policy="never"`,
		`sandbox_mode="workspace-write"`,
		"sandbox_workspace_write.exclude_slash_tmp=true",
		"sandbox_workspace_write.exclude_tmpdir_env_var=true",
		"sandbox_workspace_write.network_access=false",
		"features.apps=false",
		"features.plugins=false",
		"features.hooks=false",
		"features.multi_agent=false",
		"features.skill_search=false",
		"features.workspace_dependencies=false",
		"agents.enabled=false",
		`web_search="disabled"`,
		`projects={"/repo"={trust_level="trusted"}}`,
		"mcp_servers.chrome-devtools.enabled=false",
		"mcp_servers.openaiDeveloperDocs.enabled=false",
	} {
		if !contains(config.Overrides, required) {
			t.Errorf("pin set lacks %q: %q", required, config.Overrides)
		}
	}
	if len(commands.calls) != 2 {
		t.Fatalf("command calls = %d, want discovery + verification", len(commands.calls))
	}
	if got := commands.calls[0].args; !reflect.DeepEqual(got, mcpListArguments(discoveryOverrides())) {
		t.Fatalf("discovery args = %q, want feature-disabled enumeration", got)
	}
	if got := commands.calls[0].directory; got != "/repo" {
		t.Fatalf("discovery directory = %q, want target repository", got)
	}
	if got := commands.calls[1].args; !reflect.DeepEqual(got, mcpListArguments(config.Overrides)) {
		t.Fatalf(
			"verification args differ from exact seat pin set:\n got %q\nwant %q",
			got,
			mcpListArguments(config.Overrides),
		)
	}
	if got := commands.calls[1].directory; got != "/repo" {
		t.Fatalf("verification directory = %q, want same target repository as discovery", got)
	}
	if !verification.ConfigLoaded || verification.ExitCode != 0 ||
		verification.Enabled != 0 || verification.Disabled != 5 {
		t.Fatalf("verification = %#v", verification)
	}
	if verification.Limitation != MCPVerificationLimitation ||
		!strings.Contains(verification.Limitation, "not observable") {
		t.Fatalf("verification limitation = %q", verification.Limitation)
	}
}

func TestVerificationRequiresExitZeroAsWellAsNoEnabledRows(t *testing.T) {
	commands := &scriptedCommands{results: []CommandResult{
		{Stdout: discoveryMCPOutput, ExitCode: 0},
		{
			Stdout:   "No MCP servers configured yet. Try `codex mcp add my-tool -- my-command`.\n",
			Stderr:   "failed to load bootstrap configuration",
			ExitCode: 1,
		},
	}}
	_, verification, err := DiscoverAndVerifyConfig(context.Background(), commands, "/repo")
	if err == nil {
		t.Fatal("broken config with zero enabled rows false-passed")
	}
	if verification.ConfigLoaded {
		t.Fatal("non-zero exit was reported as a loaded config")
	}
	if verification.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", verification.ExitCode)
	}
}

func TestDisabledDoesNotCountAsEnabledButExactEnabledFails(t *testing.T) {
	disabled := &scriptedCommands{results: []CommandResult{
		{Stdout: discoveryMCPOutput, ExitCode: 0},
		{Stdout: verifiedMCPOutput, ExitCode: 0},
	}}
	if _, verification, err := DiscoverAndVerifyConfig(context.Background(), disabled, "/repo"); err != nil {
		t.Fatalf("all exact disabled statuses failed: %v", err)
	} else if verification.Enabled != 0 {
		t.Fatalf("disabled substring counted as enabled: %#v", verification)
	}

	enabled := &scriptedCommands{results: []CommandResult{
		{Stdout: discoveryMCPOutput, ExitCode: 0},
		{Stdout: strings.Replace(verifiedMCPOutput, "disabled", "enabled ", 1), ExitCode: 0},
	}}
	if _, verification, err := DiscoverAndVerifyConfig(context.Background(), enabled, "/repo"); err == nil {
		t.Fatal("one exact enabled status passed verification")
	} else if verification.Enabled != 1 {
		t.Fatalf("Enabled = %d, want 1", verification.Enabled)
	}
}

func TestConfiguredProjectContext7IsPinnedFromTheDerivedRoster(t *testing.T) {
	config, err := buildPinnedConfig([]string{"context7"}, "/repo")
	if err != nil {
		t.Fatalf("buildPinnedConfig() rejected a derived project server: %v", err)
	}
	if !contains(config.Overrides, "mcp_servers.context7.enabled=false") {
		t.Fatalf("pin set did not disable derived project context7: %q", config.Overrides)
	}
}

func TestProjectTrustOverrideQuotesArbitraryAbsolutePath(t *testing.T) {
	config, err := buildPinnedConfig(nil, "/repo/quote\"and\\slash")
	if err != nil {
		t.Fatal(err)
	}
	want := `projects={"/repo/quote\"and\\slash"={trust_level="trusted"}}`
	if !contains(config.Overrides, want) {
		t.Fatalf("project trust pin = %q, want %q", config.Overrides, want)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
