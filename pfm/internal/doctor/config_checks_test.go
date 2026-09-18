package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/paths"
)

func TestDoctorAdvisesWhenConfiguredSeatsShareOAuthLogin(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	first := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".cc", "2")
	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".claude.json"), filepath.Join(second, ".claude.json")} {
		if err := os.WriteFile(
			path,
			[]byte(`{"oauthAccount":{"emailAddress":"fixture@example.invalid"}}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	machine := config.Config{Accounts: []config.Account{
		{ID: 1, ConfigDir: first, Implicit: true},
		{ID: 2, ConfigDir: second},
	}}
	runtime := config.Runtime{Paths: paths.Values{Home: home}, Config: machine}
	var output bytes.Buffer
	if warnings := printDuplicateSeatLogins(&output, runtime, &paths.MapEnv{}); warnings != 1 {
		t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
	}
	for _, want := range []string{"duplicate-seat-login", "fixture@example.invalid", "seats=1:", "2:", "share one OAuth usage cap"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, output.String())
		}
	}
}

func TestDoctorCountsUnreadableSeatRegistryAsWarning(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configDir := filepath.Join(home, ".cc", "2")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, ".claude.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := config.Runtime{
		Paths:  paths.Values{Home: home},
		Config: config.Config{Accounts: []config.Account{{ID: 2, ConfigDir: configDir}}},
	}
	var output bytes.Buffer
	if warnings := printDuplicateSeatLogins(&output, runtime, &paths.MapEnv{}); warnings != 1 {
		t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
	}
	if !strings.Contains(output.String(), "state=unreadable") {
		t.Fatalf("output missing unreadable state: %s", output.String())
	}
}

// TestDoctorReportsUnavailableSeatRegistryAsWarning is a REGRESSION test for
// the unreadable-registry branch: a registry path that cannot be read at all
// is state=unavailable, distinct from malformed JSON's state=unreadable.
// A directory gives os.ReadFile a deterministic EISDIR even in the root-owned
// fence, so this does not depend on permission bits being enforced by the
// test user.
func TestDoctorReportsUnavailableSeatRegistryAsWarning(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configDir := filepath.Join(home, ".cc", "2")
	if err := os.MkdirAll(filepath.Join(configDir, ".claude.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := config.Runtime{
		Paths:  paths.Values{Home: home},
		Config: config.Config{Accounts: []config.Account{{ID: 2, ConfigDir: configDir}}},
	}
	var output bytes.Buffer
	if warnings := printDuplicateSeatLogins(&output, runtime, &paths.MapEnv{}); warnings != 1 {
		t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
	}
	if !strings.Contains(output.String(), "state=unavailable") {
		t.Fatalf("output missing unavailable state: %s", output.String())
	}
}

// TestDoctorReportsUnavailableWhenGitIsMissing is a REGRESSION test for the
// pre-push dependency boundary. deps.RealRunner wraps a missing executable as
// exec.ErrNotFound; doctor must preserve that honest absence as unavailable,
// not render it as an unreadable probe failure.
func TestDoctorReportsUnavailableWhenGitIsMissing(t *testing.T) {
	runner := &deps.FakeRunner{}
	runner.Script(
		[]string{"git"},
		deps.RunResult{ExitCode: -1},
		&exec.Error{Name: "git", Err: exec.ErrNotFound},
	)
	gate := inspectPrePushGateWithRunner(context.Background(), runner)
	if gate.State != StateUnavailable {
		t.Fatalf(
			"inspectPrePushGateWithRunner() state=%q error=%v, want %q for missing git",
			gate.State,
			gate.Error,
			StateUnavailable,
		)
	}
}

func TestDoctorSortsDuplicateSeatLoginAdvisoriesByEmail(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	accounts := []config.Account{
		{ID: 1, Implicit: true},
		{ID: 2, ConfigDir: filepath.Join(home, ".cc", "2")},
		{ID: 3, ConfigDir: filepath.Join(home, ".cc", "3")},
		{ID: 4, ConfigDir: filepath.Join(home, ".cc", "4")},
	}
	for _, account := range accounts {
		directory := account.ConfigDir
		if account.Implicit {
			directory = home
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, email := range map[string]string{
		filepath.Join(home, ".claude.json"):             "z@example.invalid",
		filepath.Join(home, ".cc", "2", ".claude.json"): "a@example.invalid",
		filepath.Join(home, ".cc", "3", ".claude.json"): "a@example.invalid",
		filepath.Join(home, ".cc", "4", ".claude.json"): "z@example.invalid",
	} {
		if err := os.WriteFile(
			path,
			[]byte(fmt.Sprintf(`{"oauthAccount":{"emailAddress":%q}}`, email)),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	runtime := config.Runtime{Paths: paths.Values{Home: home}, Config: config.Config{Accounts: accounts}}
	var output bytes.Buffer
	if warnings := printDuplicateSeatLogins(&output, runtime, &paths.MapEnv{}); warnings != 2 {
		t.Fatalf("warnings=%d, want 2\n%s", warnings, output.String())
	}
	first := strings.Index(output.String(), "email=a@example.invalid")
	second := strings.Index(output.String(), "email=z@example.invalid")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("advisories are not sorted by email:\n%s", output.String())
	}
}

func TestDoctorWarnsOnRetiredHarvesterEnvAndPreSplitLayout(t *testing.T) {
	clearRetiredHarvesterEnv(t)
	t.Setenv("SEARXNG_URL", "http://127.0.0.1:8888")
	t.Setenv("HARVESTER_LOCAL_ROOTS", "/srv")
	runtime := config.Runtime{Config: config.Defaults(t.TempDir(), nil)}
	runtime.Config.Path = filepath.Join(t.TempDir(), config.LegacyFileName)
	runtime.Config.Harvester.Path = filepath.Join(filepath.Dir(runtime.Config.Path), config.HarvesterFileName)
	runtime.Config.Exists = true
	if err := os.WriteFile(runtime.Config.Path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if warnings := printHarvesterConfigDoctor(&stdout, runtime); warnings != 3 {
		t.Fatalf("warnings=%d, want 3\n%s", warnings, stdout.String())
	}
	for _, want := range []string{"layout=pre-split", "retired_env=SEARXNG_URL", "search.searxngURL", "retired_env=HARVESTER_LOCAL_ROOTS", "never honored"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("doctor output lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestDoctorExternalGatewayNeverRendersAsAbsence(t *testing.T) {
	cases := []struct {
		name, reported, want        string
		enabled, external, warnings int
	}{
		{name: "off", enabled: 1},
		{
			name:     "harvester disabled",
			external: 1,
			reported: "disabled",
			warnings: 1,
			want:     "harvester.enabled is false",
		},
		{name: "old daemon", enabled: 1, external: 1, warnings: 1, want: "not reported"},
		{
			name:     "failed",
			enabled:  1,
			external: 1,
			reported: "failed: listen tcp 127.0.0.1:18378: bind",
			warnings: 1,
			want:     "failed: listen",
		},
		{name: "listening", enabled: 1, external: 1, reported: "listening on 127.0.0.1:18378", want: "listening on"},
	}
	for _, tc := range cases {
		harvester := config.DefaultHarvester()
		harvester.Enabled, harvester.External.Enabled = tc.enabled == 1, tc.external == 1
		var stdout bytes.Buffer
		warnings := printHarvesterExternalDoctor(&stdout, harvester, tc.reported)
		if warnings != tc.warnings {
			t.Errorf("%s: warnings=%d, want %d (%q)", tc.name, warnings, tc.warnings, stdout.String())
		}
		if tc.want == "" && stdout.Len() != 0 || tc.want != "" && !strings.Contains(stdout.String(), tc.want) {
			t.Errorf("%s: output %q, want %q", tc.name, stdout.String(), tc.want)
		}
	}
}
