//go:build jail

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// Jail: TestMain is the subject, so the test binary re-executes itself under a fixture PATH.
func TestTestMainUsesPrebuiltPFMBinary(t *testing.T) {
	if want := os.Getenv("TEST_EXPECT_PFM_BINARY"); want != "" {
		if testPFMBinary != want {
			t.Fatalf("testPFMBinary = %q, want prebuilt %q", testPFMBinary, want)
		}
		return
	}
	goDir := t.TempDir()
	if err := testjail.WriteExecutable(filepath.Join(goDir, "go"), []byte("#!/bin/sh\nexit 41\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	prebuilt := filepath.Join(t.TempDir(), "pfm")
	if err := testjail.WriteExecutable(prebuilt, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestTestMainUsesPrebuiltPFMBinary$")
	command.Env = append(os.Environ(),
		"PATH="+goDir, paths.EnvTestPFMBinary+"="+prebuilt, "TEST_EXPECT_PFM_BINARY="+prebuilt,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run with prebuilt pfm: %v\n%s", err, output)
	}
	if _, err := os.Stat(prebuilt); err != nil {
		t.Fatalf("TestMain removed prebuilt binary %q: %v", prebuilt, err)
	}
}

// Jail: TestMain is the subject, so the test binary re-executes itself and must exit with the named failure.
func TestTestMainRejectsBrokenPrebuiltPFMBinary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-pfm")
	command := exec.Command(os.Args[0], "-test.run=^TestTestMainRejectsBrokenPrebuiltPFMBinary$")
	command.Env = append(os.Environ(), paths.EnvTestPFMBinary+"="+missing)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), paths.EnvTestPFMBinary) ||
		!strings.Contains(string(output), missing) {
		t.Fatalf("TestMain broken prebuilt = %v, %q; want named failure", err, output)
	}
}
