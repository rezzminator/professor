package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
)

// TestDoctorReportsEachVSCodeProductLinkAndIndexState pins issue #24 9b: no
// doctor row covered the VS Code wiring at all, so 9a's link-without-index
// registration was invisible to `pfm doctor`. A jail ledger recording two
// product links — one whose product index already registers pfm's entry,
// one whose index is a bare empty array — must produce two rows and count
// exactly the MISSING one as a warning; a host with no ledger at all reports
// "not managed" and adds no warning.
func TestDoctorReportsEachVSCodeProductLinkAndIndexState(t *testing.T) {
	home := t.TempDir()
	managedRoot := filepath.Join(home, ".local", "share", "pfm", "install")
	source := filepath.Join(managedRoot, "vscode", "professor")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}

	rootRegistered := filepath.Join(home, "product-registered")
	rootMissing := filepath.Join(home, "product-missing")
	targetRegistered := filepath.Join(rootRegistered, "extensions", "professor")
	targetMissing := filepath.Join(rootMissing, "extensions", "professor")
	for _, target := range []string{targetRegistered, targetMissing} {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
	}

	registeredIndex := filepath.Join(filepath.Dir(targetRegistered), "extensions.json")
	entry := map[string]any{
		"identifier":       map[string]any{"id": "professor.professor"},
		"version":          "0.1.0",
		"relativeLocation": "professor",
		"location":         map[string]any{"$mid": 1, "path": targetRegistered, "scheme": "file"},
	}
	encodedIndex, err := json.Marshal([]map[string]any{entry})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registeredIndex, encodedIndex, 0o644); err != nil {
		t.Fatal(err)
	}
	missingIndex := filepath.Join(filepath.Dir(targetMissing), "extensions.json")
	if err := os.WriteFile(missingIndex, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledger := map[string]any{"version": 1, "extensions": []string{targetRegistered, targetMissing}}
	encodedLedger, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "vscode-ownership.json"), encodedLedger, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	warnings := printVSCodeDoctor(&out, home, config.Config{})
	output := out.String()
	rows := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "doctor: vscode product=") {
			rows++
		}
	}
	if rows != 2 {
		t.Fatalf("vscode product rows = %d, want 2:\n%s", rows, output)
	}
	if !strings.Contains(output, "product="+rootRegistered+" link=ok index=registered") {
		t.Fatalf("registered product row missing or wrong:\n%s", output)
	}
	if !strings.Contains(output, "product="+rootMissing+" link=ok index=MISSING") {
		t.Fatalf("missing-index product row missing or wrong:\n%s", output)
	}
	if warnings != 1 {
		t.Fatalf("warnings = %d, want 1 (only the MISSING product):\n%s", warnings, output)
	}

	var absent bytes.Buffer
	absentWarnings := printVSCodeDoctor(&absent, t.TempDir(), config.Config{})
	if !strings.Contains(absent.String(), "doctor: vscode not managed (pfm install --vscode never ran)") {
		t.Fatalf("ledger-absent host did not report not-managed:\n%s", absent.String())
	}
	if absentWarnings != 0 {
		t.Fatalf("ledger-absent host warnings = %d, want 0", absentWarnings)
	}
}
