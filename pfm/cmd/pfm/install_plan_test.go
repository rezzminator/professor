package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	"github.com/rezzminator/professor/pfm/internal/hostcheck"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestInstallPlanNeedsCheck(t *testing.T) {
	for _, args := range [][]string{{"--plan"}, {"--plan", "--yes"}} {
		var out, errOut bytes.Buffer
		code := runInstall(args, &out, &errOut)
		if code != 2 || out.Len() != 0 ||
			errOut.String() != "pfm install: --plan needs --check: run pfm install --check --plan\n" {
			t.Fatalf("args=%q code=%d stdout=%q stderr=%q", args, code, out.String(), errOut.String())
		}
	}
}

// installPlanRun runs `pfm install --check --plan` on the staged host with the
// installer forbidden and the dependency probe quiet, and proves home unchanged.
func installPlanRun(t *testing.T, home string) (code int, stdout, stderr string) {
	t.Helper()
	bin, _ := writeManagerFakes(
		t,
		"case \"$*\" in *ActiveState*) echo inactive;; esac\nexit 0",
		"echo 'state = not running'",
	)
	t.Setenv("PATH", bin)
	savedProbe, savedInstaller := doctor.DependencyProbeOverride, runInstaller
	t.Cleanup(func() { doctor.DependencyProbeOverride, runInstaller = savedProbe, savedInstaller })
	doctor.DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result { return nil }
	runInstaller = func(context.Context, installer.Options) (installer.Report, error) {
		t.Fatal("--check --plan ran installer")
		return installer.Report{}, nil
	}
	loaded, err := pfmconfig.LoadInstallRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	before := installCheckTree(t, home)
	var out, errOut bytes.Buffer
	code = runInstall([]string{"--check", "--plan"}, &out, &errOut, loaded)
	if !reflect.DeepEqual(before, installCheckTree(t, home)) {
		t.Fatal("--check --plan changed home")
	}
	return code, out.String(), errOut.String()
}

func writeStale(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// TestInstallCheckPlanOrdersFixes stages a single-account host (one account,
// no second seat) whose detectors register shared-db before home-state-file;
// the plan still lists the config move first, the store's identity file
// before the cleanup rows, and every fix line under its row.
func TestInstallCheckPlanOrdersFixes(t *testing.T) {
	home, _, _ := installCheckHome(t, true)
	writeStale(t, filepath.Join(home, ".local", "state", "pfm", "shared.db"))
	writeStale(t, filepath.Join(home, ".claude.json"))
	writeStale(t, filepath.Join(home, ".claude.json.tmp.fixture"))
	code, stdout, stderr := installPlanRun(t, home)
	header := fmt.Sprintf("install plan: %d host checks ran, 0 failed — ", len(hostcheck.Detectors()))
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if !strings.HasPrefix(lines[0], header) || lines[len(lines)-1] != "  then: pfm install --yes, then pfm doctor" {
		t.Fatalf("stdout=%q", stdout)
	}
	step := regexp.MustCompile(`^ {2}\d+\. (BLOCK|WARN) (\S+) `)
	var classes []string
	blocking := 0
	for i, line := range lines {
		match := step.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "      fix: ") || lines[i+1] == "      fix: " {
			t.Fatalf("step %q has no fix line in %q", line, stdout)
		}
		if match[1] == "BLOCK" {
			blocking++
		}
		switch match[2] {
		case "legacy-config", "home-state-file", "shared-db", "stale-state-tmp":
			classes = append(classes, match[2])
		}
	}
	want := []string{"legacy-config", "home-state-file", "shared-db", "stale-state-tmp"}
	if !reflect.DeepEqual(classes, want) {
		t.Fatalf("classes=%q want=%q stdout=%q", classes, want, stdout)
	}
	wantErr := fmt.Sprintf(
		"pfm install: %d blocking — apply the plan above in order, then rerun pfm install --check --plan\n",
		blocking,
	)
	if code != 4 || stderr != wantErr {
		t.Fatalf("code=%d stderr=%q want=%q", code, stderr, wantErr)
	}
}

// TestInstallCheckPlanCleanHost also stages a pfm-named socket in the ambient
// tmux directory, where a parallel test's or the host's servers live: the
// fixture's own tmux directory is inside its home, so the plan never probes it.
func TestInstallCheckPlanCleanHost(t *testing.T) {
	home, _, _ := installCheckHome(t, false)
	ambient := testjail.ShortRoot(t)
	t.Setenv("TMUX_TMPDIR", ambient)
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	strayDir := filepath.Join(ambient, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(strayDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stray, err := net.Listen("unix", filepath.Join(strayDir, "probe-stray"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stray.Close(); err != nil {
			t.Logf("close the stray socket: %v", err)
		}
	})
	code, stdout, stderr := installPlanRun(t, home)
	want := fmt.Sprintf("install plan: %d host checks ran, 0 failed — no fixes\n", len(hostcheck.Detectors())) +
		"install check: ok — pfm install --yes would pass its pre-change checks\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("code=%d stdout=%q want=%q stderr=%q", code, stdout, want, stderr)
	}
}

// TestInstallCheckPlanListsFailedCheck makes a check fail to look (a directory
// where a file is read; a chmod would not stop root in the fence): the plan
// lists it as FAILED and never says "no fixes".
func TestInstallCheckPlanListsFailedCheck(t *testing.T) {
	home, _, _ := installCheckHome(t, false)
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.Mkdir(zshrc, 0o700); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := installPlanRun(t, home)
	failed := "  FAILED staged-shim " + zshrc + " — UNREADABLE " + zshrc + ": is a directory\n"
	rerun := "  then: rerun pfm install --check --plan\n"
	if code != 4 || !strings.Contains(stdout, failed) || strings.Contains(stdout, "no fixes") ||
		strings.Contains(stdout, " 0 failed") || !strings.HasSuffix(stdout, rerun) ||
		!strings.HasPrefix(stderr, "pfm install: ") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
