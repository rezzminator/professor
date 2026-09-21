package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestInstallPreflightRefusesRequiredDependencyBeforeInstallerRuns(t *testing.T) {
	savedProbe, savedInstaller := doctor.DependencyProbeOverride, runInstaller
	t.Cleanup(func() {
		doctor.DependencyProbeOverride = savedProbe
		runInstaller = savedInstaller
	})
	doctor.DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, _ deps.ProbeOptions) []deps.Result {
		for _, entry := range entries {
			if entry.Name == "tmux" {
				return []deps.Result{{Entry: entry, State: deps.StateMissing}}
			}
		}
		t.Fatal("tmux registry entry missing")
		return nil
	}
	called := false
	runInstaller = func(context.Context, installer.Options) (installer.Report, error) {
		called = true
		return installer.Report{}, nil
	}
	home := t.TempDir()
	runtime := commandRuntime{
		Config: pfmconfig.Config{Claude: pfmconfig.Claude{Binary: "claude"}, Codex: pfmconfig.Codex{Binary: "codex"}},
		Paths: paths.Values{
			Home:  home,
			Roots: map[pfmengine.ID][]string{pfmengine.Codex: {filepath.Join(home, ".codex")}},
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime); code != 1 {
		t.Fatalf("install code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if called || !strings.Contains(stdout.String(), "doctor: dep tmux path=(none) MISSING required") ||
		!strings.Contains(stderr.String(), "required dependency preflight failed") {
		t.Fatalf("called=%t stdout=%s stderr=%s", called, stdout.String(), stderr.String())
	}
}

func TestInstallPreflightDoesNotRefuseBrokenOptionalEngine(t *testing.T) {
	savedProbe, savedInstaller := doctor.DependencyProbeOverride, runInstaller
	t.Cleanup(func() {
		doctor.DependencyProbeOverride = savedProbe
		runInstaller = savedInstaller
	})
	doctor.DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, _ deps.ProbeOptions) []deps.Result {
		for _, entry := range entries {
			if entry.Name == "codex" {
				return []deps.Result{
					{
						Entry: entry,
						State: deps.StateBroken,
						Path:  "/fixture/codex",
						Error: "self-doctor failed: auth missing",
					},
				}
			}
		}
		t.Fatal("codex registry entry missing")
		return nil
	}
	called := false
	runInstaller = func(context.Context, installer.Options) (installer.Report, error) {
		called = true
		return installer.Report{}, nil
	}
	home := t.TempDir()
	runtime := commandRuntime{
		Config: pfmconfig.Config{
			Codex:         pfmconfig.Codex{Binary: "codex"},
			CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}},
		},
		Paths: paths.Values{Home: home},
	}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf(
			"install code=%d stdout=%s stderr=%s, want optional Codex failure to remain non-blocking",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if !called || !strings.Contains(stdout.String(), "dep codex") ||
		!strings.Contains(stdout.String(), "auth missing") {
		t.Fatalf(
			"called=%t stdout=%s stderr=%s, want a visible optional failure followed by install",
			called,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestInstallPreflightFailureStillPreviewsInDryRun(t *testing.T) {
	savedProbe, savedInstaller := doctor.DependencyProbeOverride, runInstaller
	t.Cleanup(func() {
		doctor.DependencyProbeOverride = savedProbe
		runInstaller = savedInstaller
	})
	doctor.DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, _ deps.ProbeOptions) []deps.Result {
		for _, entry := range entries {
			if entry.Name == "tmux" {
				return []deps.Result{{Entry: entry, State: deps.StateMissing}}
			}
		}
		t.Fatal("tmux registry entry missing")
		return nil
	}
	called := false
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		if options.Mode != installer.ModeDryRun {
			t.Errorf("install mode=%v, want dry run", options.Mode)
		}
		called = true
		return installer.Report{}, nil
	}
	home := t.TempDir()
	runtime := commandRuntime{
		Config: pfmconfig.Config{Claude: pfmconfig.Claude{Binary: "claude"}, Codex: pfmconfig.Codex{Binary: "codex"}},
		Paths: paths.Values{
			Home:  home,
			Roots: map[pfmengine.ID][]string{pfmengine.Codex: {filepath.Join(home, ".codex")}},
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--skip-harvest"}, &stdout, &stderr, runtime); code != 1 {
		t.Fatalf("install code=%d, want 1\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	if !called {
		t.Fatal("read-only preview never ran — a fresh machine gets no plan at all")
	}
	if !strings.Contains(stdout.String(), "doctor: dep tmux path=(none) MISSING required") ||
		!strings.Contains(stderr.String(), "required dependency preflight failed") {
		t.Fatalf("missing preflight report:\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "if you agree, run again") {
		t.Fatalf("apply confirmation offered despite failed preflight:\n%s", stdout.String())
	}
}
