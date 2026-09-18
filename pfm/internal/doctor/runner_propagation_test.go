package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/harvestpy"
)

type doctorTestWriteCloser struct{ io.Writer }

func (doctorTestWriteCloser) Close() error { return nil }

// TestPrintHarvestPythonDoctorBindsTheProductionRunner proves that the
// production pinned doctor uses the runner supplied by its caller. The
// environment is deliberately only structurally readable: the first checks
// must still reach FakeRunner before later health checks report their fixture
// gaps.
func TestPrintHarvestPythonDoctorBindsTheProductionRunner(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".local", "state", "pfm", "harvest-python")
	platform := harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"}
	versioned := filepath.Join(root, "env", platform.String(), "fixture")
	if err := os.MkdirAll(versioned, 0o700); err != nil {
		t.Fatal(err)
	}
	marker, err := json.Marshal(harvestpy.EnvironmentDigest{Schema: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versioned, "environment.json"), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(versioned), harvestpy.RuntimeRoot(root, platform)); err != nil {
		t.Fatal(err)
	}

	fake := &deps.FakeRunner{}
	var output strings.Builder
	_ = printHarvestPythonDoctorWithRunner(
		context.Background(),
		&output,
		home,
		platform,
		pinnedHarvestDoctor{},
		false,
		fake,
	)
	if calls := fake.Calls(); len(calls) == 0 {
		t.Fatalf("production pinned doctor bypassed injected runner; calls=%v output=%s", calls, output.String())
	}
}

// TestAppendHarvestBrowserDoctorRowBindsTheProductionSmokeRunner proves that
// the default browser smoke starts through the supplied Runner while keeping
// doctorBrowserSmoke's legacy override available to existing tests.
func TestAppendHarvestBrowserDoctorRowBindsTheProductionSmokeRunner(t *testing.T) {
	root := t.TempDir()
	platform := harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"}
	digest := doctorHarvestDigest()
	digest.Digest = "browser-fixture"
	interpreter := writeProvisionedBrowserEnv(t, root, platform, digest)
	chrome := filepath.Join(root, "chrome")
	if err := os.WriteFile(chrome, []byte("chrome"), 0o700); err != nil {
		t.Fatal(err)
	}
	stdout := io.NopCloser(strings.NewReader(
		`{"ok":true,"patchright":true,"chrome_path":"` + chrome + `"}` + "\n",
	))
	fake := &deps.FakeRunner{}
	fake.ScriptInteractive([]string{interpreter}, deps.InteractiveScript{
		Pid:    4243,
		Stdin:  doctorTestWriteCloser{Writer: io.Discard},
		Stdout: stdout,
	})

	var output bytes.Buffer
	warnings := appendHarvestBrowserDoctorRowWithRunner(
		context.Background(),
		&output,
		root,
		platform,
		0,
		true,
		fake,
		func() string { return "" },
	)
	if warnings != 0 {
		t.Fatalf("default browser smoke warnings=%d output=%s", warnings, output.String())
	}
	starts := fake.Starts()
	if len(starts) != 1 || len(starts[0].Argv) != 2 || starts[0].Argv[0] != interpreter {
		t.Fatalf(
			"default browser smoke did not start through injected runner: starts=%v output=%s",
			starts,
			output.String(),
		)
	}
}
