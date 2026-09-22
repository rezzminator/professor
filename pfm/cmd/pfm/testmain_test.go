package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

var testPFMBinary string

type noNetworkHarvestProvisioner struct{}

func (noNetworkHarvestProvisioner) Plan(platform harvestpy.Platform) (harvestpy.InstallPlan, error) {
	return harvestpy.PlanConversionEnvironment(platform)
}

func (noNetworkHarvestProvisioner) Check(context.Context, string, harvestpy.Platform) (harvestpy.CheckReport, error) {
	return harvestpy.CheckReport{Healthy: true}, nil
}

func (noNetworkHarvestProvisioner) Provision(
	context.Context,
	harvestpy.ProvisionOptions,
) (harvestpy.ProvisionResult, error) {
	return harvestpy.ProvisionResult{}, errors.New("test fake must not provision the pinned runtime")
}

type noNetworkHarvestDoctor struct{}

type noNetworkThemeTransport struct{}

func (noNetworkThemeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"name":"Tokyo Night","fixture":true}`)),
	}, nil
}

func (noNetworkHarvestDoctor) Inspect(string, harvestpy.Platform) (harvestpy.EnvironmentDigest, error) {
	return noNetworkHarvestDigest(), nil
}

func (noNetworkHarvestDoctor) Check(context.Context, string, harvestpy.Platform) (harvestpy.CheckReport, error) {
	digest := noNetworkHarvestDigest()
	return harvestpy.CheckReport{
		Healthy: true,
		Digest:  digest,
		Checks: map[string]harvestpy.CheckStatus{
			"interpreter":           {OK: true},
			"lock_completeness":     {OK: true},
			"live_smoke":            {OK: true},
			"live_smoke_conversion": {OK: true},
		},
	}, nil
}

func noNetworkHarvestDigest() harvestpy.EnvironmentDigest {
	return harvestpy.EnvironmentDigest{
		Schema:          1,
		Python:          "3.11.15+20260610",
		LockSHA256:      "test-lock-digest",
		InventorySHA256: "test-inventory-digest",
		InventoryCount:  1,
	}
}

// TestMain gives this package a short, canonical TMPDIR before any test builds
// a path from it. See internal/testjail for why both properties matter.
func TestMain(m *testing.M) {
	binaryDir := ""
	if os.Getenv(attachHelperEnv) != "1" {
		var err error
		binaryDir, err = os.MkdirTemp("", "pfm-cmd-test-binary-")
		if err != nil {
			_, _ = os.Stderr.WriteString("create shared pfm test binary directory: " + err.Error() + "\n")
			os.Exit(1)
		}
		testPFMBinary = filepath.Join(binaryDir, "pfm")
		build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", testPFMBinary, ".")
		if output, buildErr := build.CombinedOutput(); buildErr != nil {
			_, _ = os.Stderr.WriteString(
				"build shared pfm test binary: " + buildErr.Error() + ": " + string(output) + "\n",
			)
			_ = os.RemoveAll(binaryDir)
			os.Exit(1)
		}
	}
	// Mirrors main()'s own call (issue #24 F1): every test in this package
	// calls run()/runInternal() directly, never main(), so without this the
	// registry stays unset for the whole suite and every unknown-pfm-hook
	// scenario a rollback/residue test stages would never be recognized.
	installer.SetImplementedSubcommands(topLevelSubcommands, internalSubcommands)
	installHarvestProvisionerOverride = noNetworkHarvestProvisioner{}
	installThemeHTTPClientOverride = &http.Client{Transport: noNetworkThemeTransport{}}
	doctor.HarvestOverride = noNetworkHarvestDoctor{}
	doctor.DaemonReachabilityOverride = func(pfmconfig.Runtime) (mcpserv.DaemonStatus, error) {
		return mcpserv.DaemonStatus{}, fmt.Errorf("%w: jailed test never probes a live daemon", mcpserv.ErrDaemonAbsent)
	}
	doctor.PrePushGateProbeOverride = func(context.Context) doctor.PrePushGate {
		return doctor.PrePushGate{State: "outside-repository"}
	}
	doctor.DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, _ deps.ProbeOptions) []deps.Result {
		results := make([]deps.Result, 0, len(entries))
		for index := range entries {
			entry := &entries[index]
			if !entry.AppliesTo(runtime.GOOS) {
				results = append(
					results,
					deps.Result{Entry: *entry, State: deps.StateSkipped, Error: "not this platform"},
				)
				continue
			}
			results = append(
				results,
				deps.Result{
					Entry:   *entry,
					State:   deps.StateOK,
					Path:    "/test/bin/" + entry.Name,
					Version: entry.MinVersion,
				},
			)
		}
		return results
	}
	installer.HookProbeOverride = func(home string, machine pfmconfig.Config) []installer.HookProbeResult {
		expected := installer.ExpectedHooks(home, machine)
		results := make([]installer.HookProbeResult, 0, len(expected))
		for _, hook := range expected {
			results = append(results, installer.HookProbeResult{Hook: hook, State: "ok"})
		}
		return results
	}
	// No jail has a real `claude` to spawn — captureHarnessPrompt's own doc
	// comment marks that REAL-SESSION. This stub stands in for every test;
	// whether a doctor fixture reads as matches/DRIFT/CHECK-FAILED still
	// depends only on what baseline (if any) the fixture stages, via
	// stageHarnessPromptBaseline in main_test.go.
	doctor.HarnessCaptureOverride = func(_ context.Context, _ string, _ pfmconfig.Config, alias, _ string) (doctor.HarnessCapture, error) {
		return doctor.HarnessCapture{
			Prompt:        harnessPromptFixtureCaptured,
			ResolvedModel: "claude-" + alias + "-5",
			CLIVersion:    "fixture",
		}, nil
	}
	testjail.KeepAmbientIdentity = os.Getenv(attachHelperEnv) == "1"
	code := testjail.Run(m)
	if binaryDir != "" {
		if err := os.RemoveAll(binaryDir); err != nil && code == 0 {
			_, _ = os.Stderr.WriteString("remove shared pfm test binary directory: " + err.Error() + "\n")
			code = 1
		}
	}
	os.Exit(code)
}
