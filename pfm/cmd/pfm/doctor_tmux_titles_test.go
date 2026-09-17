package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
)

// The check exists so a reader can tell "pfm owns this title" from "the host
// owns it and pfm has not stomped it". Both states must be visible, and
// neither may be reported as a fault.
func TestTmuxTitlesDoctorReportsBothOwnersOnProbeSockets(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socketDirectory := "tmux-" + strconv.Itoa(os.Getuid())
	base := filepath.Join(os.TempDir(), socketDirectory)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "probe-pfm-titles-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove probe jail: %v", err)
		}
	})
	tmuxDir := filepath.Join(root, socketDirectory)
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}

	owned := "probe-cc-1800000005-1-1"
	hostOwned := "probe-cc-1800000006-1-1"
	for _, socket := range []string{owned, hostOwned} {
		socketPath := filepath.Join(tmuxDir, socket)
		start := exec.Command(
			"tmux", "-S", socketPath, "-f", "/dev/null",
			"new-session", "-d", "-s", socket, "-n", "probe", "sleep 120",
		)
		start.Env = append(os.Environ(), "TMUX=")
		if output, err := start.CombinedOutput(); err != nil {
			t.Fatalf("start probe server %s: %v: %s", socket, err, output)
		}
		t.Cleanup(func() {
			kill := exec.Command("tmux", "-S", socketPath, "kill-server")
			kill.Env = append(os.Environ(), "TMUX=")
			_ = kill.Run()
		})
	}
	setTitles := exec.Command(
		"tmux", "-S", filepath.Join(tmuxDir, owned),
		"set-option", "-g", "set-titles", "on",
	)
	setTitles.Env = append(os.Environ(), "TMUX=")
	if output, err := setTitles.CombinedOutput(); err != nil {
		t.Fatalf("set-titles on: %v: %s", err, output)
	}
	setTitlesString := exec.Command(
		"tmux", "-S", filepath.Join(tmuxDir, owned),
		"set-option", "-g", "set-titles-string", config.TmuxTitlesString,
	)
	setTitlesString.Env = append(os.Environ(), "TMUX=")
	if output, err := setTitlesString.CombinedOutput(); err != nil {
		t.Fatalf("set-titles-string: %v: %s", err, output)
	}

	// The probe-socket allowance keeps the fixture off every live cc-/cx-
	// name while still exercising the production discovery path.
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	machine := config.Config{Tmux: config.Tmux{Titles: config.DefaultTmuxTitles()}}
	var stdout bytes.Buffer
	printTmuxTitlesDoctor(
		context.Background(), &stdout,
		paths.Values{TmuxDir: tmuxDir, Home: root}, machine,
	)
	report := stdout.String()
	for _, want := range []string{
		"doctor: tmux titles policy=pfm-owned (config tmux.titles.enabled=true default)",
		"doctor: tmux titles " + owned + "=pfm-owned (set-titles on)",
		"doctor: tmux titles " + hostOwned + "=divergent (set-titles off; set-titles-string \"#S:#I:#W - \\\"#T\\\" #{session_alerts}\"; expected set-titles on and set-titles-string \"" + config.TmuxTitlesString + "\") DIVERGES from policy=pfm-owned: expected set-titles on",
		"doctor: tmux titles divergent=1",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}

	// The check must not have TOUCHED either server: the host-owned one still
	// reads off after the audit.
	read := exec.Command(
		"tmux", "-S", filepath.Join(tmuxDir, hostOwned),
		"show-options", "-g", "set-titles",
	)
	read.Env = append(os.Environ(), "TMUX=")
	output, err := read.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(output)); got != "set-titles off" {
		t.Fatalf("the audit changed a host-owned server: set-titles = %q", got)
	}
}

func TestTmuxTitlesDoctorReportsADisabledPolicy(t *testing.T) {
	machine := config.Config{
		Tmux:    config.Tmux{Titles: config.TmuxTitles{Enabled: false}},
		Sources: map[string]config.Source{"tmux.titles.enabled": config.SourceFile},
	}
	var stdout bytes.Buffer
	printTmuxTitlesDoctor(
		context.Background(), &stdout,
		paths.Values{TmuxDir: filepath.Join(t.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))}, machine,
	)
	want := "doctor: tmux titles policy=host-owned (config tmux.titles.enabled=false file)"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("report missing %q:\n%s", want, stdout.String())
	}
}
