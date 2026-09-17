package doctor

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/gather"
)

func TestDoctorRecognizesThenFailedAsSatelliteMetadata(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	for name, content := range map[string]string{
		"cc-1-2-3": "/transcripts/live.jsonl", "cc-1-2-3.then-failed": "prompt preserved for retry",
		"reload-cc-1-2-3.log": "completed fleet reload", "reload-vsct.log": "completed bunker reload",
		"reload-probe.log": "not a fleet reload", ".open.uuid": "lock metadata",
		"cc-1-2-3.not-metadata": "invalid",
	} {
		if err := os.WriteFile(filepath.Join(sidDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 7 || invalid != 2 {
		t.Fatalf("crumbHealth() entries=%d invalid=%d", entries, invalid)
	}
}

func TestDoctorIgnoresBunkerCrumbsAndOpenLockDirectories(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	for name, content := range map[string]string{
		"cc-1-2-3": "/transcripts/live.jsonl", "vsct": "/transcripts/bunker.jsonl",
		"vsct.%187": "/transcripts/bunker.jsonl", "rotten": "neither a crumb nor sid metadata",
	} {
		if err := os.WriteFile(filepath.Join(sidDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{".open.88888888-8888-4888-8888-888888888888", "rotten-directory"} {
		if err := os.Mkdir(filepath.Join(sidDir, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 6 || invalid != 2 {
		t.Fatalf("crumbHealth() entries=%d invalid=%d, want 6 and 2", entries, invalid)
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

func TestTmuxTitlesDoctorReportsStringDriftAsDivergent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root, socket := t.TempDir(), "titles-doctor"
	environment := append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	start := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "fixture", "sleep", "120")
	start.Env = environment
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start tmux fixture: %v: %s", err, output)
	}
	t.Cleanup(func() {
		command := exec.Command("tmux", "-L", socket, "kill-server")
		command.Env = environment
		_ = command.Run()
	})
	for option, value := range map[string]string{"set-titles": "on", "set-titles-string": "custom-host-title"} {
		command := exec.Command("tmux", "-L", socket, "set-option", "-g", option, value)
		command.Env = environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", option, err, output)
		}
	}
	state, detail := readTmuxTitlesState(
		context.Background(),
		gather.TmuxProbe{Binary: "tmux", TmuxTmpDir: root},
		socket,
		true,
	)
	if state != titlesDivergent || !strings.Contains(detail, "custom-host-title") ||
		!strings.Contains(detail, config.TmuxTitlesString) {
		t.Fatalf("state=%q detail=%q, want divergent with actual and expected strings", state, detail)
	}
}

func TestDoctorConfigRenderingShowsEffectiveValuesAndSources(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "config-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "doctor-machine.json")
	if err := os.WriteFile(
		path,
		[]byte(
			`{"version":1,"accounts":[{"id":7,"configDir":"~/account-seven"}],"claude":{"permissionMode":"prompt","binary":"claude-fixture"},"codex":{"yolo":false,"binary":"codex-fixture"},"mcp":{"servers":{"chat":{"enabled":true}}}}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	PrintConfig(&stdout, config.Runtime{Config: loaded})
	for _, want := range []string{"config version=2 effective (input=1 file)", "config accounts=7:" + filepath.Join(home, "account-seven"), "mcp.servers.chat.enabled=true (file)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("PrintConfig() = %q, missing %q", stdout.String(), want)
		}
	}
}
