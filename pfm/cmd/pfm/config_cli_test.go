package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestExplicitConfigFlagWinsOverPFMConfig(t *testing.T) {
	root := jailTest(t)
	fromEnv := filepath.Join(root, "env.json")
	fromFlag := filepath.Join(root, "flag.json")
	if err := os.WriteFile(fromEnv, []byte(`{"version":2,"theme":"tokyo-night"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fromFlag, []byte(`{"version":2,"theme":"default"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvConfig, fromEnv)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", fromFlag, "config", "show"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "config path="+fromFlag) {
		t.Fatalf("flag precedence code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExplicitConfigIndexUsesFlagDatabase(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	fromEnv, fromFlag := filepath.Join(root, "env.json"), filepath.Join(root, "flag.json")
	for path, content := range map[string]string{
		fromEnv:  `{"version":2,"state":{"db":"~/env.db"}}`,
		fromFlag: `{"version":2,"state":{"db":"~/flag.db"}}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(paths.EnvConfig, fromEnv)
	t.Setenv(paths.EnvStateDB, "")
	t.Setenv(paths.EnvCacheDB, "")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", fromFlag, "index"}, &stdout, &stderr); code != 0 {
		t.Fatalf("explicit config index=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "flag.db")); err != nil {
		t.Fatalf("flag database: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "env.db")); !os.IsNotExist(err) {
		t.Fatalf("environment database exists or cannot be inspected: %v", err)
	}
}

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
// serve --stdio` with only the harvester enabled configures it and reaches the
// server start: exit 0, no usage refusal and no harvester configuration error. Stdin is closed immediately so the reachable
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
	if code := run([]string{"--config", path, "mcp", "serve", "--stdio"}, &stdout, &stderr); code != 0 ||
		strings.Contains(stderr.String(), "configure harvester") {
		t.Fatalf(
			"run(mcp serve --stdio) code=%d stdout=%q stderr=%q, want exit 0 from the enabled harvester's server start",
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

func TestConfigShowReportsClaudeLaunchPreferenceSources(t *testing.T) {
	root := jailTest(t)
	path := writeConfigFixture(
		t,
		root,
		`{"version":2,"claude":{"webSearchesPerSession":17,"autoCompactWindow":250000,"tmuxTruecolor":false,"cleanupPeriodDays":31,"requireManagedCleanup":false}}`,
	)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", path, "config", "show"}, &stdout, &stderr); code != 0 {
		t.Fatalf("config show code=%d stderr=%q", code, stderr.String())
	}
	for _, want := range []string{
		"config claude.webSearchesPerSession=17 (file)",
		"config claude.autoCompactWindow=250000 (file)",
		"config claude.tmuxTruecolor=false (file)",
		"config claude.cleanupPeriodDays=31 (file)",
		"config claude.requireManagedCleanup=false (file)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("config show lacks %q: %s", want, stdout.String())
		}
	}
	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--config", path, "config", "show"}, &stdout, &stderr); code != 0 {
		t.Fatalf("default config show code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "config claude.autoCompactWindow=100000 (default)") {
		t.Fatalf("default config show lacks auto compact window: %s", stdout.String())
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
