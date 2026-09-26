package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

const claudeRemediation = " remediation=run pfm install --yes (registers every registry a pfm-launched Claude reads)"

// professorStdio is the one registration every engine writes for Claude:
// the absolute pfm binary run as `pfm mcp serve --stdio`.
func professorStdio(home string) string {
	return `{"type":"stdio","command":"` + filepath.Join(home, ".local", "bin", "pfm") +
		`","args":["mcp","serve","--stdio"]}`
}

// TestDoctorMCPClientRowNamesEachRegistryAndItsReason pins issue #24 finding
// 5's doctor half: on a host whose shell exports CLAUDE_CONFIG_DIR, the
// implicit account's ~/.claude.json is not the only file a pfm-launched
// `claude` can read — doctor must name BOTH registries with why each is one,
// and warn on the ambient one pfm never reached while the implicit one is
// healthy.
func TestDoctorMCPClientRowNamesEachRegistryAndItsReason(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	ambient := filepath.Join(home, ".cc", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", ambient)

	registration := `{"mcpServers":{"professor":` + professorStdio(home) + `}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(registration), 0o600); err != nil {
		t.Fatal(err)
	}
	// The ambient CLAUDE_CONFIG_DIR's .claude.json deliberately does not exist.

	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Config.Accounts = []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude"), Implicit: true}}
	runtime.Config.MCP.HTTP.Port = 18377

	var stdout, stderr bytes.Buffer
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 {
		t.Fatalf(
			"doctor code=%d stdout=%q stderr=%q, want a warning for the absent ambient registry",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	out := stdout.String()
	implicitRow := "doctor: mcp client=claude registry=" + filepath.Join(home, ".claude.json") +
		" (account 1 (pfm spawns it without CLAUDE_CONFIG_DIR)) professor=pfm\n"
	if !strings.Contains(out, implicitRow) {
		t.Fatalf("doctor output missing the healthy implicit-account row %q:\n%s", implicitRow, out)
	}
	ambientRow := "doctor: mcp client=claude registry=" + filepath.Join(ambient, ".claude.json") +
		" (ambient CLAUDE_CONFIG_DIR=" + ambient + " (the claude launcher passes it through — internal_launch.go)) professor=absent" +
		claudeRemediation + "\n"
	if !strings.Contains(out, ambientRow) {
		t.Fatalf("doctor output missing the absent ambient-registry warning row %q:\n%s", ambientRow, out)
	}
}

// TestDoctorMCPClientClaudeRowClassifiesProfessorAndLegacyEntries pins the
// Claude row's states: pfm's legacy `chat` / `harvester` entries are named
// sorted with the reinstall remedy, an HTTP or foreign `professor` is a
// foreign registration, and an unreadable registry names its error.
func TestDoctorMCPClientClaudeRowClassifiesProfessorAndLegacyEntries(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	registry := filepath.Join(home, ".claude.json")
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Config.Accounts = []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude"), Implicit: true}}
	runtime.Config.MCP.HTTP.Port = 18377
	row := "doctor: mcp client=claude registry=" + registry +
		" (account 1 (pfm spawns it without CLAUDE_CONFIG_DIR)) professor="
	bin := filepath.Join(home, ".local", "bin", "pfm")
	for _, testCase := range []struct {
		name, servers, want string
	}{
		{
			"legacy entries remain",
			`"professor":` + professorStdio(home) +
				`,"chat":{"type":"stdio","command":"` + bin + `","args":["mcp","chat","serve"]}` +
				`,"harvester":{"type":"http","url":"http://127.0.0.1:18377/mcp/harvester"}`,
			row + "pfm legacy=chat,harvester" + claudeRemediation + "\n",
		},
		{
			"empty env is shape-neutral",
			`"professor":{"type":"stdio","command":"` + bin + `","args":["mcp","serve","--stdio"],"env":{}}` +
				`,"chat":{"type":"stdio","command":"` + bin + `","args":["mcp","chat","serve"],"env":null}`,
			row + "pfm legacy=chat" + claudeRemediation + "\n",
		},
		{
			"non-empty env professor",
			`"professor":{"type":"stdio","command":"` + bin + `","args":["mcp","serve","--stdio"],"env":{"DEBUG":"1"}}`,
			row + "foreign-registration" + claudeRemediation + "\n",
		},
		{
			"http professor",
			`"professor":{"type":"http","url":"http://127.0.0.1:18377/mcp/professor"}`,
			row + "foreign-registration" + claudeRemediation + "\n",
		},
		{
			"foreign professor",
			`"professor":{"type":"stdio","command":"manual","args":["serve"]}`,
			row + "foreign-registration" + claudeRemediation + "\n",
		},
		{
			"bare pfm professor",
			`"professor":{"type":"stdio","command":"pfm","args":["mcp","serve","--stdio"]}`,
			row + "foreign-registration" + claudeRemediation + "\n",
		},
		{
			"type-less professor",
			`"professor":{"command":"` + bin + `","args":["mcp","serve","--stdio"]}`,
			row + "foreign-registration" + claudeRemediation + "\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := os.WriteFile(registry, []byte(`{"mcpServers":{`+testCase.servers+`}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 ||
				!strings.Contains(stdout.String(), testCase.want) {
				t.Fatalf("doctor code=%d stdout=%q, want a warning row %q", code, stdout.String(), testCase.want)
			}
		})
	}
	t.Run("unreadable registry", func(t *testing.T) {
		if err := os.WriteFile(registry, []byte(`{"mcpServers":`), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 ||
			!strings.Contains(stdout.String(), row+"unreadable error=parse "+registry) {
			t.Fatalf("doctor code=%d stdout=%q, want the unreadable row naming its error", code, stdout.String())
		}
	})
}

func TestDoctorMCPClientReportsOpenCodeStates(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	path := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Paths.Home = home
	runtime.Config.MCP.HTTP.Port = 18377
	bin := filepath.Join(home, ".local", "bin", "pfm")
	professor := `"professor":{"type":"local","command":["` + bin + `","mcp","serve","--stdio"],"enabled":true}`
	row := "doctor: mcp client=opencode config=" + path + " professor="
	write(`{"mcp":{` + professor + `}}`)
	var stdout, stderr bytes.Buffer
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("healthy OpenCode doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), row+"pfm\n") {
		t.Fatalf("doctor omitted healthy OpenCode row:\n%s", stdout.String())
	}
	write(`{"mcp":{` + professor +
		`,"chat":{"type":"local","command":["` + bin + `","mcp","chat","serve"],"enabled":true}` +
		`,"harvester":{"type":"remote","url":"http://127.0.0.1:18377/mcp/harvester","enabled":true}}}`)
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 ||
		!strings.Contains(stdout.String(), row+"pfm legacy=chat,harvester remediation=run pfm install --yes\n") {
		t.Fatalf(
			"legacy OpenCode doctor code=%d stdout=%q, want the legacy keys and the reinstall",
			code,
			stdout.String(),
		)
	}
	write(`{"mcp":{}}`)
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 ||
		!strings.Contains(stdout.String(), row+"absent\n") {
		t.Fatalf("absent OpenCode doctor code=%d stdout=%q, want clean absence", code, stdout.String())
	}
	write(`{`)
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 ||
		!strings.Contains(stdout.String(), row+"unreadable error=") {
		t.Fatalf("unreadable OpenCode doctor code=%d stdout=%q, want error warning", code, stdout.String())
	}
}

// TestDoctorOpenCodeNamesAUserOwnedEntry is a REGRESSION test for the host
// whose ~/.config/opencode/opencode.jsonc already holds a hand-written entry
// under pfm's key: telling the operator to run `pfm install --yes` would be
// advice that can never fix it, because install preserves an entry its
// ownership ledger does not claim. That entry is named as user-owned, with
// the file and the key; a pfm-written entry gone stale keeps the plain
// reinstall remediation.
func TestDoctorOpenCodeNamesAUserOwnedEntry(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	path := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Paths.Home = home
	runtime.Config.MCP.HTTP.Port = 18377
	foreign := `{"mcp":{"professor":{"type":"local","command":["uv","--directory","/srv/professor","run","professor"]}}}`
	if err := os.WriteFile(path, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	out := stdout.String()
	if code != 1 {
		t.Fatalf("user-owned OpenCode doctor code=%d stdout=%q stderr=%q, want a warning", code, out, stderr.String())
	}
	for _, want := range []string{
		"config=" + path + " professor=foreign-registration",
		"remediation=professor in " + path + " is a user-owned entry pfm install will not replace",
		"remove or rename it, then run pfm install --yes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor OpenCode row missing %q:\n%s", want, out)
		}
	}

	// A pfm-written entry whose binary path went stale is install's to
	// rewrite: the ledger claims it, so the remediation stays the plain
	// reinstall.
	stale := `{"mcp":{"professor":{"type":"local","command":["/old/pfm","mcp","serve","--stdio"],"enabled":true}}}`
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(home, ".local", "share", "pfm", "install", "mcp-ownership.json")
	if err := os.MkdirAll(filepath.Dir(ledger), 0o700); err != nil {
		t.Fatal(err)
	}
	owned := `{"opencodeRegistrations":{"` + path + `":{"professor":` +
		`{"type":"local","command":["/old/pfm","mcp","serve","--stdio"],"enabled":true}}}}`
	if err := os.WriteFile(ledger, []byte(owned), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 ||
		!strings.Contains(stdout.String(), "remediation=run pfm install --yes") ||
		strings.Contains(stdout.String(), "user-owned") {
		t.Fatalf("stale pfm-owned OpenCode doctor code=%d stdout=%q, want the plain reinstall remediation",
			code, stdout.String())
	}

	// An unreadable ledger reports the failure to look; it never renders as
	// "no user-owned entry here".
	if err := os.WriteFile(ledger, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 ||
		!strings.Contains(stdout.String(), "ownership=unreadable error=") {
		t.Fatalf("unreadable ledger OpenCode doctor code=%d stdout=%q, want the ownership error named",
			code, stdout.String())
	}
}

// TestDoctorMCPClientReportsCodexProfessorStates pins the Codex row: every
// Codex account home's config.toml is checked for pfm's stdio professor the
// way Claude and OpenCode are — healthy, absent while a family is enabled
// (warn) or disabled (quiet), a hand-written table named user-owned, pfm's
// legacy chat table, and an unparseable file named as an error, never absent.
func TestDoctorMCPClientReportsCodexProfessorStates(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local", "bin", "pfm")
	professor := "[mcp_servers.professor]\ncommand = \"" + bin + "\"\nargs = [\"mcp\", \"serve\", \"--stdio\"]\n"
	fenced := "# BEGIN pfm mcp_servers — installer-owned\n" + professor +
		"# END pfm mcp_servers — installer-owned\n"
	row := "doctor: mcp client=codex config=" + path + " professor="
	reinstall := " remediation=run pfm install --yes"
	for _, fixture := range []struct {
		name, config string
		enabled      bool
		wantRow      string
		warnings     int
	}{
		{"present", fenced, true, row + "pfm\n", 0},
		{"absent while enabled", "", true, row + "absent" + reinstall + "\n", 1},
		{"absent while disabled", "", false, row + "absent\n", 0},
		{
			"foreign", "[mcp_servers.professor]\ncommand = \"/opt/professor/bin/serve\"\n", true,
			row + "foreign-registration remediation=professor in " + path +
				" is a user-owned entry pfm install will not replace — remove or rename it, then run pfm install --yes\n",
			1,
		},
		{
			"legacy chat", fenced + "\n[mcp_servers.chat]\nurl = \"http://127.0.0.1:18377/mcp/chat\"\n", true,
			row + "pfm legacy=chat" + reinstall + "\n", 1,
		},
		// The same unparseable file also fails the harvester row, which
		// names that key; both rows report the error, never absence.
		{"unreadable", "[mcp_servers.professor\n", true, row + "unreadable error=", 2},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(fixture.config), 0o600); err != nil {
				t.Fatal(err)
			}
			runtime, err := pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			runtime.Paths.Home = home
			runtime.Config.MCP.HTTP.Port = 18377
			runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Dir(path)}}
			runtime.Config.MCPServers = map[string]pfmconfig.MCPServer{
				pfmconfig.MCPServerChat: {Enabled: fixture.enabled},
			}
			var stdout bytes.Buffer
			warnings := PrintMCPClientCutover(&stdout, runtime)
			out := stdout.String()
			if warnings != fixture.warnings || !strings.Contains(out, fixture.wantRow) {
				t.Fatalf("warnings=%d output:\n%s\nwant %d warnings and the row %q", warnings, out, fixture.warnings,
					fixture.wantRow)
			}
			if strings.Count(out, "client=codex config=") != 1 {
				t.Fatalf("want exactly one Codex professor row:\n%s", out)
			}
			if complete := strings.Contains(out, "client-cutover=complete"); complete != (fixture.warnings == 0) {
				t.Fatalf("client-cutover=complete printed=%v with %d warnings:\n%s", complete, fixture.warnings, out)
			}
		})
	}
}
