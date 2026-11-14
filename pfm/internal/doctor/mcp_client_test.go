package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

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

func TestDoctorMCPClientReportsFamilyOwnership(t *testing.T) {
	for _, family := range []string{pfmconfig.MCPServerChat} {
		for _, tc := range []struct {
			name, ledger, want string
			enabled            bool
			warnings           int
		}{
			{name: "enabled", enabled: true, want: "doctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=pfm\n"},
			{
				name: "disabled owned", ledger: "opencodeRegistrations", warnings: 1,
				want: "doctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=pfm remediation=run pfm install --yes\n",
			},
			{
				name: "disabled pending", ledger: "opencodePending", warnings: 1,
				want: "doctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=pfm remediation=run pfm install --yes\n",
			},
			{name: "disabled unowned", warnings: 1, want: "doctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=pfm remediation=chat-professor in {root}/.config/opencode/opencode.jsonc is a user-owned entry pfm install will not replace — remove or rename it, then run pfm install --yes\n"},
			{
				name: "disabled unreadable ownership", ledger: "unreadable", warnings: 1,
				want: "doctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=pfm ownership=unreadable error=decode MCP ownership {root}/.local/share/pfm/install/mcp-ownership.json: unexpected end of JSON input remediation=run pfm install --yes\n",
			},
		} {
			t.Run(family+"/"+tc.name, func(t *testing.T) {
				home := filepath.Join(jailTest(t), "home")
				name := pfmconfig.MCPRegistrationName(family)
				bin := filepath.Join(home, ".local", "bin", "pfm")
				args := []string{"mcp", "serve", "--stdio", "--family", family}
				entry := map[string]any{"type": "local", "command": append([]string{bin}, args...), "enabled": true}
				path := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
				document := map[string]any{"mcp": map[string]any{name: entry}}
				writeJSON := func(path string, value any) {
					t.Helper()
					raw, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, raw, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				writeJSON(path, document)
				if tc.ledger != "" {
					ledger := filepath.Join(home, ".local", "share", "pfm", "install", "mcp-ownership.json")
					writeJSON(ledger, map[string]any{tc.ledger: map[string]any{path: map[string]any{name: entry}}})
					if tc.ledger == "unreadable" {
						if err := os.WriteFile(ledger, []byte("{"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				runtime := pfmconfig.Runtime{}
				runtime.Paths.Home = home
				runtime.Config.MCPServers = map[string]pfmconfig.MCPServer{family: {Enabled: tc.enabled}}
				runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{}
				var output bytes.Buffer
				warnings := printOpenCodeRows(&output, runtime)
				if got := rooted(output.String(), home); warnings != tc.warnings || got != tc.want {
					t.Errorf("warnings=%d output = %q, want %q", warnings, got, tc.want)
				}
			})
		}
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

	// An unreadable ledger reports the failure to look, a warning; it never
	// renders as "no user-owned entry here".
	if err := os.WriteFile(ledger, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	row := "doctor: mcp client=opencode config=" + path + " chat-professor=foreign-registration ownership=unreadable " +
		"error=decode MCP ownership " + ledger + ": unexpected end of JSON input remediation=run pfm install --yes\n"
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 || !strings.Contains(stdout.String(), row) {
		t.Fatalf("unreadable ledger %s: OpenCode doctor code=%d stdout=%q, want code 1 and the row %q",
			ledger, code, stdout.String(), row)
	}
}

// TestDoctorMCPClientReportsCodexProfessorStates pins the Codex row: every
// Codex account home's config.toml is checked for pfm's stdio chat-professor
// the way OpenCode is — healthy, absent while a family is enabled (warn) or
// disabled (quiet), a hand-written table named user-owned, and an unparseable
// file named as an error, never absent.
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
	for _, fixture := range []struct {
		name, config string
		enabled      bool
		want         string
		warnings     int
	}{
		{"present", fenced, true, "doctor: mcp client=codex config={root}/.codex/config.toml chat-professor=pfm\ndoctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=pfm\ndoctor: mcp client-cutover=complete\n", 0},
		{"present while disabled", fenced, false, "doctor: mcp client=codex config={root}/.codex/config.toml chat-professor=pfm remediation=run pfm install --yes\ndoctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=absent\n", 1},
		{"absent while enabled", "", true, "doctor: mcp client=codex config={root}/.codex/config.toml chat-professor=absent remediation=run pfm install --yes\ndoctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=pfm\n", 1},
		{"absent while disabled", "", false, "doctor: mcp client=codex config={root}/.codex/config.toml chat-professor=absent\ndoctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=absent\ndoctor: mcp client-cutover=complete\n", 0},
		{"foreign", "[mcp_servers.chat-professor]\ncommand = \"/opt/professor/bin/serve\"\n", true, "doctor: mcp client=codex config={root}/.codex/config.toml chat-professor=foreign-registration remediation=chat-professor in {root}/.codex/config.toml is a user-owned entry pfm install will not replace — remove or rename it, then run pfm install --yes\ndoctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=pfm\n", 1},
		// An unparseable file must report an error, never absence.
		{"unreadable", "[mcp_servers.chat-professor\n", true, "doctor: mcp client=codex config={root}/.codex/config.toml chat-professor=unreadable error=parse {root}/.codex/config.toml: toml: line 2: expected '.' or ']' to end table name, but got '\\n' instead\ndoctor: mcp client=opencode config={root}/.config/opencode/opencode.jsonc chat-professor=pfm\n", 1},
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
			if got := rooted(stdout.String(), home); warnings != fixture.warnings || got != fixture.want {
				t.Errorf("warnings=%d output = %q, want %q", warnings, got, fixture.want)
			}
		})
	}
}
