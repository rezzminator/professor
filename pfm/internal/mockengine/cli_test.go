package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

func assertCLIOpensNoSession(t *testing.T, fix *fixture) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(fix.configDir, "projects"), filepath.Join(fix.codexHome, "sessions"),
		fix.sidDir, fix.procRoot,
	} {
		entries, err := os.ReadDir(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || len(entries) != 0 {
			t.Fatalf("CLI opened session state at %s: entries=%v err=%v", path, entries, err)
		}
	}
	entries, err := os.ReadDir(fix.recordDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "argv" {
		t.Fatalf("CLI recorded session or hook activity: %v", entries)
	}
}

func TestEngineDoctorAnswersWithoutStartingASession(t *testing.T) {
	for _, row := range []struct {
		engine string
		args   []string
	}{
		{"claude", []string{"doctor"}},
		{"claude", []string{"doctor", "--help"}},
		{"codex", []string{"doctor", "--help"}},
		{"codex", []string{"doctor", "--summary", "--ascii", "--no-color"}},
		{"claude", []string{"doctor", "--fixture-argument"}},
	} {
		t.Run(row.engine+"/"+strings.Join(row.args, " "), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.installHooks()
			code, stdout, stderr := runOnce(fix, row.engine, row.args, "")
			want := "mock-engine: " + row.engine + " doctor — fixture engine, nothing checked\n"
			if code != 0 || stdout != want || stderr != "" {
				t.Fatalf("doctor exit=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, want)
			}
			assertCLIOpensNoSession(t, fix)
		})
	}
}

func TestClaudePluginAnswersWithoutStartingASession(t *testing.T) {
	for _, row := range []struct {
		args []string
		want string
	}{
		{
			[]string{"plugin", "marketplace", "add", "rezzminator/x"},
			"mock-engine: claude plugin marketplace add rezzminator/x — fixture, nothing fetched\n",
		},
		{
			[]string{"plugin", "install", "x@x", "-y"},
			"mock-engine: claude plugin install x@x — fixture, recorded installed and enabled in %s\n",
		},
		{
			[]string{"plugin", "install", "x@x"},
			"mock-engine: claude plugin install x@x — fixture, recorded installed and enabled in %s\n",
		},
	} {
		t.Run(strings.Join(row.args, " "), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.installHooks()
			for range 2 {
				var out, errs bytes.Buffer
				code := Main(
					context.Background(),
					"claude",
					row.args,
					strings.NewReader(""),
					&out,
					&errs,
					fix.env(map[string]string{"CLAUDE_CONFIG_DIR": fix.configDir}),
				)
				stdout, stderr := out.String(), errs.String()
				want := row.want
				if row.args[1] == "install" {
					want = fmt.Sprintf(want, fix.configDir)
				}
				if code != 0 || stdout != want || stderr != "" {
					t.Fatalf("plugin exit=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, want)
				}
				assertCLIOpensNoSession(t, fix)
			}
		})
	}
}

func TestClaudeUnscriptedPluginsAreRefusedWithoutStartingASession(t *testing.T) {
	for _, args := range [][]string{
		{"plugin", "list"},
		{"plugin"},
		{"plugin", "marketplace", "add"},
		{"plugin", "marketplace", "add", "source", "extra"},
		{"plugin", "install"},
		{"plugin", "install", "x@x", "--unknown"},
		{"plugin", "install", "x@x", "-y", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.installHooks()
			code, stdout, stderr := runOnce(fix, "claude", args, "")
			want := "mock-engine: claude " + strings.Join(args, " ") + " is not scripted\n"
			if code != ExitUnpinned || stdout != "" || stderr != want {
				t.Fatalf("plugin refusal exit=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, want)
			}
			assertCLIOpensNoSession(t, fix)
		})
	}
}

func TestEngineDoctorPassesPfmsDependencyProbe(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installHooks()
	var engines []deps.Entry
	entries := deps.Registry(deps.Options{
		Home: fix.home, ClaudeBinary: filepath.Join(fix.bin, "claude"), CodexBinary: filepath.Join(fix.bin, "codex"),
	})
	for index := range entries {
		if entries[index].Engine != "" {
			engines = append(engines, entries[index])
		}
	}
	results := deps.Probe(context.Background(), engines, deps.ProbeOptions{Runner: deps.RealRunner{}})
	if len(results) != 2 {
		t.Fatalf("engine probes=%+v, want both registered engines", results)
	}
	for index := range results {
		result := &results[index]
		if result.State != deps.StateOK || result.SelfDoctor != "ok" {
			t.Fatalf("%s probe=%+v, want state ok and self-doctor ok", result.Entry.Engine, result)
		}
	}
	assertCLIOpensNoSession(t, fix)
}

func pluginCLI(t *testing.T, fix *fixture, config string, args ...string) (int, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Main(
		context.Background(),
		"claude",
		args,
		strings.NewReader(""),
		&out,
		&errs,
		fix.env(map[string]string{"CLAUDE_CONFIG_DIR": config, "HOME": fix.home}),
	)
	return code, errs.String()
}

func TestClaudePluginInstallReaderContract(t *testing.T) {
	fix := newFixture(t)
	ids, err := installer.ClaudePluginsNotInstalled(fix.configDir)
	if err != nil || len(ids) == 0 {
		t.Fatalf("fresh ids=%v err=%v", ids, err)
	}
	for _, id := range ids {
		if code, msg := pluginCLI(t, fix, fix.configDir, "plugin", "install", id, "-y"); code != 0 {
			t.Fatalf("exit=%d %s", code, msg)
		}
	}
	missing, err := installer.ClaudePluginsNotInstalled(fix.configDir)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
	gaps, err := installer.ClaudePluginGaps(filepath.Join(fix.configDir, "settings.json"))
	if err != nil || len(gaps) != 0 {
		t.Fatalf("gaps=%v err=%v", gaps, err)
	}
}

func TestClaudePluginInstallSettingsAndRepeat(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			fix := newFixture(t)
			path := filepath.Join(fix.configDir, "settings.json")
			target := path
			if shared {
				target = filepath.Join(fix.home, "shared.json")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			before := []byte(`{"hooks":{"Stop":[]},"enabledPlugins":{"other":true},"extra":42}`)
			if err := os.WriteFile(target, before, 0o600); err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(fix.configDir, "plugins", "installed_plugins.json")
			if err := os.MkdirAll(filepath.Dir(record), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				record,
				[]byte(`{"extra":42,"plugins":{"other":[{"installPath":"kept"}]}}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			if code, msg := pluginCLI(t, fix, fix.configDir, "plugin", "install", "example"); code != 0 {
				t.Fatalf("%d %s", code, msg)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err = json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			enabled := doc["enabledPlugins"].(map[string]any)
			if doc["hooks"] == nil || doc["extra"] != float64(42) || enabled["other"] != true ||
				enabled["example"] != true {
				t.Fatalf("settings=%s", data)
			}
			if shared {
				info, err := os.Lstat(path)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("symlink lost: %v", err)
				}
			}
			installed, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(installed, &doc); err != nil {
				t.Fatal(err)
			}
			if doc["extra"] != float64(42) || doc["plugins"].(map[string]any)["other"] == nil {
				t.Fatalf("record=%s", installed)
			}
			if code, msg := pluginCLI(t, fix, fix.configDir, "plugin", "install", "example"); code != 0 {
				t.Fatalf("%d %s", code, msg)
			}
			for file, want := range map[string][]byte{path: data, record: installed} {
				got, err := os.ReadFile(file)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("repeat changed %s: %v", file, err)
				}
			}
		})
	}
}

func TestClaudePluginInstallDefaultConfig(t *testing.T) {
	fix := newFixture(t)
	if code, msg := pluginCLI(t, fix, "", "plugin", "install", "example"); code != 0 {
		t.Fatalf("%d %s", code, msg)
	}
	for _, file := range []string{"settings.json", "plugins/installed_plugins.json"} {
		info, err := os.Stat(filepath.Join(fix.home, ".claude", file))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode=%v", info.Mode())
		}
	}
}

func TestClaudePluginInstallInvalidRecords(t *testing.T) {
	for _, bad := range []string{"settings.json", "plugins/installed_plugins.json"} {
		t.Run(bad, func(t *testing.T) {
			fix := newFixture(t)
			originals := map[string][]byte{
				"settings.json":                  []byte(`{"enabledPlugins":{"other":true}}`),
				"plugins/installed_plugins.json": []byte(`{"plugins":{}}`),
			}
			originals[bad] = []byte("invalid")
			for file, data := range originals {
				path := filepath.Join(fix.configDir, file)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			code, msg := pluginCLI(t, fix, fix.configDir, "plugin", "install", "example")
			if code != ExitUsage || !strings.HasPrefix(msg, "mock-engine: ") ||
				!strings.Contains(msg, filepath.Join(fix.configDir, bad)) ||
				strings.Count(msg, "\n") != 1 {
				t.Fatalf("exit=%d stderr=%q", code, msg)
			}
			for file, want := range originals {
				got, err := os.ReadFile(filepath.Join(fix.configDir, file))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("changed %s: %v", file, err)
				}
			}
		})
	}
}

func TestClaudeMarketplaceWritesNothing(t *testing.T) {
	fix := newFixture(t)
	if code, msg := pluginCLI(t, fix, fix.configDir, "plugin", "marketplace", "add", "source"); code != 0 {
		t.Fatalf("%d %s", code, msg)
	}
	entries, err := os.ReadDir(fix.configDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}
