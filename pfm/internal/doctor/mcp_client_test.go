package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
)

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

	pfmBinary := filepath.Join(home, ".local", "bin", "pfm")
	registration := `{"mcpServers":{"harvester":{"type":"http","url":"http://127.0.0.1:18377/mcp/harvester"},"chat":{"type":"stdio","command":"` + pfmBinary + `","args":["mcp","chat","serve"]}}}`
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
		" (account 1 (pfm spawns it without CLAUDE_CONFIG_DIR)) harvester=pfm chat=pfm"
	if !strings.Contains(out, implicitRow) {
		t.Fatalf("doctor output missing the healthy implicit-account row %q:\n%s", implicitRow, out)
	}
	ambientRow := "doctor: mcp client=claude registry=" + filepath.Join(ambient, ".claude.json") +
		" (ambient CLAUDE_CONFIG_DIR=" + ambient + " (the claude launcher passes it through — internal_launch.go)) harvester=absent chat=absent" +
		" remediation=run pfm install --yes (registers every registry a pfm-launched Claude reads)"
	if !strings.Contains(out, ambientRow) {
		t.Fatalf("doctor output missing the absent ambient-registry warning row %q:\n%s", ambientRow, out)
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
	write(
		`{"mcp":{"chat":{"type":"local","command":["` + filepath.Join(
			home,
			".local",
			"bin",
			"pfm",
		) + `","mcp","chat","serve"],"enabled":true},"harvester":{"type":"remote","url":"http://127.0.0.1:18377/mcp/harvester","enabled":true}}}`,
	)
	var stdout, stderr bytes.Buffer
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("healthy OpenCode doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "doctor: mcp client=opencode config="+path+" harvester=pfm chat=pfm") {
		t.Fatalf("doctor omitted healthy OpenCode row:\n%s", stdout.String())
	}
	write(`{"mcp":{"chat":{"type":"local","enabled":true}}}`)
	stdout.Reset()
	if code := runDoctor(
		nil,
		&stdout,
		&stderr,
		runtime,
	); code != 1 ||
		!strings.Contains(stdout.String(), "state=partial") {
		t.Fatalf("partial OpenCode doctor code=%d stdout=%q", code, stdout.String())
	}
	write(`{"mcp":{}}`)
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 ||
		!strings.Contains(stdout.String(), "state=absent") ||
		strings.Contains(stdout.String(), "remediation=run pfm install --yes") {
		t.Fatalf("absent OpenCode doctor code=%d stdout=%q, want clean absence", code, stdout.String())
	}
	// Nothing in the install ownership ledger claims this entry, so the
	// remediation is the user-owned one (TestDoctorOpenCodeNamesAUserOwnedEntry
	// pins both halves of that split).
	write(`{"mcp":{"chat":{"type":"remote","url":"https://operator.invalid","enabled":true}}}`)
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 ||
		!strings.Contains(stdout.String(), "state=foreign-registration") ||
		!strings.Contains(stdout.String(), "is a user-owned entry pfm install will not replace") {
		t.Fatalf("foreign OpenCode doctor code=%d stdout=%q, want remediation warning", code, stdout.String())
	}
	write(`{`)
	stdout.Reset()
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 ||
		!strings.Contains(stdout.String(), "state=unreadable") ||
		!strings.Contains(stdout.String(), "error=") {
		t.Fatalf("unreadable OpenCode doctor code=%d stdout=%q, want error warning", code, stdout.String())
	}
}

// TestDoctorOpenCodeNamesAUserOwnedEntry is a REGRESSION test for the host
// whose ~/.config/opencode/opencode.jsonc already holds a hand-written
// `harvester` entry (`uv --directory … run harvester`): doctor reported
// state=partial and told the operator to run `pfm install --yes`, which
// preserves that entry forever — the advice could never fix what it named. An
// entry the install ownership ledger does not claim is named as user-owned,
// with the file and the key; a pfm-written entry gone stale keeps the plain
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
	foreign := `{"mcp":{"harvester":{"type":"local","command":["uv","--directory","/srv/harvester","run","harvester"]}}}`
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
		"config=" + path,
		"remediation=harvester in " + path + " is a user-owned entry pfm install will not replace",
		"remove or rename it, then run pfm install --yes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor OpenCode row missing %q:\n%s", want, out)
		}
	}

	// A pfm-written entry whose port went stale is install's to rewrite: the
	// ledger claims it, so the remediation stays the plain reinstall.
	stale := `{"mcp":{"harvester":{"type":"remote","url":"http://127.0.0.1:19999/mcp/harvester","enabled":true}}}`
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(home, ".local", "share", "pfm", "install", "mcp-ownership.json")
	if err := os.MkdirAll(filepath.Dir(ledger), 0o700); err != nil {
		t.Fatal(err)
	}
	owned := `{"opencodeRegistrations":{"` + path + `":{"harvester":` +
		`{"type":"remote","url":"http://127.0.0.1:19999/mcp/harvester","enabled":true}}}}`
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
