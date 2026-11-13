package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

var testPFMBinary string

type noNetworkThemeTransport struct{}

func (noNetworkThemeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"name":"Tokyo Night","fixture":true}`)),
	}, nil
}

// testBinaryDir is where TestMain built the shared pfm binary, and where the
// watch ladder builds its mock engine once; empty when no build ran.
var testBinaryDir string

// attachHelperEnv marks a re-exec of this test binary as the attach helper a jailed attach test
// runs inside a live tmux pane (attach_jail_test.go); TestMain reads it in every build.
const attachHelperEnv = "PFM_ATTACH_HELPER"

// TestMain gives this package a short, canonical TMPDIR before any test builds
// a path from it. See internal/testjail for why both properties matter.
func TestMain(m *testing.M) {
	flag.Parse()
	binaryDir := ""
	// This daemon test exercises its in-process handler and never starts the
	// shared CLI binary. Its one-test run need not pay for a Go build.
	if flag.Lookup("test.list").Value.String() == "" && os.Getenv(attachHelperEnv) != "1" &&
		!runningOnlyMCPDaemonMountedTest() {
		var err error
		binaryDir, err = os.MkdirTemp(paths.TempRoot(), "pfm-cmd-test-binary-")
		if err != nil {
			_, _ = os.Stderr.WriteString("create shared pfm test binary directory: " + err.Error() + "\n")
			os.Exit(1)
		}
		testBinaryDir = binaryDir
		testPFMBinary, err = testjail.PFMBinary(filepath.Join("..", ".."), binaryDir)
		if err != nil {
			_, _ = os.Stderr.WriteString(
				"build shared pfm test binary: " + err.Error() + "\n",
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
	installThemeHTTPClientOverride = &http.Client{Transport: noNetworkThemeTransport{}}
	// os.Executable is this test binary, no pfm: a front's paced worker spawn
	// would run the whole suite again, detached. A test that counts spawns
	// replaces this stub.
	statuslinePacedSpawn = func(string) error { return nil }
	// The same holds for a kill's detached exit finisher: a test that reads
	// the start replaces this recorder.
	fleet.KillFinisherRunner = &deps.FakeRunner{}
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
	installer.HookProbeOverride = func(string, pfmconfig.Config) []installer.HookProbeResult { return nil }
	// No jail has a real `claude` to spawn — captureHarnessPrompt's own doc
	// comment marks that flow UNPLAYED. This stub stands in for every test;
	// whether a doctor fixture reads as matches/DRIFT/CHECK-FAILED still
	// depends only on what baseline (if any) the fixture stages, via
	// stageHarnessPromptBaseline in main_test.go.
	doctor.HarnessCaptureOverride = func(_ context.Context, _ string, _ pfmconfig.Config, alias, _ string) (doctor.HarnessCapture, error) {
		prompt, err := testjail.ShippedHarnessPrompt(alias)
		return doctor.HarnessCapture{
			Prompt:        prompt,
			ResolvedModel: "claude-" + alias + "-5",
			CLIVersion:    "fixture",
		}, err
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

func runningOnlyMCPDaemonMountedTest() bool {
	const pattern = "^TestMCPDaemonMountedServersNeedNoAuthAndServeTools$"
	for index, arg := range os.Args {
		if arg == "-test.run="+pattern || arg == "-test.run" && index+1 < len(os.Args) && os.Args[index+1] == pattern {
			return true
		}
	}
	return false
}
