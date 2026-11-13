package testjail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestPFMBinaryUsesExecutablePrebuilt(t *testing.T) {
	prebuilt := filepath.Join(t.TempDir(), "pfm")
	if err := WriteExecutable(prebuilt, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTestPFMBinary, prebuilt)
	got, err := PFMBinary(t.TempDir(), t.TempDir())
	if err != nil || got != prebuilt {
		t.Fatalf("PFMBinary() = %q, %v; want %q", got, err, prebuilt)
	}
}

// TestPrebuiltBinariesRejectBrokenPaths: a prebuilt pfm or mock engine that is
// missing, not executable or a directory is a named error, never a binary.
func TestPrebuiltBinariesRejectBrokenPaths(t *testing.T) {
	got := ""
	for _, binary := range []struct {
		env   string
		build func(string, string) (string, error)
	}{{paths.EnvTestPFMBinary, PFMBinary}, {paths.EnvTestMockEngineBinary, MockEngineBinary}} {
		nonExecutable := filepath.Join(t.TempDir(), "binary")
		if err := os.WriteFile(nonExecutable, []byte("binary"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{filepath.Join(t.TempDir(), "missing"), nonExecutable, t.TempDir()} {
			t.Setenv(binary.env, path)
			built, err := binary.build(t.TempDir(), t.TempDir())
			got += rooted(t, fmt.Sprintf("%q %v\n", built, err))
		}
	}
	if want := "\"\" PFM_TEST_PFM_BINARY=\"{root}/002/missing\": stat {root}/002/missing: no such file or directory\n\"\" PFM_TEST_PFM_BINARY=\"{root}/001/binary\": not an executable regular file\n\"\" PFM_TEST_PFM_BINARY=\"{root}/003\": not an executable regular file\n\"\" PFM_TEST_MOCK_ENGINE_BINARY=\"{root}/014/missing\": stat {root}/014/missing: no such file or directory\n\"\" PFM_TEST_MOCK_ENGINE_BINARY=\"{root}/013/binary\": not an executable regular file\n\"\" PFM_TEST_MOCK_ENGINE_BINARY=\"{root}/015\": not an executable regular file\n"; got != want {
		t.Fatalf("prebuilt binaries = %q, want %q", got, want)
	}
}

func TestMockEngineBinaryUsesExecutablePrebuilt(t *testing.T) {
	prebuilt := filepath.Join(t.TempDir(), "mock-engine")
	if err := WriteExecutable(prebuilt, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTestMockEngineBinary, prebuilt)
	got, err := MockEngineBinary(t.TempDir(), t.TempDir())
	if err != nil || got != prebuilt {
		t.Fatalf("MockEngineBinary() = %q, %v; want %q", got, err, prebuilt)
	}
}

// TestBinaryWithoutAModuleOrPrebuiltNamesBoth: a test process that started outside the module
// (RunInJail's child, launched in the jail) and holds no prebuilt binary gets a named error, never
// a build in whatever directory it stands in; with a prebuilt binary it needs no module at all.
func TestBinaryWithoutAModuleOrPrebuiltNamesBoth(t *testing.T) {
	for _, name := range []string{paths.EnvTestPFMBinary, paths.EnvTestMockEngineBinary} {
		t.Setenv(name, "") // restored at cleanup; unset now, since a set empty value is a prebuilt path
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	_, pfmErr := PFMBinary("", t.TempDir())
	_, mockErr := MockEngineBinary("", t.TempDir())
	if got, want := fmt.Sprintf(
		"%v\n%v",
		pfmErr,
		mockErr,
	), "build ./cmd/pfm: no go.mod above the directory the test started in, and PFM_TEST_PFM_BINARY is unset\n"+
		"build ./cmd/mock-engine: no go.mod above the directory the test started in, and PFM_TEST_MOCK_ENGINE_BINARY is unset"; got != want {
		t.Fatalf("binaries without a module = %q, want %q", got, want)
	}
	prebuilt := filepath.Join(t.TempDir(), "pfm")
	if err := WriteExecutable(prebuilt, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTestPFMBinary, prebuilt)
	if got, err := PFMBinary("", t.TempDir()); err != nil || got != prebuilt {
		t.Fatalf("PFMBinary(no module) = %q, %v; want prebuilt %q", got, err, prebuilt)
	}
}

func TestGoBuildReportsResolveFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := GoBuild(t.TempDir(), filepath.Join(t.TempDir(), "pfm"), "./cmd/pfm")
	if got, want := rooted(
		t,
		fmt.Sprint(err),
	), "resolve go for test build: "+NotOnPath("go"); got != want {
		t.Fatalf("GoBuild error = %q, want %q", got, want)
	}
}

// rooted writes the test's temp root as {root}, so an error naming a t.TempDir() compares exactly.
func rooted(t *testing.T, s string) string {
	return strings.ReplaceAll(s, filepath.Dir(t.TempDir()), "{root}")
}
