package testjail

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestPFMBinaryUsesExecutablePrebuilt(t *testing.T) {
	prebuilt := filepath.Join(t.TempDir(), "pfm")
	if err := os.WriteFile(prebuilt, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTestPFMBinary, prebuilt)
	got, err := PFMBinary(t.TempDir(), t.TempDir())
	if err != nil || got != prebuilt {
		t.Fatalf("PFMBinary() = %q, %v; want %q", got, err, prebuilt)
	}
}

func TestPFMBinaryRejectsBrokenPrebuilt(t *testing.T) {
	nonExecutable := filepath.Join(t.TempDir(), "pfm")
	if err := os.WriteFile(nonExecutable, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "missing"), nonExecutable, t.TempDir()} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Setenv(paths.EnvTestPFMBinary, path)
			got, err := PFMBinary(t.TempDir(), t.TempDir())
			if got != "" || err == nil ||
				!strings.Contains(err.Error(), paths.EnvTestPFMBinary) || !strings.Contains(err.Error(), path) {
				t.Fatalf("PFMBinary() = %q, %v; want named error for %q", got, err, path)
			}
		})
	}
}

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
	if err := os.WriteFile(filepath.Join(goDir, "go"), []byte(stub), 0o700); err != nil {
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

func TestGoBuildReportsCombinedOutput(t *testing.T) {
	goDir := t.TempDir()
	if err := os.WriteFile(
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
	if err == nil || !strings.Contains(err.Error(), "exit status 41") ||
		!strings.Contains(err.Error(), "fixture compile error") ||
		!strings.Contains(err.Error(), "fixture compile output") ||
		strings.Index(err.Error(), "fixture compile error") > strings.Index(err.Error(), "fixture compile output") {
		t.Fatalf("GoBuild error = %v, want status 41 with stderr before stdout", err)
	}
}

func TestGoBuildReportsStartFailure(t *testing.T) {
	goDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(goDir, "go"), []byte("#!/missing-test-interpreter\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", goDir+":"+os.Getenv("PATH"))
	const target = "./cmd/pfm"
	err := GoBuild(t.TempDir(), filepath.Join(t.TempDir(), "pfm"), target)
	var pathErr *os.PathError
	if err == nil || !strings.Contains(err.Error(), "go build "+target) || !errors.As(err, &pathErr) {
		t.Fatalf("GoBuild error = %v, want named wrapped start failure", err)
	}
}

func TestGoBuildReportsResolveFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := GoBuild(t.TempDir(), filepath.Join(t.TempDir(), "pfm"), "./cmd/pfm")
	if err == nil || !strings.Contains(err.Error(), "resolve go for test build:") {
		t.Fatalf("GoBuild error = %v, want named resolution failure", err)
	}
}
