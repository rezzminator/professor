package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// stageHostOverlayManagedCopies writes both contracted overlay scripts into
// the managed install root a real `pfm install` would have staged them at,
// so a test can wire (or deliberately not wire) the canonical symlink on top
// without exercising the whole installer.
func stageHostOverlayManagedCopies(t *testing.T, home string) string {
	t.Helper()
	managed := filepath.Join(home, ".local", "share", "pfm", "install", "bin")
	if err := os.MkdirAll(managed, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pfm-statusline", "tmux-title-renudge"} {
		if err := testjail.WriteExecutable(
			filepath.Join(managed, name),
			[]byte("#!/bin/sh\nexit 0\n"),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
	}
	return managed
}

// wireHostOverlaySymlinks correctly links both canonical ~/.local/bin
// overlay names to their managed copies — the healthy baseline several
// tests start from before deliberately breaking one thing at a time.
func wireHostOverlaySymlinks(t *testing.T, home, managed string) string {
	t.Helper()
	canonical := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pfm-statusline", "tmux-title-renudge"} {
		if err := os.Symlink(filepath.Join(managed, name), filepath.Join(canonical, name)); err != nil {
			t.Fatal(err)
		}
	}
	return canonical
}

// TestHostOverlayDoctorMissingSymlinksAreFailures pins issue #14 F1.d: on a
// HOME with no host-overlay wiring at all (the exact defect the issue
// reported — both symlinks absent), doctor names each contracted overlay by
// name and counts it as a failure, not a soft note.
func TestHostOverlayDoctorMissingSymlinksAreFailures(t *testing.T) {
	home := t.TempDir()
	var output bytes.Buffer
	if warnings, failures := printHostOverlayDoctor(&output, home, pfmconfig.Config{}); warnings != 0 || failures != 2 {
		t.Fatalf("warnings=%d failures=%d, want 0/2 (both overlays missing)\n%s", warnings, failures, output.String())
	}
	for _, wanted := range []string{
		"doctor: host_overlay pfm-statusline missing — run pfm install --yes",
		"doctor: host_overlay tmux-title-renudge missing — run pfm install --yes",
	} {
		if !strings.Contains(output.String(), wanted) {
			t.Fatalf("output missing %q:\n%s", wanted, output.String())
		}
	}
}

// TestHostOverlayDoctorDisplacedSymlinkIsAFailure covers the other half of
// InspectHostOverlays' state machine: a stale regular file sitting where the
// symlink belongs is DISPLACED, not missing, while its correctly-wired
// sibling reports clean.
func TestHostOverlayDoctorDisplacedSymlinkIsAFailure(t *testing.T) {
	home := t.TempDir()
	managed := stageHostOverlayManagedCopies(t, home)
	canonical := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(
		filepath.Join(canonical, "pfm-statusline"),
		[]byte("stale copy, never a link\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(managed, "tmux-title-renudge"),
		filepath.Join(canonical, "tmux-title-renudge"),
	); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if warnings, failures := printHostOverlayDoctor(&output, home, pfmconfig.Config{}); warnings != 0 || failures != 1 {
		t.Fatalf("warnings=%d failures=%d, want 0/1\n%s", warnings, failures, output.String())
	}
	if !strings.Contains(
		output.String(),
		"doctor: host_overlay pfm-statusline DISPLACED by "+filepath.Join(canonical, "pfm-statusline"),
	) {
		t.Fatalf("output missing displaced pfm-statusline:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "doctor: host_overlay tmux-title-renudge ok") {
		t.Fatalf("output missing healthy tmux-title-renudge:\n%s", output.String())
	}
}

// The overlay check judges the two managed links even when account settings
// cannot be parsed; host checks report account file problems separately.
func TestHostOverlayDoctorChecksLinksWithUnreadableAccountSettings(t *testing.T) {
	home := t.TempDir()
	managed := stageHostOverlayManagedCopies(t, home)
	wireHostOverlaySymlinks(t, home, managed)
	configDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: configDir}}}
	var output bytes.Buffer
	if warnings, failures := printHostOverlayDoctor(&output, home, machine); warnings != 0 || failures != 0 {
		t.Fatalf("warnings=%d failures=%d, want 0/0\n%s", warnings, failures, output.String())
	}
	for _, name := range []string{"pfm-statusline", "tmux-title-renudge"} {
		if !strings.Contains(output.String(), "doctor: host_overlay "+name+" ok") {
			t.Fatalf("missing healthy link row for %s: %s", name, output.String())
		}
	}
}
