package harvestpy

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestCheckDependenciesAcceptsOnlyPinnedArm64SBSAFalsePositive(t *testing.T) {
	tests := []struct {
		name         string
		platform     Platform
		lockPin      bool
		wheelTag     string
		library      []byte
		message      string
		wantAccepted bool
	}{
		{
			name:         "exact pinned wheel",
			platform:     Platform{GOOS: goosLinux, GOARCH: goarchARM64},
			lockPin:      true,
			wheelTag:     "py3-none-manylinux2014_sbsa",
			library:      minimalELF(elf.EM_AARCH64),
			message:      "The package nvidia-cusparselt-cu13 was built for a different platform",
			wantAccepted: true,
		},
		{
			name:     "wrong platform",
			platform: Platform{GOOS: goosLinux, GOARCH: goarchAMD64},
			lockPin:  true,
			wheelTag: "py3-none-manylinux2014_sbsa",
			library:  minimalELF(elf.EM_AARCH64),
			message:  "The package nvidia-cusparselt-cu13 was built for a different platform",
		},
		{
			name:     "wrong package message",
			platform: Platform{GOOS: goosLinux, GOARCH: goarchARM64},
			lockPin:  true,
			wheelTag: "py3-none-manylinux2014_sbsa",
			library:  minimalELF(elf.EM_AARCH64),
			message:  "The package other-package was built for a different platform",
		},
		{
			name:     "missing lock pin",
			platform: Platform{GOOS: goosLinux, GOARCH: goarchARM64},
			wheelTag: "py3-none-manylinux2014_sbsa",
			library:  minimalELF(elf.EM_AARCH64),
			message:  "The package nvidia-cusparselt-cu13 was built for a different platform",
		},
		{
			name:     "wrong wheel tag",
			platform: Platform{GOOS: goosLinux, GOARCH: goarchARM64},
			lockPin:  true,
			wheelTag: "py3-none-manylinux2014_aarch64",
			library:  minimalELF(elf.EM_AARCH64),
			message:  "The package nvidia-cusparselt-cu13 was built for a different platform",
		},
		{
			name:     "missing library",
			platform: Platform{GOOS: goosLinux, GOARCH: goarchARM64},
			lockPin:  true,
			wheelTag: "py3-none-manylinux2014_sbsa",
			message:  "The package nvidia-cusparselt-cu13 was built for a different platform",
		},
		{
			name:     "wrong ELF machine",
			platform: Platform{GOOS: goosLinux, GOARCH: goarchARM64},
			lockPin:  true,
			wheelTag: "py3-none-manylinux2014_sbsa",
			library:  minimalELF(elf.EM_X86_64),
			message:  "The package nvidia-cusparselt-cu13 was built for a different platform",
		},
		{
			name:     "invalid ELF",
			platform: Platform{GOOS: goosLinux, GOARCH: goarchARM64},
			lockPin:  true,
			wheelTag: "py3-none-manylinux2014_sbsa",
			library:  []byte("not an ELF"),
			message:  "The package nvidia-cusparselt-cu13 was built for a different platform",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := arm64DependencyFixture(t, test.lockPin, test.wheelTag, test.library)
			uv := filepath.Join(root, "uv")
			runner := &deps.FakeRunner{}
			runner.Script(
				[]string{uv, "pip", "check"},
				deps.RunResult{ExitCode: 1, Stderr: []byte(test.message)},
				nil,
			)
			err := checkDependencies(context.Background(), runner, root, test.platform)
			if test.wantAccepted {
				if err != nil {
					t.Fatalf("pinned arm64 SBSA metadata false-positive was rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("non-exact pip-check failure was accepted")
			}
		})
	}
}

func TestCheckDependenciesRejectsArm64PinAcrossLockRecords(t *testing.T) {
	root := arm64DependencyFixture(t, true, "py3-none-manylinux2014_sbsa", minimalELF(elf.EM_AARCH64))
	lock := `[[package]]
name = "nvidia-cusparselt-cu13"
version = "0.8.0"

[[package]]
name = "other-package"
version = "0.8.1"

[[package]]
name = "another-package"
url = "manylinux2014_aarch64"
`
	if err := os.WriteFile(filepath.Join(root, "project", "uv.lock"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	runner.Script(
		[]string{filepath.Join(root, "uv"), "pip", "check"},
		deps.RunResult{
			ExitCode: 1,
			Stderr:   []byte("The package nvidia-cusparselt-cu13 was built for a different platform"),
		},
		nil,
	)
	if err := checkDependencies(
		context.Background(),
		runner,
		root,
		Platform{GOOS: goosLinux, GOARCH: goarchARM64},
	); err == nil {
		t.Fatal("arm64 false-positive accepted when lock fields came from separate package records")
	}
}

func TestCheckDependenciesRejectsArm64PinWithoutExactAarch64Wheel(t *testing.T) {
	root := arm64DependencyFixture(t, true, "py3-none-manylinux2014_sbsa", minimalELF(elf.EM_AARCH64))
	lock := `[[package]]
name = "nvidia-cusparselt-cu13"
version = "0.8.1"
dependencies = ["manylinux2014_aarch64"]
wheels = [
    { url = "https://example.invalid/nvidia_cusparselt_cu13-0.8.1-py3-none-manylinux2014_sbsa.whl" },
]
`
	if err := os.WriteFile(filepath.Join(root, "project", "uv.lock"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	runner.Script(
		[]string{filepath.Join(root, "uv"), "pip", "check"},
		deps.RunResult{
			ExitCode: 1,
			Stderr:   []byte("The package nvidia-cusparselt-cu13 was built for a different platform"),
		},
		nil,
	)
	if err := checkDependencies(
		context.Background(),
		runner,
		root,
		Platform{GOOS: goosLinux, GOARCH: goarchARM64},
	); err == nil {
		t.Fatal("arm64 false-positive accepted without the exact aarch64 wheel filename")
	}
}

func arm64DependencyFixture(t *testing.T, lockPin bool, wheelTag string, library []byte) string {
	t.Helper()
	root := t.TempDir()
	wheelInfo := filepath.Join(
		root,
		"project",
		".venv",
		"lib",
		"python3.11",
		"site-packages",
		"nvidia_cusparselt_cu13-0.8.1.dist-info",
	)
	if err := os.MkdirAll(wheelInfo, 0o700); err != nil {
		t.Fatal(err)
	}
	lock := "name = \"other-package\"\nurl = \"manylinux2014_x86_64\"\n"
	if lockPin {
		lock = "[[package]]\nname = \"nvidia-cusparselt-cu13\"\nversion = \"0.8.1\"\nwheels = [\n    { url = \"https://example.invalid/nvidia_cusparselt_cu13-0.8.1-py3-none-manylinux2014_aarch64.whl\" },\n]\n"
	}
	if err := os.WriteFile(filepath.Join(root, "project", "uv.lock"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(wheelInfo, "WHEEL"),
		[]byte("Wheel-Version: 1.0\nTag: "+wheelTag+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if library != nil {
		lib := filepath.Join(
			root,
			"project",
			".venv",
			"lib",
			"python3.11",
			"site-packages",
			"nvidia",
			"cusparselt",
			"lib",
		)
		if err := os.MkdirAll(lib, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lib, "libcusparseLt.so.0"), library, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func minimalELF(machine elf.Machine) []byte {
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F'})
	header[4] = byte(elf.ELFCLASS64)
	header[5] = byte(elf.ELFDATA2LSB)
	header[6] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(header[16:18], uint16(elf.ET_DYN))
	binary.LittleEndian.PutUint16(header[18:20], uint16(machine))
	binary.LittleEndian.PutUint32(header[20:24], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint16(header[52:54], 64)
	return header
}
