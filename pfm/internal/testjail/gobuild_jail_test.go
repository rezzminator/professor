//go:build jail

package testjail

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestPFMBinaryBuildsWhenUnset(t *testing.T) {
	previous, hadPrevious := os.LookupEnv(paths.EnvTestPFMBinary)
	if err := os.Unsetenv(paths.EnvTestPFMBinary); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var err error
		if hadPrevious {
			err = os.Setenv(paths.EnvTestPFMBinary, previous)
		} else {
			err = os.Unsetenv(paths.EnvTestPFMBinary)
		}
		if err != nil {
			t.Errorf("restore %s: %v", paths.EnvTestPFMBinary, err)
		}
	})
	goDir := t.TempDir()
	log := filepath.Join(t.TempDir(), "go.log")
	stub := `#!/bin/sh
printf 'HOME=%s CGO_ENABLED=%s GOFLAGS=%s GOTOOLCHAIN=%s GOTELEMETRY=%s ARGS=%s\n' \
  "$HOME" "$CGO_ENABLED" "$GOFLAGS" "$GOTOOLCHAIN" "$GOTELEMETRY" "$*" > "$TEST_GO_BUILD_LOG"
while [ "$1" != -o ]; do shift; done
shift
printf '#!/bin/sh\nexit 0\n' > "$1"
chmod +x "$1"
`
	if err := WriteExecutable(filepath.Join(goDir, "go"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", goDir+":"+os.Getenv("PATH"))
	t.Setenv("TEST_GO_BUILD_LOG", log)
	dir := t.TempDir()
	got, err := PFMBinary(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "pfm"); got != want {
		t.Fatalf("PFMBinary() = %q, want %q", got, want)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat built binary %q: %v", got, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("built binary %q is not executable: %v", got, err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"HOME=" + testProcessHome,
		"CGO_ENABLED=0",
		"GOFLAGS=",
		"GOTOOLCHAIN=local",
		"GOTELEMETRY=off",
		"ARGS=build -trimpath -buildvcs=false -o " + got + " ./cmd/pfm",
	} {
		if !strings.Contains(string(data), part) {
			t.Fatalf("go build log %q missing %q", data, part)
		}
	}
}

func TestMockEngineBinaryBuildsWhenUnset(t *testing.T) {
	previous, hadPrevious := os.LookupEnv(paths.EnvTestMockEngineBinary)
	if err := os.Unsetenv(paths.EnvTestMockEngineBinary); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var err error
		if hadPrevious {
			err = os.Setenv(paths.EnvTestMockEngineBinary, previous)
		} else {
			err = os.Unsetenv(paths.EnvTestMockEngineBinary)
		}
		if err != nil {
			t.Errorf("restore %s: %v", paths.EnvTestMockEngineBinary, err)
		}
	})
	goDir := t.TempDir()
	log := filepath.Join(t.TempDir(), "go.log")
	stub := `#!/bin/sh
printf '%s\n' "$*" > "$TEST_GO_BUILD_LOG"
while [ "$1" != -o ]; do shift; done
shift
printf '#!/bin/sh\nexit 0\n' > "$1"
chmod +x "$1"
`
	if err := WriteExecutable(filepath.Join(goDir, "go"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", goDir+":"+os.Getenv("PATH"))
	t.Setenv("TEST_GO_BUILD_LOG", log)
	dir := t.TempDir()
	got, err := MockEngineBinary(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "mock-engine"); got != want {
		t.Fatalf("MockEngineBinary() = %q, want %q", got, want)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "build -trimpath -buildvcs=false -o " + got + " ./cmd/mock-engine"
	if !strings.Contains(string(data), want) {
		t.Fatalf("go build log %q missing %q", data, want)
	}
}

func TestGoBuildReportsCombinedOutput(t *testing.T) {
	goDir := t.TempDir()
	if err := WriteExecutable(
		filepath.Join(
			goDir,
			"go",
		),
		[]byte("#!/bin/sh\necho 'fixture compile output'\necho 'fixture compile error' >&2\nexit 41\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", goDir+":"+os.Getenv("PATH"))
	err := GoBuild(t.TempDir(), filepath.Join(t.TempDir(), "pfm"), "./cmd/pfm")
	if got, want := rooted(
		t,
		fmt.Sprint(err),
	), "go build ./cmd/pfm: exit status 41: fixture compile error\nfixture compile output\n"; got != want {
		t.Fatalf("GoBuild error = %q, want %q", got, want)
	}
}

func TestGoBuildReportsStartFailure(t *testing.T) {
	goDir := t.TempDir()
	if err := WriteExecutable(filepath.Join(goDir, "go"), []byte("#!/missing-test-interpreter\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", goDir+":"+os.Getenv("PATH"))
	const target = "./cmd/pfm"
	err := GoBuild(t.TempDir(), filepath.Join(t.TempDir(), "pfm"), target)
	var pathErr *os.PathError
	if got, want := rooted(
		t,
		fmt.Sprint(err),
	), "go build ./cmd/pfm: run \"{root}/001/go\": fork/exec {root}/001/go: no such file or directory"; got != want ||
		!errors.As(err, &pathErr) {
		t.Fatalf("GoBuild error = %q (a path error: %t), want %q", got, errors.As(err, &pathErr), want)
	}
}
