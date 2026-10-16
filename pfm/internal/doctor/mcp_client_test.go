package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestDoctorMCPClientChecksSupportedClientsWithUnreadableClaudeRegistry(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := pfmconfig.Runtime{}
	runtime.Paths.Home = home
	runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{}
	var output bytes.Buffer
	if warnings := PrintMCPClientCutover(
		&output,
		runtime,
	); warnings != 1 ||
		!strings.Contains(
			output.String(),
			"doctor: mcp client=claude config="+filepath.Join(home, ".claude.json")+" state=unreadable",
		) {
		t.Fatalf("warnings=%d output=%s", warnings, output.String())
	}
}

func TestDoctorMCPClientPreservesUnrelatedRegistrations(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	for _, tc := range []struct {
		client, name, relative, content string
		warnings                        int
	}{
		{"claude", "chat", ".claude.json", `{"mcpServers":{"chat":{"command":"other-server"}}}`, 0},
		{"claude", "harvester", ".claude.json", `{"mcpServers":{"harvester":{"command":"other-server"}}}`, 0},
		{"claude", "professor", ".claude.json", `{"mcpServers":{"professor":{"command":"other-server"}}}`, 0},
		{"codex", "professor", ".codex/config.toml", "[mcp_servers.professor]\ncommand = \"other-server\"\n", 0},
		{"opencode", "professor", ".config/opencode/opencode.jsonc", `{"mcp":{"professor":{"type":"local","command":["other-server"]}}}`, 0},
		{"claude", "chat-professor", ".claude.json", `{"mcpServers":{"chat-professor":{"command":"other-server"}}}`, 1},
	} {
		t.Run(tc.client+"/"+tc.name, func(t *testing.T) {
			path := filepath.Join(home, tc.relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			})
			runtime, err := pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			runtime.Paths.Home = home
			runtime.Config.MCPServers = map[string]pfmconfig.MCPServer{}
			runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}}
			var output bytes.Buffer
			warnings := PrintMCPClientCutover(&output, runtime)
			label := "info="
			if tc.warnings != 0 {
				label = "warning="
			}
			want := "client=" + tc.client + " config=" + path + " " + tc.name +
				"=foreign-registration " + label + "user-owned entry preserved"
			if warnings != tc.warnings || !strings.Contains(output.String(), want) {
				t.Fatalf("warnings=%d output=%s; want %d warnings and %q", warnings, output.String(), tc.warnings, want)
			}
		})
	}
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
	professor := `"chat-professor":{"type":"local","command":["` + bin + `","mcp","serve","--stdio","--family","chat"],"enabled":true}`
	row := "doctor: mcp client=opencode config=" + path + " chat-professor="
	runtime.Config.MCPServers[pfmconfig.MCPServerChat] = pfmconfig.MCPServer{Enabled: true}
	write(`{"mcp":{` + professor + `}}`)
	var stdout, stderr bytes.Buffer
	if code := PrintMCPClientCutover(&stdout, runtime); code != 0 {
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
	runtime.Config.MCPServers[pfmconfig.MCPServerChat] = pfmconfig.MCPServer{}
	write(`{"mcp":{}}`)
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 ||
		!strings.Contains(stdout.String(), row+"absent\n") {
		t.Fatalf("absent OpenCode doctor code=%d stdout=%q, want clean absence", code, stdout.String())
	}
	runtime.Config.MCPServers[pfmconfig.MCPServerChat] = pfmconfig.MCPServer{Enabled: true}
	stdout.Reset()
	if warnings := PrintMCPClientCutover(
		&stdout,
		runtime,
	); warnings != 1 ||
		!strings.Contains(stdout.String(), row+"absent remediation=run pfm install --yes") {
		t.Fatalf("enabled absent OpenCode family warnings=%d output=%s", warnings, stdout.String())
	}
	runtime.Config.MCPServers[pfmconfig.MCPServerChat] = pfmconfig.MCPServer{}
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
	foreign := `{"mcp":{"chat-professor":{"type":"local","command":["uv","--directory","/srv/professor","run","chat-professor"]}}}`
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
		"config=" + path + " chat-professor=foreign-registration",
		"remediation=chat-professor in " + path + " is a user-owned entry pfm install will not replace",
		"remove or rename it, then run pfm install --yes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor OpenCode row missing %q:\n%s", want, out)
		}
	}

	// A pfm-written entry whose binary path went stale is install's to
	// rewrite: the ledger claims it, so the remediation stays the plain
	// reinstall.
	stale := `{"mcp":{"chat-professor":{"type":"local","command":["/old/pfm","mcp","serve","--stdio","--family","chat"],"enabled":true}}}`
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(home, ".local", "share", "pfm", "install", "mcp-ownership.json")
	if err := os.MkdirAll(filepath.Dir(ledger), 0o700); err != nil {
		t.Fatal(err)
	}
	owned := `{"opencodeRegistrations":{"` + path + `":{"chat-professor":` +
		`{"type":"local","command":["/old/pfm","mcp","serve","--stdio","--family","chat"],"enabled":true}}}}`
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
	// "no user-owned entry here". The layout rows tally it as a failure
	// (0-layout.md § Doctor lines), so doctor exits 3.
	if err := os.WriteFile(ledger, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 3 ||
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
	professor := "[mcp_servers.chat-professor]\ncommand = \"" + bin + "\"\nargs = [\"mcp\", \"serve\", \"--stdio\", \"--family\", \"chat\"]\n"
	fenced := "# BEGIN pfm mcp_servers — installer-owned\n" + professor +
		"# END pfm mcp_servers — installer-owned\n"
	row := "doctor: mcp client=codex config=" + path + " chat-professor="
	reinstall := " remediation=run pfm install --yes"
	for _, fixture := range []struct {
		name, config string
		enabled      bool
		wantRow      string
		warnings     int
	}{
		{"present", fenced, true, row + "pfm\n", 0},
		{"present while disabled", fenced, false, row + "pfm" + reinstall + "\n", 1},
		{"absent while enabled", "", true, row + "absent" + reinstall + "\n", 1},
		{"absent while disabled", "", false, row + "absent\n", 0},
		{
			"foreign", "[mcp_servers.chat-professor]\ncommand = \"/opt/professor/bin/serve\"\n", true,
			row + "foreign-registration remediation=chat-professor in " + path +
				" is a user-owned entry pfm install will not replace — remove or rename it, then run pfm install --yes\n",
			1,
		},
		{
			"legacy chat", fenced + "\n[mcp_servers.chat]\nurl = \"http://127.0.0.1:18377/mcp/chat\"\n", true,
			row + "pfm legacy=chat" + reinstall + "\n", 1,
		},
		// The same unparseable file also fails the harvester row, which
		// names that key; both rows report the error, never absence.
		{"unreadable", "[mcp_servers.chat-professor\n", true, row + "unreadable error=", 3},
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
			if fixture.enabled {
				opencode := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
				if err := os.MkdirAll(filepath.Dir(opencode), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					opencode,
					[]byte(
						`{"mcp":{"chat-professor":{"type":"local","command":["`+bin+`","mcp","serve","--stdio","--family","chat"],"enabled":true}}}`,
					),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(
					filepath.Join(home, ".config", "opencode", "opencode.jsonc"),
				); err != nil &&
					!os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			var stdout bytes.Buffer
			warnings := PrintMCPClientCutover(&stdout, runtime)
			out := stdout.String()
			if warnings != fixture.warnings || !strings.Contains(out, fixture.wantRow) {
				t.Fatalf("warnings=%d output:\n%s\nwant %d warnings and the row %q", warnings, out, fixture.warnings,
					fixture.wantRow)
			}
			if strings.Count(out, "client=codex config=") != 2 {
				t.Fatalf("want exactly one Codex professor row:\n%s", out)
			}
			if complete := strings.Contains(out, "client-cutover=complete"); complete != (fixture.warnings == 0) {
				t.Fatalf("client-cutover=complete printed=%v with %d warnings:\n%s", complete, fixture.warnings, out)
			}
		})
	}
}
