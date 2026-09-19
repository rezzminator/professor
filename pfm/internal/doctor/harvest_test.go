package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/harvestpy"
)

// writeProvisionedBrowserEnv materialises an environment directory whose
// on-disk browser.py matches digest.SourceSHA256 — everything short of a
// live smoke, which tests inject through doctorBrowserSmoke.
func writeProvisionedBrowserEnv(
	t *testing.T,
	root string,
	platform harvestpy.Platform,
	digest harvestpy.EnvironmentDigest,
) string {
	t.Helper()
	sum := sha256.Sum256(harvestpy.BrowserWorkerSource())
	digest.SourceSHA256 = hex.EncodeToString(sum[:])
	lock := sha256.Sum256(harvestpy.BrowserLockMetadata())
	digest.LockSHA256 = hex.EncodeToString(lock[:])
	env := harvestpy.BrowserRuntimeRoot(root, platform)
	if err := os.MkdirAll(filepath.Join(env, "project", ".venv", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	interpreter := filepath.Join(env, "project", ".venv", "bin", "python")
	if err := os.WriteFile(interpreter, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(env, "project", "browser.py"),
		harvestpy.BrowserWorkerSource(),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env, "environment.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	return interpreter
}

type harvestDoctorFake struct {
	digest   harvestpy.EnvironmentDigest
	inspect  error
	check    harvestpy.CheckReport
	checkErr error
}

func (fake harvestDoctorFake) Inspect(_ string, _ harvestpy.Platform) (harvestpy.EnvironmentDigest, error) {
	return fake.digest, fake.inspect
}

func (fake harvestDoctorFake) Check(
	_ context.Context,
	_ string,
	_ harvestpy.Platform,
) (harvestpy.CheckReport, error) {
	return fake.check, fake.checkErr
}

func doctorHarvestDigest() harvestpy.EnvironmentDigest {
	return harvestpy.EnvironmentDigest{
		Schema:          1,
		Target:          "linux-amd64",
		Python:          "3.11.15+20260610",
		LockSHA256:      "lock-digest",
		InventorySHA256: "inventory-digest",
		InventoryCount:  187,
		State:           "ready",
	}
}

func TestDoctorHarvestReportsPinnedInterpreterLockInventoryAndLiveSmokeHealthy(t *testing.T) {
	digest := doctorHarvestDigest()
	fake := harvestDoctorFake{
		digest: digest,
		check: harvestpy.CheckReport{
			Healthy: true,
			Digest:  digest,
			Checks: map[string]harvestpy.CheckStatus{
				"interpreter":           {OK: true},
				"lock_completeness":     {OK: true},
				"dependency_check":      {OK: true},
				"inventory":             {OK: true},
				"live_smoke":            {OK: true},
				"live_smoke_conversion": {OK: true},
			},
		},
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "state", "pfm", "harvest-python"), 0o700); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	warnings := printHarvestPythonDoctor(
		context.Background(),
		&output,
		home,
		harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
		fake,
		false,
	)
	if warnings != 0 {
		t.Fatalf("healthy doctor warnings=%d, want 0\n%s", warnings, output.String())
	}
	for _, want := range []string{
		"doctor: harvestpy interpreter=(file)",
		"3.11.15+20260610",
		"doctor: harvestpy lock=(file) complete",
		"lock-digest",
		"doctor: harvestpy inventory=(file) complete count=187",
		"inventory-digest",
		"doctor: harvestpy live_smoke=(file) healthy",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("healthy doctor output missing %q:\n%s", want, output.String())
		}
	}
}

func TestDoctorHarvestDistinguishesBrokenEnvironmentAndSmoke(t *testing.T) {
	digest := doctorHarvestDigest()
	fake := harvestDoctorFake{
		digest: digest,
		check: harvestpy.CheckReport{
			Healthy: false,
			Digest:  digest,
			Checks: map[string]harvestpy.CheckStatus{
				"lock_hash":  {Error: "uv.lock does not match its recorded digest"},
				"live_smoke": {Error: "harvestpy smoke subprocess: interpreter missing"},
			},
		},
		checkErr: errors.New("harvestpy environment check failed"),
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "state", "pfm", "harvest-python"), 0o700); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	warnings := printHarvestPythonDoctor(
		context.Background(),
		&output,
		home,
		harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
		fake,
		false,
	)
	if warnings == 0 {
		t.Fatalf("broken doctor warnings=%d, want nonzero\n%s", warnings, output.String())
	}
	text := output.String()
	// digest.State is "ready" here (doctorHarvestDigest): the lock check
	// failed against a provision that DID finish, so the failure word is
	// "broken" — "incomplete" is reserved for digest.State == "incomplete".
	for _, want := range []string{
		"doctor: harvestpy lock=(file) broken error=uv.lock does not match its recorded digest",
		"doctor: harvestpy live_smoke=(file) broken error=harvestpy smoke subprocess: interpreter missing",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("broken doctor output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "live_smoke=(file) healthy") || strings.Contains(text, "lock=(file) complete") {
		t.Fatalf("broken doctor reused healthy rendering:\n%s", text)
	}
	if strings.Contains(text, "lock=(file) incomplete") {
		t.Fatalf("broken doctor rendered the interrupted-provision word for a State==ready digest:\n%s", text)
	}
}

func TestDoctorHarvestLockIncompleteReflectsInterruptedProvisionState(t *testing.T) {
	digest := doctorHarvestDigest()
	digest.State = "incomplete"
	digest.LockSHA256 = ""
	fake := harvestDoctorFake{
		digest: digest,
		check: harvestpy.CheckReport{
			Healthy: false,
			Digest:  digest,
			Checks: map[string]harvestpy.CheckStatus{
				"lock_hash": {Error: "provision interrupted: lock file missing"},
			},
		},
		checkErr: errors.New("harvestpy environment check failed"),
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "state", "pfm", "harvest-python"), 0o700); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	warnings := printHarvestPythonDoctor(
		context.Background(),
		&output,
		home,
		harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
		fake,
		false,
	)
	if warnings == 0 {
		t.Fatalf("interrupted-provision doctor warnings=%d, want nonzero\n%s", warnings, output.String())
	}
	text := output.String()
	if want := "doctor: harvestpy lock=(file) incomplete error=provision interrupted: lock file missing"; !strings.Contains(
		text,
		want,
	) {
		t.Fatalf("interrupted-provision doctor output missing %q:\n%s", want, text)
	}
	if strings.Contains(text, "lock=(file) broken") {
		t.Fatalf(
			"interrupted-provision doctor rendered the finished-provision word for a State==incomplete digest:\n%s",
			text,
		)
	}
}

// TestDoctorHarvestUnnamedFailedCheckIsNotHiddenAsClean pins L3-F1: the doctor
// section printed only interpreter, lock, inventory, and live-smoke rows,
// but harvestpy.CheckConversionEnvironment computes 15 named checks and sets
// report.Healthy=false when ANY of them fails — before this fix a check like
// digest_integrity could fail while every printed row stayed healthy, and the
// section reported zero warnings: a coincidence detector.
func TestDoctorHarvestUnnamedFailedCheckIsNotHiddenAsClean(t *testing.T) {
	digest := doctorHarvestDigest()
	fake := harvestDoctorFake{
		digest: digest,
		check: harvestpy.CheckReport{
			Healthy: false,
			Digest:  digest,
			Checks: map[string]harvestpy.CheckStatus{
				"interpreter":           {OK: true},
				"lock_hash":             {OK: true},
				"lock_completeness":     {OK: true},
				"live_smoke":            {OK: true},
				"live_smoke_conversion": {OK: true},
				// Not printed by any dedicated row above — the check the
				// coincidence-detector hid before this fix.
				"digest_integrity": {Error: "environment digest does not match its own recorded state"},
			},
		},
		checkErr: errors.New("harvestpy environment check failed"),
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "state", "pfm", "harvest-python"), 0o700); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	warnings := printHarvestPythonDoctor(
		context.Background(),
		&output,
		home,
		harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
		fake,
		false,
	)
	if warnings == 0 {
		t.Fatalf(
			"digest_integrity failed but no printed row named it — the section stayed clean:\n%s",
			output.String(),
		)
	}
	if !strings.Contains(output.String(), "digest_integrity") ||
		!strings.Contains(output.String(), "environment digest does not match its own recorded state") {
		t.Fatalf("output does not name the failed check:\n%s", output.String())
	}
}

func TestDoctorHarvestMissingRootIsSkipped(t *testing.T) {
	var output strings.Builder
	warnings := printHarvestPythonDoctor(
		context.Background(),
		&output,
		t.TempDir(),
		harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
		harvestDoctorFake{
			inspect:  errors.New("harvestpy root is absent"),
			checkErr: errors.New("harvestpy root is absent"),
		},
		false,
	)
	if warnings != 0 {
		t.Fatalf("missing harvest root warnings=%d, want 0\n%s", warnings, output.String())
	}
	// The skipped conversion env is followed by the informational gate-off
	// browser row (absence of an opt-in environment is not a defect).
	// S7: even disabled, never-provisioned is NAMED, not folded into a bare "disabled".
	if got, want := output.String(), "doctor: harvestpy skipped\ndoctor: harvestpy_browser env=NOT_PROVISIONED disabled gate=fetch.browser\n"; got != want {
		t.Fatalf("missing harvest root output=%q, want %q", got, want)
	}
}

func TestDoctorHarvestUnreadableRootIsNotSkipped(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".local")
	if err := os.MkdirAll(local, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("state", filepath.Join(local, "state")); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	warnings := printHarvestPythonDoctor(
		context.Background(),
		&output,
		home,
		harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
		harvestDoctorFake{
			inspect:  os.ErrPermission,
			checkErr: os.ErrPermission,
		},
		false,
	)
	if warnings == 0 {
		t.Fatalf("unreadable harvest root warnings=0, want a visible probe failure\n%s", output.String())
	}
	if strings.Contains(output.String(), "doctor: harvestpy skipped") {
		t.Fatalf("unreadable harvest root rendered as skipped:\n%s", output.String())
	}
}

// TestDoctorHarvestBrowserRowDistinguishesItsBrokenStates pins the browser
// row: gate off is informational absence; gate on distinguishes NOT
// provisioned, probe-failed, source-mismatch, broken-smoke, and
// chrome-missing — and the healthy verdict comes from LIVE smoke.
func TestDoctorHarvestBrowserRowDistinguishesItsBrokenStates(t *testing.T) {
	platform := harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"}
	ctx := context.Background()
	newRoot := func() string { return filepath.Join(t.TempDir(), "harvest-python") } // fresh per subtest — no fixture bleed

	restoreSmoke := func(impl func(context.Context, string, string) (map[string]any, error)) {
		previous := doctorBrowserSmoke
		doctorBrowserSmoke = impl
		t.Cleanup(func() { doctorBrowserSmoke = previous })
	}

	root := newRoot()
	t.Run("gate off is informational and warns about nothing", func(t *testing.T) {
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, false)
		if warnings != 0 || !strings.Contains(output.String(), "NOT_PROVISIONED") ||
			!strings.Contains(output.String(), "disabled") {
			t.Fatalf("gate-off row=%q warnings=%d", output.String(), warnings)
		}
	})

	root = newRoot()
	t.Run("gate off with a corrupt record still NAMES the corruption", func(t *testing.T) {
		env := harvestpy.BrowserRuntimeRoot(root, platform)
		if err := os.MkdirAll(env, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(env, "environment.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, false)
		if warnings != 0 || !strings.Contains(output.String(), "CORRUPT_RECORD") {
			t.Fatalf(
				"corrupt gate-off row=%q warnings=%d — a broken state rendered as plain disabled",
				output.String(),
				warnings,
			)
		}
	})

	root = newRoot()
	t.Run("gate on without an environment reports NOT provisioned", func(t *testing.T) {
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, true)
		if warnings != 1 || !strings.Contains(output.String(), "NOT_PROVISIONED") {
			t.Fatalf("unprovisioned row=%q warnings=%d", output.String(), warnings)
		}
	})

	root = newRoot()
	t.Run("gate on with a corrupt record reports probe failed", func(t *testing.T) {
		env := harvestpy.BrowserRuntimeRoot(root, platform)
		if err := os.MkdirAll(env, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(env, "environment.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, true)
		if warnings != 1 || !strings.Contains(output.String(), "PROBE_FAILED") {
			t.Fatalf("corrupt-record row=%q warnings=%d", output.String(), warnings)
		}
	})

	root = newRoot()
	t.Run("gate on without a pinned source refuses to vouch for the guard", func(t *testing.T) {
		digest := doctorHarvestDigest()
		digest.Digest = "abcdef1234567890"
		digest.Imports = map[string]any{"patchright": true}
		writeProvisionedBrowserEnv(t, root, platform, digest)
		// strip the pin the helper wrote — simulating a pre-pinning record
		digest.SourceSHA256 = ""
		body, err := json.Marshal(digest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(harvestpy.BrowserRuntimeRoot(root, platform), "environment.json"),
			body,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, true)
		if warnings != 1 || !strings.Contains(output.String(), "SOURCE_UNPINNED") {
			t.Fatalf("unpinned-source row=%q warnings=%d", output.String(), warnings)
		}
	})

	root = newRoot()
	t.Run("gate on with a tampered worker reports SOURCE MISMATCH", func(t *testing.T) {
		digest := doctorHarvestDigest()
		digest.Digest = "abcdef1234567890"
		digest.Imports = map[string]any{"patchright": true}
		writeProvisionedBrowserEnv(t, root, platform, digest)
		script := filepath.Join(harvestpy.BrowserRuntimeRoot(root, platform), "project", "browser.py")
		if err := os.WriteFile(script, []byte("# tampered — route guard removed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, true)
		if warnings != 1 || !strings.Contains(output.String(), "SOURCE_MISMATCH") {
			t.Fatalf("tampered-worker row=%q warnings=%d", output.String(), warnings)
		}
	})

	root = newRoot()
	t.Run("gate on with a worker provisioned by an older pfm reports SOURCE STALE", func(t *testing.T) {
		liveChrome := filepath.Join(t.TempDir(), "live-chrome")
		if err := os.WriteFile(liveChrome, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		restoreSmoke(func(_ context.Context, _, _ string) (map[string]any, error) {
			return map[string]any{"ok": true, "patchright": true, "chrome_path": liveChrome}, nil
		})
		digest := doctorHarvestDigest()
		digest.Digest = "abcdef1234567890"
		writeProvisionedBrowserEnv(t, root, platform, digest)
		// Record and disk agree with EACH OTHER (no tamper) but not with
		// the worker this binary embeds — the state an upgrade leaves.
		older := []byte("# browser worker from an older pfm\n")
		envDir := harvestpy.BrowserRuntimeRoot(root, platform)
		if err := os.WriteFile(filepath.Join(envDir, "project", "browser.py"), older, 0o600); err != nil {
			t.Fatal(err)
		}
		record, err := harvestpy.InspectBrowser(root, platform)
		if err != nil {
			t.Fatal(err)
		}
		olderSum := sha256.Sum256(older)
		record.SourceSHA256 = hex.EncodeToString(olderSum[:])
		body, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(envDir, "environment.json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, true)
		if !strings.Contains(output.String(), "SOURCE_STALE") || strings.Contains(output.String(), "source_hash=ok") {
			t.Fatalf("an older pfm's worker read as current: %q", output.String())
		}
		// Self-healing on the next browser fetch: named, never an update-blocking warning.
		if warnings != 0 {
			t.Fatalf("stale-but-self-healing worker counted %d warning(s), want 0: %q", warnings, output.String())
		}
	})

	root = newRoot()
	t.Run("gate on with failing live smoke reports BROKEN SMOKE", func(t *testing.T) {
		digest := doctorHarvestDigest()
		digest.Digest = "abcdef1234567890"
		digest.Imports = map[string]any{"patchright": true}
		writeProvisionedBrowserEnv(t, root, platform, digest)
		restoreSmoke(func(context.Context, string, string) (map[string]any, error) {
			return nil, errors.New("smoke subprocess died")
		})
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, true)
		if warnings != 1 || !strings.Contains(output.String(), "BROKEN_SMOKE") {
			t.Fatalf("broken-smoke row=%q warnings=%d", output.String(), warnings)
		}
	})

	root = newRoot()
	t.Run("gate on with a ready environment but no Chrome reports the missing binary", func(t *testing.T) {
		goneChrome := filepath.Join(t.TempDir(), "uninstalled-chrome")
		previous := doctorChromeResolver
		doctorChromeResolver = func() string { return "" } // simulate a Chrome-less host
		t.Cleanup(func() { doctorChromeResolver = previous })
		restoreSmoke(func(_ context.Context, _, _ string) (map[string]any, error) {
			return map[string]any{"ok": true, "patchright": true, "chrome_path": goneChrome}, nil
		})
		digest := doctorHarvestDigest()
		digest.Digest = "abcdef1234567890"
		digest.Imports = map[string]any{"patchright": true, "chrome_path": goneChrome}
		writeProvisionedBrowserEnv(t, root, platform, digest)
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, true)
		if warnings != 1 || !strings.Contains(output.String(), "chrome=MISSING") {
			t.Fatalf("missing-chrome row=%q warnings=%d", output.String(), warnings)
		}
	})

	root = newRoot()
	t.Run("the healthy verdict is earned by LIVE smoke, not the record", func(t *testing.T) {
		liveChrome := filepath.Join(t.TempDir(), "live-chrome")
		if err := os.WriteFile(liveChrome, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		restoreSmoke(func(_ context.Context, _, _ string) (map[string]any, error) {
			return map[string]any{"ok": true, "patchright": true, "chrome_path": liveChrome}, nil
		})
		digest := doctorHarvestDigest()
		digest.Digest = "abcdef1234567890"
		digest.Imports = map[string]any{"patchright": false, "chrome_path": ""} // stale record must not decide
		writeProvisionedBrowserEnv(t, root, platform, digest)
		var output strings.Builder
		warnings := appendHarvestBrowserDoctorRow(ctx, &output, root, platform, 0, true)
		if warnings != 0 || !strings.Contains(output.String(), "healthy") ||
			!strings.Contains(output.String(), "live smoke") {
			t.Fatalf("healthy row=%q warnings=%d — a stale record decided the verdict", output.String(), warnings)
		}
	})
}

// TestDoctorBrowserRowPrintsEvenWhenHarvestpyIsSkipped pins the fence-found
// gap: a host with NO harvest-python root takes the early "harvestpy skipped"
// return — the browser row must still print there, or a gated-on unprovisioned
// rung renders as silence.
func TestDoctorBrowserRowPrintsEvenWhenHarvestpyIsSkipped(t *testing.T) {
	var output strings.Builder
	warnings := printHarvestPythonDoctor(
		context.Background(),
		&output,
		t.TempDir(),
		harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
		harvestDoctorFake{},
		true,
	)
	if !strings.Contains(output.String(), "harvestpy_browser") ||
		!strings.Contains(output.String(), "NOT_PROVISIONED") {
		t.Fatalf("skipped-harvestpy host lost the browser row: %q", output.String())
	}
	if warnings != 1 {
		t.Fatalf("unprovisioned browser env with the gate on must warn, got %d", warnings)
	}
}
