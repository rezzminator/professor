package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/harvestpy"
)

// harvestProvisionerFake is deliberately small: installer tests must be able
// to exercise host wiring without ever downloading the pinned multi-GB lock.
type harvestProvisionerFake struct {
	plan           harvestpy.InstallPlan
	planErr        error
	check          harvestpy.CheckReport
	checkErr       error
	provision      harvestpy.ProvisionResult
	provisionErr   error
	planCalls      int
	checkCalls     int
	provisionCalls int
	lastOptions    harvestpy.ProvisionOptions
	lastCheckRoot  string
	lastCheckPlat  harvestpy.Platform
}

func (fake *harvestProvisionerFake) Plan(_ harvestpy.Platform) (harvestpy.InstallPlan, error) {
	fake.planCalls++
	return fake.plan, fake.planErr
}

func (fake *harvestProvisionerFake) Check(
	ctx context.Context,
	root string,
	platform harvestpy.Platform,
) (harvestpy.CheckReport, error) {
	fake.checkCalls++
	fake.lastCheckRoot = root
	fake.lastCheckPlat = platform
	return fake.check, fake.checkErr
}

func (fake *harvestProvisionerFake) Provision(
	ctx context.Context,
	options harvestpy.ProvisionOptions,
) (harvestpy.ProvisionResult, error) {
	fake.provisionCalls++
	fake.lastOptions = options
	return fake.provision, fake.provisionErr
}

func linuxHarvestPlan() harvestpy.InstallPlan {
	return harvestpy.InstallPlan{
		Platform:              "linux-amd64",
		PythonVersion:         "3.11.15+20260610",
		UVVersion:             "0.11.32",
		PythonDownloadBytes:   1234,
		UVDownloadBytes:       5678,
		PackageDownloadBytes:  3106174573,
		PackageDownloadStatus: "cold-cache-download",
		EnvironmentBytes:      5786939761,
	}
}

func TestInstallHarvestDryRunPlansOnlyAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	fake := &harvestProvisionerFake{plan: linuxHarvestPlan()}
	var output strings.Builder
	_, err := Run(context.Background(), Options{
		Mode:               ModeDryRun,
		Home:               home,
		Stdout:             &output,
		Runner:             &fakeRunner{},
		ProvisionHarvest:   true,
		HarvestProvisioner: fake,
		HarvestPlatform:    harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
		HarvestOffline:     true,
	})
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, output.String())
	}
	if fake.planCalls != 1 || fake.provisionCalls != 0 || fake.checkCalls != 0 {
		t.Fatalf(
			"dry-run calls plan=%d check=%d provision=%d, want 1/0/0",
			fake.planCalls,
			fake.checkCalls,
			fake.provisionCalls,
		)
	}
	if entries, readErr := os.ReadDir(home); readErr != nil || len(entries) != 0 {
		t.Fatalf("dry-run wrote files: entries=%v err=%v", entries, readErr)
	}
	text := output.String()
	for _, want := range []string{
		"harvestpy dry-run: Plan only; writes=0 network=0",
		"Python 3.11.15+20260610",
		"uv_download_bytes=5678",
		"python_download_bytes=1234",
		"package_download_bytes=3106174573",
		"environment_bytes=5786939761",
		"offline=true",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestInstallHarvestSkipReportsExactStateForApplyAndPreview(t *testing.T) {
	for _, test := range []struct {
		name string
		mode Mode
		want string
	}{
		{name: "preview", mode: ModeDryRun, want: "harvestpy: would skip (blocked, not attempted)"},
		{name: "apply", mode: ModeApply, want: "harvestpy: skipped (blocked, not attempted)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			fake := &harvestProvisionerFake{plan: linuxHarvestPlan()}
			var output strings.Builder
			if _, err := Run(context.Background(), Options{
				Mode:               test.mode,
				Home:               home,
				Stdout:             &output,
				Runner:             &fakeRunner{},
				ProvisionHarvest:   false,
				HarvestProvisioner: fake,
			}); err != nil {
				t.Fatalf("skip %s: %v\n%s", test.name, err, output.String())
			}
			if strings.Count(output.String(), test.want) != 1 {
				t.Fatalf("skip %s output=%q, want one %q", test.name, output.String(), test.want)
			}
			if fake.planCalls != 0 || fake.checkCalls != 0 || fake.provisionCalls != 0 {
				t.Fatalf(
					"skip %s called provisioner plan=%d check=%d provision=%d",
					test.name,
					fake.planCalls,
					fake.checkCalls,
					fake.provisionCalls,
				)
			}
		})
	}
}

func TestInstallHarvestApplyUsesCentralRuntimeRootAndCheckFastPath(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".local", "state", "pfm", "harvest-python")
	fake := &harvestProvisionerFake{
		plan:     linuxHarvestPlan(),
		checkErr: errors.New("environment missing"),
	}
	var output strings.Builder
	if _, err := Run(context.Background(), Options{
		Mode:               ModeApply,
		Home:               home,
		Stdout:             &output,
		Runner:             &fakeRunner{},
		ProvisionHarvest:   true,
		HarvestProvisioner: fake,
		HarvestPlatform:    harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
	}); err != nil {
		t.Fatalf("apply: %v\n%s", err, output.String())
	}
	if fake.planCalls != 2 || fake.checkCalls != 1 || fake.provisionCalls != 1 {
		t.Fatalf(
			"repair calls plan=%d check=%d provision=%d, want 2/1/1 (preflight + apply plans)",
			fake.planCalls,
			fake.checkCalls,
			fake.provisionCalls,
		)
	}
	if fake.lastOptions.Root != root || fake.lastOptions.Cache != filepath.Join(root, "cache") {
		t.Fatalf(
			"provision root/cache = %q/%q, want %q/%q",
			fake.lastOptions.Root,
			fake.lastOptions.Cache,
			root,
			filepath.Join(root, "cache"),
		)
	}

	// A subsequent apply with a healthy check must not reprovision. This is the
	// installer-level fast path; the real worker's Check performs the live
	// no-download smoke and lock/inventory verification.
	fast := &harvestProvisionerFake{
		plan:  linuxHarvestPlan(),
		check: harvestpy.CheckReport{Healthy: true},
	}
	if _, err := Run(context.Background(), Options{
		Mode:               ModeApply,
		Home:               home,
		Stdout:             &output,
		Runner:             &fakeRunner{},
		ProvisionHarvest:   true,
		HarvestProvisioner: fast,
		HarvestPlatform:    harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
	}); err != nil {
		t.Fatalf("healthy fast-path apply: %v\n%s", err, output.String())
	}
	if fast.planCalls != 2 || fast.checkCalls != 1 || fast.provisionCalls != 0 {
		t.Fatalf(
			"healthy fast-path calls plan=%d check=%d provision=%d, want 2/1/0 (preflight + apply plans)",
			fast.planCalls,
			fast.checkCalls,
			fast.provisionCalls,
		)
	}
	if fast.lastCheckRoot != root || fast.lastCheckPlat != (harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"}) {
		t.Fatalf(
			"healthy fast-path check root/platform=%q/%s, want %q/linux-amd64",
			fast.lastCheckRoot,
			fast.lastCheckPlat,
			root,
		)
	}
}

func TestInstallHarvestApplyProvisionFailureIsActionableOffline(t *testing.T) {
	home := t.TempDir()
	fake := &harvestProvisionerFake{
		plan:         linuxHarvestPlan(),
		checkErr:     errors.New("environment missing"),
		provisionErr: harvestpy.ErrOfflineUnavailable,
	}
	var output strings.Builder
	_, err := Run(context.Background(), Options{
		Mode:               ModeApply,
		Home:               home,
		Stdout:             &output,
		Runner:             &fakeRunner{},
		ProvisionHarvest:   true,
		HarvestProvisioner: fake,
		HarvestPlatform:    harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
		HarvestOffline:     true,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "offline") {
		t.Fatalf("apply error=%v, want actionable offline error\n%s", err, output.String())
	}
	if !strings.Contains(strings.ToLower(output.String()), "offline") || !strings.Contains(output.String(), "cached") {
		t.Fatalf("offline output is not actionable:\n%s", output.String())
	}
}

func TestInstallHarvestDarwinAMD64BlockedByExactLock(t *testing.T) {
	home := t.TempDir()
	plan := linuxHarvestPlan()
	plan.Platform = "darwin-amd64"
	plan.PackageDownloadBytes = -1
	plan.PackageDownloadStatus = "blocked-exact-lock"
	plan.PackageBlockers = []string{
		"torch==2.12.1 has no compatible darwin-amd64 wheel or source",
		"torchvision==0.27.1 has no compatible darwin-amd64 wheel or source",
		"onnxruntime==1.27.0 has no compatible darwin-amd64 wheel or source",
	}
	fake := &harvestProvisionerFake{plan: plan}
	var output strings.Builder
	_, err := Run(context.Background(), Options{
		Mode:               ModeDryRun,
		Home:               home,
		Stdout:             &output,
		Runner:             &fakeRunner{},
		ProvisionHarvest:   true,
		HarvestProvisioner: fake,
		HarvestPlatform:    harvestpy.Platform{GOOS: "darwin", GOARCH: "amd64"},
	})
	if err == nil || !strings.Contains(err.Error(), "blocked-exact-lock") {
		t.Fatalf("darwin-amd64 error=%v, want exact-lock blocker\n%s", err, output.String())
	}
	if fake.provisionCalls != 0 {
		t.Fatalf("blocked target attempted provision %d times", fake.provisionCalls)
	}
	for _, want := range append([]string{"darwin-amd64", "blocked-exact-lock"}, plan.PackageBlockers...) {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("blocked output missing %q:\n%s", want, output.String())
		}
	}
}

func TestUninstallHarvestRemovesOnlyManagedRuntimeAndCache(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".local", "state", "pfm", "harvest-python")
	if err := os.MkdirAll(filepath.Join(root, "env", "linux-amd64", "current"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(home, ".local", "state", "pfm", "other-state")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &harvestProvisionerFake{plan: linuxHarvestPlan()}
	var output strings.Builder
	if _, err := Run(context.Background(), Options{
		Mode:               ModeUninstall,
		Home:               home,
		Stdout:             &output,
		Runner:             &fakeRunner{},
		ProvisionHarvest:   true,
		HarvestProvisioner: fake,
		HarvestPlatform:    harvestpy.Platform{GOOS: "linux", GOARCH: "amd64"},
	}); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, output.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("managed harvest root remains: %v", err)
	}
	if body, err := os.ReadFile(keep); err != nil || string(body) != "keep" {
		t.Fatalf("uninstall changed unrelated state: body=%q err=%v", body, err)
	}
	if !strings.Contains(output.String(), "remove managed harvestpy runtime and cache") {
		t.Fatalf("uninstall did not state explicit managed change:\n%s", output.String())
	}
}
