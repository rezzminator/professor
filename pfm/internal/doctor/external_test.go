package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
)

func TestDoctorWarnsWhenLegacyHarvesterClientsStillOwnTheRoute(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	if err := os.WriteFile(
		filepath.Join(home, ".mcp.json"),
		[]byte(
			`{"mcpServers":{"harvester":{"type":"stdio","command":"uv","args":["--directory","/fixture/legacy-harvester","run","harvester"]}}}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(home, ".codex", "config.toml"),
		[]byte(
			"[mcp_servers.harvester]\ncommand = \"uv\"\nargs = [\"--directory\", \"/fixture/legacy-harvester\", \"run\", \"harvester\"]\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}}
	var stdout, stderr bytes.Buffer
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q, want warning exit", code, stdout.String(), stderr.String())
	}
	for _, client := range []string{"claude", "codex"} {
		want := "doctor: mcp client=" + client + " harvester=legacy-standalone"
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "/fixture/legacy-harvester") {
		t.Fatalf("doctor leaked registration paths instead of a bounded diagnosis:\n%s", stdout.String())
	}
}

func TestDoctorReportsHarvesterCutoverForModernForeignAndUnreadableClients(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	codexPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(t *testing.T) string {
		t.Helper()
		runtime, err := pfmconfig.LoadRuntime("")
		if err != nil {
			t.Fatal(err)
		}
		runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}}
		var stdout, stderr bytes.Buffer
		if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 {
			t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	t.Run("modern no-auth loopback routes are complete", func(t *testing.T) {
		write(
			filepath.Join(home, ".mcp.json"),
			`{"mcpServers":{"harvester":{"type":"http","url":"http://127.0.0.1:18377/mcp/harvester"}}}`,
		)
		write(codexPath, "[mcp_servers.harvester]\nurl = \"http://127.0.0.1:18377/mcp/harvester\"\n")
		output := run(t)
		if !strings.Contains(output, "doctor: mcp client-cutover=complete") {
			t.Fatalf("modern no-auth routes were not reported complete:\n%s", output)
		}
	})

	t.Run("loopback routes with retired authentication are incomplete", func(t *testing.T) {
		write(
			filepath.Join(home, ".mcp.json"),
			`{"mcpServers":{"harvester":{"type":"http","url":"http://127.0.0.1:18377/mcp/harvester","headers":{"Authorization":"Bearer retired"}}}}`,
		)
		write(
			codexPath,
			"[mcp_servers.harvester]\nurl = \"http://127.0.0.1:18377/mcp/harvester\"\n[mcp_servers.harvester.headers]\nAuthorization = \"Bearer retired\"\n",
		)
		runtime, err := pfmconfig.LoadRuntime("")
		if err != nil {
			t.Fatal(err)
		}
		runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}}
		var stdout, stderr bytes.Buffer
		if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 {
			t.Fatalf(
				"doctor code=%d stdout=%q stderr=%q, want retired-auth warnings",
				code,
				stdout.String(),
				stderr.String(),
			)
		}
		for _, client := range []string{"claude", "codex"} {
			want := "doctor: mcp client=" + client + " harvester=foreign-registration warning=consumer cutover incomplete"
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("retired-auth route output missing %q:\n%s", want, stdout.String())
			}
		}
	})

	t.Run("foreign routes are warnings for both clients", func(t *testing.T) {
		write(
			filepath.Join(home, ".mcp.json"),
			`{"mcpServers":{"harvester":{"type":"http","url":"https://foreign.invalid/mcp"}}}`,
		)
		write(codexPath, "[mcp_servers.harvester]\nurl = \"https://foreign.invalid/mcp\"\n")
		runtime, err := pfmconfig.LoadRuntime("")
		if err != nil {
			t.Fatal(err)
		}
		runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}}
		var stdout, stderr bytes.Buffer
		if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 {
			t.Fatalf(
				"doctor code=%d stdout=%q stderr=%q, want foreign-route warnings",
				code,
				stdout.String(),
				stderr.String(),
			)
		}
		for _, client := range []string{"claude", "codex"} {
			want := "doctor: mcp client=" + client + " harvester=foreign-registration warning=consumer cutover incomplete"
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("foreign route output missing %q:\n%s", want, stdout.String())
			}
		}
	})

	t.Run("malformed Claude JSON is unreadable", func(t *testing.T) {
		write(filepath.Join(home, ".mcp.json"), `{"mcpServers":`)
		write(codexPath, "[mcp_servers.harvester]\nurl = \"http://127.0.0.1:18377/mcp/harvester\"\n")
		runtime, err := pfmconfig.LoadRuntime("")
		if err != nil {
			t.Fatal(err)
		}
		runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}}
		var stdout, stderr bytes.Buffer
		if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 {
			t.Fatalf(
				"doctor code=%d stdout=%q stderr=%q, want unreadable warning",
				code,
				stdout.String(),
				stderr.String(),
			)
		}
		if !strings.Contains(stdout.String(), "doctor: mcp client=claude harvester=unreadable error=") {
			t.Fatalf("malformed Claude JSON was not distinguished from absence:\n%s", stdout.String())
		}
	})

	t.Run("malformed Codex TOML is unreadable", func(t *testing.T) {
		write(
			filepath.Join(home, ".mcp.json"),
			`{"mcpServers":{"harvester":{"type":"http","url":"http://127.0.0.1:18377/mcp/harvester"}}}`,
		)
		write(codexPath, "[mcp_servers.harvester\n")
		runtime, err := pfmconfig.LoadRuntime("")
		if err != nil {
			t.Fatal(err)
		}
		runtime.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}}
		var stdout, stderr bytes.Buffer
		if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 {
			t.Fatalf(
				"doctor code=%d stdout=%q stderr=%q, want unreadable warning",
				code,
				stdout.String(),
				stderr.String(),
			)
		}
		if !strings.Contains(stdout.String(), "doctor: mcp client=codex harvester=unreadable error=") {
			t.Fatalf("malformed Codex TOML was not distinguished from absence:\n%s", stdout.String())
		}
	})
}

func TestDoctorEnumeratesExternalDependenciesAndInstalledHooks(t *testing.T) {
	clearRetiredHarvesterEnv(t) // golden doctor output must not depend on an ambient retired harvester variable
	jailTest(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf(
			"doctor code=%d, want clean injected probes\nstdout=%s\nstderr=%s",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	for _, wanted := range []string{
		"doctor: dep tmux ",
		"doctor: hook claude[1] ",
	} {
		if !strings.Contains(stdout.String(), wanted) {
			t.Fatalf("doctor output missing %q:\n%s", wanted, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "doctor: hook codex[") {
		t.Fatalf("doctor invented a Codex hook for an empty roster:\n%s", stdout.String())
	}
}

func TestDependencyDoctorRowsKeepMissingBrokenAndSkippedDistinct(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	entries := []deps.Entry{
		{Name: "tmux", Required: true, MinVersion: "1.8"},
		{Name: "ps", Required: true, Platforms: []string{"darwin"}},
		{Name: "codex", Required: true, InstallHint: "install configured Codex"},
		{Name: "claude", Required: true},
	}
	DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result {
		return []deps.Result{
			{Entry: entries[0], State: deps.StateOK, Path: "/fixture/tmux", Version: "3.4"},
			{Entry: entries[1], State: deps.StateSkipped, Error: "not this platform"},
			{Entry: entries[2], State: deps.StateMissing},
			{
				Entry: entries[3],
				State: deps.StateBroken,
				Path:  "/fixture/claude",
				Error: "exit status 1",
				Raw:   "damaged install\nmore",
			},
		}
	}
	var output bytes.Buffer
	if _, failures, _ := PrintDependencies(
		context.Background(),
		&output,
		"",
		entries,
		deps.ProbeOptions{},
	); failures != 2 {
		t.Fatalf("failures=%d, want 2\n%s", failures, output.String())
	}
	want := strings.Join([]string{
		"doctor: dep tmux path=/fixture/tmux version=3.4 min=1.8 ok",
		"doctor: dep ps platform=darwin skipped (not this platform)",
		"doctor: dep codex path=(none) MISSING required — install: install configured Codex",
		`doctor: dep claude path=/fixture/claude broken error=exit status 1 raw="damaged install"`,
		"",
	}, "\n")
	if output.String() != want {
		t.Fatalf("dependency rows:\n%s\nwant:\n%s", output.String(), want)
	}
}

// TestDependencyDoctorClaudeAbsenceIsNamedNotWarned pins the ruling: pfm's own
// launcher exiting 127 (its "no real Claude binary" contract, assets/bin/claude)
// is absence — MISSING optional, no warning — while the SAME exit code from a
// binary that is not pfm's launcher, and pfm's launcher exiting anything else,
// both stay broken and counted. The identity check is installer.ClaudeAbsent,
// never a string match on stderr.
func TestDependencyDoctorClaudeAbsenceIsNamedNotWarned(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	home := t.TempDir()
	launcher := filepath.Join(home, ".local", "bin", pfmengine.MustLookup(pfmengine.Claude).Binary)
	// Required:true here (unlike the real registry's optional claude entry) is
	// what makes the "still counted" half of this test meaningful: it proves
	// absence overrides the warning even for a dependency that would
	// otherwise count one, and that a merely-broken claude still gets it.
	entry := deps.Entry{Name: "claude", Engine: pfmengine.Claude, Required: true}
	nonPfm := filepath.Join(home, "opt", "claude")

	cases := []struct {
		name       string
		path       string
		exitCode   int
		wantMissed bool
	}{
		{"pfms launcher absent", launcher, 127, true},
		{"pfms launcher broken", launcher, 1, false},
		{"non-pfm claude at 127", nonPfm, 127, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result {
				return []deps.Result{{
					Entry: entry, State: deps.StateBroken, Path: testCase.path,
					ExitCode: testCase.exitCode, Error: fmt.Sprintf("exit status %d", testCase.exitCode),
				}}
			}
			var output bytes.Buffer
			_, failures, claudeAbsent := PrintDependencies(
				context.Background(),
				&output,
				home,
				[]deps.Entry{entry},
				deps.ProbeOptions{},
			)
			if claudeAbsent != testCase.wantMissed {
				t.Fatalf("claudeAbsent=%v, want %v", claudeAbsent, testCase.wantMissed)
			}
			if testCase.wantMissed {
				if failures != 0 {
					t.Fatalf("failures=%d, want 0\n%s", failures, output.String())
				}
				want := "doctor: dep claude path=" + testCase.path + " MISSING optional — install: install Claude Code (the pfm launcher has no real binary to run)\n"
				if output.String() != want {
					t.Fatalf("output=%q, want %q", output.String(), want)
				}
			} else {
				if failures != 1 {
					t.Fatalf("failures=%d, want 1 (still broken, still counted)\n%s", failures, output.String())
				}
				if !strings.Contains(output.String(), "broken") {
					t.Fatalf("output never called it broken:\n%s", output.String())
				}
			}
		})
	}
}

func TestDependencyDoctorTimeoutRowNamesTimeoutNotBroken(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	entries := []deps.Entry{
		{Name: "tmux", Required: true},
	}
	DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result {
		return []deps.Result{
			{Entry: entries[0], State: deps.StateTimeout, Path: "/fixture/tmux", Error: "timeout (5s)"},
		}
	}
	var output bytes.Buffer
	_, failures, _ := PrintDependencies(context.Background(), &output, "", entries, deps.ProbeOptions{})
	if failures != 1 {
		t.Fatalf(
			"failures=%d, want 1 — a required timed-out dep still contributes its failure\n%s",
			failures,
			output.String(),
		)
	}
	if !strings.Contains(output.String(), "timeout") {
		t.Fatalf("dependency row missing %q:\n%s", "timeout", output.String())
	}
	if strings.Contains(output.String(), "broken") {
		t.Fatalf("dependency row must not call an unanswered probe broken:\n%s", output.String())
	}
}

func TestDependencyDoctorCancellationRowNamesCallerStopNotBroken(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	entry := deps.Entry{Name: "tmux", Required: true}
	DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result {
		return []deps.Result{{
			Entry: entry, State: deps.StateCancelled, Path: "/fixture/tmux",
			Error: "cancelled by parent context",
		}}
	}
	var output bytes.Buffer
	_, failures, _ := PrintDependencies(context.Background(), &output, "", []deps.Entry{entry}, deps.ProbeOptions{})
	if failures != 1 {
		t.Fatalf("failures=%d, want 1 for a required unanswered probe\n%s", failures, output.String())
	}
	if want := "doctor: dep tmux path=/fixture/tmux cancelled error=cancelled by parent context — probe stopped by its caller; unverified, no fault established"; !strings.Contains(
		output.String(),
		want,
	) {
		t.Fatalf("output=%q, want caller cancellation row %q", output.String(), want)
	}
	if strings.Contains(output.String(), "broken") {
		t.Fatalf("caller cancellation must not be diagnosed as broken:\n%s", output.String())
	}
}
