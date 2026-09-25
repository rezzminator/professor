package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestConfigCLIRejectsGlobalConfigSyntaxAndLoadErrors(t *testing.T) {
	root := jailTest(t)
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing path",
			args: []string{"--config"},
			want: "--config requires a path",
		},
		{
			name: "empty equals path",
			args: []string{"--config="},
			want: "--config requires a path",
		},
		{
			name: "duplicate path",
			args: []string{"--config", "one.json", "--config=two.json"},
			want: "--config may be specified only once",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != 2 {
				t.Fatalf(
					"run(%q) code=%d stdout=%q stderr=%q, want usage error",
					test.args,
					code,
					stdout.String(),
					stderr.String(),
				)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) ||
				!strings.Contains(stderr.String(), "usage:") {
				t.Fatalf(
					"run(%q) stdout=%q stderr=%q, want %q and usage",
					test.args,
					stdout.String(),
					stderr.String(),
					test.want,
				)
			}
		})
	}

	path := filepath.Join(root, "machine.json")
	if err := os.WriteFile(path, []byte(`{"version":1`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", path, "mcp", "ls"}, &stdout, &stderr); code != 1 {
		t.Fatalf(
			"run(malformed config) code=%d stdout=%q stderr=%q, want config failure",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "pfm: config: parse config "+path) ||
		!strings.Contains(stderr.String(), "byte ") {
		t.Fatalf(
			"run(malformed config) stdout=%q stderr=%q, want path and byte offset",
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestConfigCLIMCPListReportsConfiguredStateAndSource(t *testing.T) {
	root := jailTest(t)
	path := writeConfigFixture(t, root, `{
  "version": 1,
  "mcp": {"servers": {"chat": {"enabled": true}}}
}`)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", path, "mcp", "ls"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(mcp ls) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "chat\ttrue\tfile\nharvester\tfalse\tdefault\n"; got != want {
		t.Fatalf("run(mcp ls) stdout=%q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("run(mcp ls) stderr=%q, want empty", stderr.String())
	}
}

func TestConfigCLIMCPStdioRefusesWhenEveryServerIsDisabled(t *testing.T) {
	root := jailTest(t)
	path := writeConfigFixture(t, root, `{
  "version": 1,
  "mcp": {"servers": {"chat": {"enabled": false}, "harvester": {"enabled": false}}}
}`)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", path, "mcp", "serve", "--stdio"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run(disabled mcp serve --stdio) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	want := "pfm mcp serve --stdio: every registered server is disabled by config " + path +
		"; enable at least one with: pfm mcp <server> enable"
	if stdout.Len() != 0 || strings.TrimSpace(stderr.String()) != want {
		t.Fatalf(
			"run(disabled mcp serve --stdio) stdout=%q stderr=%q, want actionable message %q",
			stdout.String(),
			stderr.String(),
			want,
		)
	}
}

// TestConfigCLIMCPStdioHarvesterOnlyReachesServerStart pins that `pfm mcp
// serve --stdio` with only the harvester enabled reaches the server start
// rather than a usage refusal. Stdin is closed immediately so the reachable
// server terminates instead of blocking the test on stdio framing.
func TestConfigCLIMCPStdioHarvesterOnlyReachesServerStart(t *testing.T) {
	root := jailTest(t)
	path := writeConfigFixture(t, root, `{
  "version": 1,
  "mcp": {"servers": {"chat": {"enabled": false}, "harvester": {"enabled": true}}}
}`)

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = previous
		_ = reader.Close()
	}()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", path, "mcp", "serve", "--stdio"}, &stdout, &stderr); code == 2 ||
		strings.Contains(stderr.String(), "usage: pfm mcp") {
		t.Fatalf(
			"run(mcp serve --stdio) code=%d stdout=%q stderr=%q, want the enabled harvester's server start, not usage",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}

// Every form outside ls, serve [--stdio] and <server> enable|disable prints
// the usage line and exits 2 — a bare server name included.
func TestConfigCLIMCPUnknownFormsPrintUsage(t *testing.T) {
	root := jailTest(t)
	path := writeConfigFixture(t, root, `{"version": 1}`)
	const usage = "usage: pfm mcp ls | pfm mcp serve [--stdio] | pfm mcp <server> enable|disable"
	for _, args := range [][]string{{"chat"}, {"serve", "--http"}} {
		var stdout, stderr bytes.Buffer
		argv := append([]string{"--config", path, "mcp"}, args...)
		if code := run(argv, &stdout, &stderr); code != 2 || strings.TrimSpace(stderr.String()) != usage {
			t.Errorf("run(mcp %q) code=%d stderr=%q, want 2 and %q", args, code, stderr.String(), usage)
		}
	}
}

func TestConfigCLIMCPEnableDisableAreIdempotentWithoutStartingStdio(t *testing.T) {
	root := jailTest(t)
	path := writeConfigFixture(t, root, `{
  "version": 1,
  "mcp": {"servers": {"chat": {"enabled": false}}}
}`)

	assertMCPToggle := func(action, wantState string) []byte {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := run([]string{"--config", path, "mcp", "chat", action}, &stdout, &stderr); code != 0 {
			t.Fatalf("run(mcp chat %s) code=%d stdout=%q stderr=%q", action, code, stdout.String(), stderr.String())
		}
		want := "chat\t" + action + "d\t" + wantState + "\n"
		if stdout.String() != want || stderr.Len() != 0 {
			t.Fatalf(
				"run(mcp chat %s) stdout=%q stderr=%q, want stdout %q and empty stderr",
				action,
				stdout.String(),
				stderr.String(),
				want,
			)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}

	initial, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertMCPToggle("enable", "updated")
	beforeRepeatEnable := mustReadFile(t, path)
	afterEnable := assertMCPToggle("enable", "unchanged")
	if !bytes.Equal(beforeRepeatEnable, afterEnable) {
		t.Fatal("idempotent enable changed the config")
	}
	if bytes.Equal(initial, afterEnable) {
		t.Fatal("enable did not update the config")
	}

	assertMCPToggle("disable", "updated")
	beforeRepeatDisable := mustReadFile(t, path)
	afterDisable := assertMCPToggle("disable", "unchanged")
	if !bytes.Equal(beforeRepeatDisable, afterDisable) {
		t.Fatal("idempotent disable changed the config")
	}
	if bytes.Equal(afterEnable, afterDisable) {
		t.Fatal("disable did not update the config")
	}
}

func TestConfigCLIInitShowValidateAndBrokenDiagnostics(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config", "config.json")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", path, "config", "init"}, &stdout, &stderr); code != 0 {
		t.Fatalf("config init code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "field documentation:") || stderr.Len() != 0 {
		t.Fatalf("config init stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "//") {
		t.Fatalf("config init wrote comments: %s", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config init mode=%o, want 600", info.Mode().Perm())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"--config", path, "config", "validate"},
		&stdout,
		&stderr,
	); code != 0 ||
		!strings.Contains(stdout.String(), "config valid:") {
		t.Fatalf("config validate code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"--config", path, "config", "show"},
		&stdout,
		&stderr,
	); code != 0 ||
		strings.Contains(stdout.String(), "authToken") {
		t.Fatalf("config show code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	if err := os.WriteFile(path, []byte(`{"version":2`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"--config", path, "config", "show"},
		&stdout,
		&stderr,
	); code != 0 ||
		!strings.Contains(stderr.String(), "configuration error: parse config "+path) {
		t.Fatalf("broken config show code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"--config", path, "config", "validate"},
		&stdout,
		&stderr,
	); code != 1 ||
		!strings.Contains(stderr.String(), "parse config "+path) {
		t.Fatalf("broken config validate code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"--config", path, "doctor"},
		&stdout,
		&stderr,
	); code == 0 ||
		!strings.Contains(stdout.String(), "doctor: config error=") {
		t.Fatalf("broken doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestConfigShowDistinguishesInputAndEffectiveSchema(t *testing.T) {
	root := jailTest(t)
	path := writeConfigFixture(t, root, `{"version":1}`)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", path, "config", "show"}, &stdout, &stderr); code != 0 {
		t.Fatalf("config show code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if want := "config version=2 effective (input=1 file)\n"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("config show stdout=%q, want explicit schema line %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("config show stderr=%q, want empty", stderr.String())
	}
}

func TestConfiguredAccountRosterIsTheExactTranscriptSearchBoundary(t *testing.T) {
	root := jailTest(t)
	// The jail's home, not the plain HOME variable: this test writes a fixture
	// transcript under it, and reading the raw variable once put that fixture
	// in the operator's live ~/.claude/projects.
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	home := resolved.Home
	configuredRoot := filepath.Join(home, "configured-account")
	configuredProject := filepath.Join(configuredRoot, "projects", "configured-project")
	legacyProject := filepath.Join(home, ".claude", "projects", "legacy-project")
	for _, directory := range []string{configuredProject, legacyProject} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configuredNeedle := "configured transcript phrase that must be discoverable"
	legacyNeedle := "legacy transcript phrase that configured accounts must exclude"
	if err := os.WriteFile(
		filepath.Join(configuredProject, "configured-session.jsonl"),
		[]byte(`{"type":"user","message":{"content":"`+configuredNeedle+`"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(legacyProject, "legacy-session.jsonl"),
		[]byte(`{"type":"user","message":{"content":"`+legacyNeedle+`"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	configPath := writeConfigFixture(t, root, `{
  "version": 1,
  "accounts": [{"id": 9, "configDir": "`+configuredRoot+`"}]
}`)

	find := func(name, needle string) (int, string, string) {
		t.Helper()
		excerptPath := filepath.Join(root, name+".txt")
		if err := os.WriteFile(excerptPath, []byte(needle+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{"--config", configPath, "chat", "find", excerptPath}, &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}

	if code, stdout, stderr := find("configured", configuredNeedle); code != 0 ||
		!strings.Contains(stdout, "configured-session") || strings.Contains(stdout+stderr, "legacy-session") {
		t.Fatalf("configured lookup code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := find("legacy", legacyNeedle); code != 2 || stdout != "" ||
		!strings.Contains(stderr, "no session contains the excerpt") {
		t.Fatalf("legacy lookup escaped configured boundary: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func writeConfigFixture(t *testing.T, root, content string) string {
	t.Helper()
	path := filepath.Join(root, "machine.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
