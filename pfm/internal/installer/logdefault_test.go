package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// logDefaultEngine builds an engine whose pfm.config.json is path.
func logDefaultEngine(t *testing.T, path string, apply bool, stdout io.Writer) *engine {
	t.Helper()
	home := t.TempDir()
	return &engine{
		options: Options{
			Mode: ModeApply, Home: home, ConfigDir: filepath.Join(home, ".claude"),
			MCPConfigPath: path, Runner: &fakeRunner{}, Stdout: stdout,
		},
		apply: apply, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"), stamp: "fixture",
	}
}

// readLogBlock decodes the `log` key of the file at path.
func readLogBlock(t *testing.T, path string) (map[string]any, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	top := map[string]any{}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, raw)
	}
	block, present := top["log"].(map[string]any)
	return block, present
}

// TestInstallWritesTheLogDefaultWhereNoneExists pins § Control's visible safe
// default: a pfm.config.json without a `log` key gains {"level": "info",
// "keepDays": 30} through the ledger (a `change` row), and nothing else moves.
func TestInstallWritesTheLogDefaultWhereNoneExists(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	if err := os.WriteFile(path, []byte(`{"version": 2, "theme": "dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	installer := logDefaultEngine(t, path, true, &stdout)
	if err := installer.wireLogDefault(); err != nil {
		t.Fatal(err)
	}
	block, present := readLogBlock(t, path)
	if !present {
		t.Fatalf("no log key written:\n%s", stdout.String())
	}
	if block["level"] != pfmconfig.InstallLogLevel || block["keepDays"] != float64(30) || len(block) != 2 {
		t.Fatalf("log block = %v, want level info + keepDays 30", block)
	}
	if installer.report.Changed != 1 || !strings.Contains(stdout.String(), "change") ||
		!strings.Contains(stdout.String(), path) {
		t.Fatalf("ledger = changed %d:\n%s", installer.report.Changed, stdout.String())
	}
	if _, err := pfmconfig.Load(path, installer.options.Home, nil); err != nil {
		t.Fatalf("the strict loader refused the installed file: %v", err)
	}
	// A second install finds the key and leaves the file byte-identical.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second := logDefaultEngine(t, path, true, io.Discard)
	if err := second.wireLogDefault(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || second.report.Changed != 0 || second.report.OK != 1 {
		t.Fatalf(
			"a re-install touched an installed log block (changed=%d ok=%d)", second.report.Changed, second.report.OK,
		)
	}
}

// TestInstallNeverOverwritesAnExistingLogKey is the spec's own test: install
// over a config carrying "level": "off" and read `off` back.
func TestInstallNeverOverwritesAnExistingLogKey(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	original := `{"version": 2, "log": {"level": "off"}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := logDefaultEngine(t, path, true, io.Discard)
	if err := installer.wireLogDefault(); err != nil {
		t.Fatal(err)
	}
	block, present := readLogBlock(t, path)
	if !present || block["level"] != "off" {
		t.Fatalf("log block = %v present=%t, want level off kept", block, present)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != original {
		t.Fatalf("the file was rewritten:\n%s", raw)
	}
	if installer.report.Changed != 0 {
		t.Fatalf("report.Changed = %d, want 0", installer.report.Changed)
	}
}

// TestInstallLogDefaultLeavesAnAbsentFileAlone: config init owns creation, and
// a dry run plans the write without performing it.
func TestInstallLogDefaultLeavesAnAbsentFileAlone(t *testing.T) {
	t.Parallel()
	absent := filepath.Join(t.TempDir(), "pfm.config.json")
	var stdout strings.Builder
	installer := logDefaultEngine(t, absent, true, &stdout)
	if err := installer.wireLogDefault(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("install created %s (err %v); config init owns creation", absent, err)
	}
	if installer.report.Skipped != 1 || !strings.Contains(stdout.String(), "skip") {
		t.Fatalf("an absent file was not a skip row:\n%s", stdout.String())
	}

	path := filepath.Join(t.TempDir(), "pfm.config.json")
	if err := os.WriteFile(path, []byte(`{"version": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	preview := logDefaultEngine(t, path, false, io.Discard)
	if err := preview.wireLogDefault(); err != nil {
		t.Fatal(err)
	}
	if _, present := readLogBlock(t, path); present || preview.report.Changed != 1 {
		t.Fatalf("dry run wrote the file (present=%t changed=%d)", present, preview.report.Changed)
	}

	stdout.Reset()
	none := logDefaultEngine(t, "", true, &stdout)
	if err := none.wireLogDefault(); err != nil {
		t.Fatal(err)
	}
	if want := "  skip    log default: no pfm.config.json path known — nothing to write\n"; stdout.String() != want {
		t.Fatalf("stdout=%q, want %q", stdout.String(), want)
	}
}

// TestInstallRunsTheLogDefaultStep: the step is wired into install(), not a
// helper nothing calls.
func TestInstallRunsTheLogDefaultStep(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	path := filepath.Join(home, "pfm.config.json")
	writeFixture(t, path, `{"version": 2}`)
	if _, err := Run(context.Background(), Options{
		Mode:          ModeApply,
		Home:          home,
		ConfigDir:     canonical,
		MCPConfigPath: path,
		Runner:        &fakeRunner{},
		Stdout:        io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	if block, present := readLogBlock(t, path); !present || block["level"] != pfmconfig.InstallLogLevel {
		t.Fatalf("install() did not write the log default: %v present=%t", block, present)
	}
}

func TestInstallLogDefaultPlansOnSeed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	example := filepath.Join(t.TempDir(), "example.pfm.config.json")
	writeFixture(t, example, `{"version": 2}`)
	var output bytes.Buffer
	installer := logDefaultEngine(t, path, false, &output)
	installer.options.ConfigSeed = example
	if err := installer.wireLogDefault(); err != nil {
		t.Fatal(err)
	}
	block, err := json.Marshal(pfmconfig.InstallLogBlock())
	if err != nil {
		t.Fatal(err)
	}
	want := "  change  write log default " + string(block) + " into " + path + "\n"
	if output.String() != want || installer.report.Changed != 1 {
		t.Fatalf("output=%q changed=%d, want %q/1", output.String(), installer.report.Changed, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("preview target exists: %v", err)
	}
}

func TestInstallFreshHomeThenPlansConfigSeed(t *testing.T) {
	t.Setenv(paths.EnvConfig, "")
	if err := os.Unsetenv(paths.EnvConfig); err != nil {
		t.Fatal(err)
	}
	home, clone, _ := aliasInstallFixture(t)
	example := filepath.Join(clone, "example.pfm.config.json")
	writeFixture(t, example, `{"version":2,"theme":"seeded"}`)
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, SourceRepo: clone, Runner: &fakeRunner{}, Stdout: &output,
	}); err != nil {
		t.Fatalf("fresh install: %v\n%s", err, output.String())
	}
	if want := "  skip    log default: no pfm.config.json path known — nothing to write\n"; !strings.Contains(
		output.String(),
		want,
	) {
		t.Fatalf("fresh install missing log skip: %s", output.String())
	}
	if got, err := paths.ReadSourceRepoMarker(home); err != nil || got != clone {
		t.Fatalf("marker=%q err=%v, want %q", got, err, clone)
	}
	runtime := pfmconfig.Runtime{
		Config: pfmconfig.Config{Path: filepath.Join(clone, pfmconfig.FileName)},
		Paths:  paths.Values{Home: home},
	}
	if _, seeded, err := InstallConfig(runtime, &paths.MapEnv{}, ""); err != nil || seeded != example {
		t.Fatalf("seeded=%q err=%v, want %q", seeded, err, example)
	}
}
