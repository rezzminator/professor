package harvestpy

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

type CheckStatus struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type CheckReport struct {
	Healthy bool                   `json:"healthy"`
	Digest  EnvironmentDigest      `json:"digest"`
	Checks  map[string]CheckStatus `json:"checks"`
}

// CheckConversionEnvironment verifies the pointer, marker, embedded assets,
// interpreter version, environment shape, and a live no-download conversion.
// Every failed check remains visible in the returned report; a failed report
// also returns an error so callers cannot mistake it for a healthy result.
func CheckConversionEnvironment(ctx context.Context, root string, platform Platform) (CheckReport, error) {
	return evaluateConversionEnvironment(ctx, root, platform, obs.Runner(deps.RealRunner{}))
}

// CheckConversionEnvironmentWithRunner is the process-seamed health check.
// The compatibility wrapper above retains the production default.
func CheckConversionEnvironmentWithRunner(
	ctx context.Context,
	root string,
	platform Platform,
	runner deps.Runner,
) (CheckReport, error) {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	return evaluateConversionEnvironment(ctx, root, platform, runner)
}

func evaluateConversionEnvironment(
	ctx context.Context,
	root string,
	platform Platform,
	runner deps.Runner,
) (CheckReport, error) {
	report := CheckReport{Checks: make(map[string]CheckStatus)}
	set := func(name string, err error) {
		if err == nil {
			report.Checks[name] = CheckStatus{OK: true}
		} else {
			report.Checks[name] = CheckStatus{Error: err.Error()}
		}
	}
	if platform.GOOS == "" {
		platform.GOOS, platform.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	current := RuntimeRoot(root, platform)
	linkTarget, err := os.Readlink(current)
	if err != nil {
		set("current_pointer", fmt.Errorf("read current pointer %s: %w", current, err))
	} else {
		resolved := linkTarget
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(current), resolved)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			set("current_pointer", err)
		} else {
			set("current_pointer", func() error {
				if filepath.Base(resolved) == "current" || filepath.Dir(resolved) != filepath.Dir(current) {
					return fmt.Errorf("current pointer resolves outside versioned target directory: %s", resolved)
				}
				if _, err := os.Stat(resolved); err != nil {
					return fmt.Errorf("current target missing: %w", err)
				}
				return nil
			}())
		}
	}
	digest, digestErr := InspectConversionEnvironment(root, platform)
	report.Digest = digest
	if digestErr != nil {
		set("marker", digestErr)
	} else {
		set("marker", nil)
		set("marker_state", func() error {
			if digest.State != provisionStateReady {
				return fmt.Errorf("environment state is %q, want ready", digest.State)
			}
			if digest.Target != platform.String() {
				return fmt.Errorf("environment target is %q, want %s", digest.Target, platform)
			}
			target, ok := immutableTargets[platform]
			if !ok {
				return fmt.Errorf("no immutable target pin for %s", platform)
			}
			if digest.PythonSHA256 != target.Python.SHA256 || digest.UVSHA256 != target.UV.SHA256 {
				return fmt.Errorf("environment artifact digests do not match immutable target pins")
			}
			return nil
		}())
		set("lock_hash", VerifySHA256(filepath.Join(current, "project", "uv.lock"), digest.LockSHA256))
		set("source_hash", VerifySHA256(filepath.Join(current, "project", "converter.py"), digest.SourceSHA256))
		set("project_metadata", compareFile(filepath.Join(current, "project", "pyproject.toml"), ProjectMetadata()))
		set("digest_integrity", verifyDigestIntegrity(digest))
		set("current_target", verifyCurrentTarget(current, digest))
		set(
			"interpreter",
			checkInterpreter(ctx, runner, filepath.Join(current, "project", ".venv", "bin", "python"), digest.Python),
		)
		set("interpreter_build", checkPythonBuild(filepath.Join(current, "python", "BUILD"), digest.Python))
		set("environment_shape", checkEnvironmentShape(current))
		set("dependency_check", checkDependencies(ctx, runner, current, platform))
		set("lock_completeness", checkInventory(ctx, runner, current, digest))
		if report.Checks["interpreter"].OK {
			smokeConverter := NewConverter(Runtime{
				Python: filepath.Join(current, "project", ".venv", "bin", "python"),
				Script: filepath.Join(current, "project", "converter.py"),
				Runner: runner,
			})
			smoke, smokeErr := smokeConverter.Smoke(ctx)
			_ = smokeConverter.Close()
			set("live_smoke", smokeErr)
			if smokeErr == nil {
				if converted, _ := smoke["conversion"].(map[string]any); converted == nil {
					set("live_smoke_conversion", errors.New("smoke omitted live conversion"))
				} else if ok, _ := converted["ok"].(bool); !ok {
					set("live_smoke_conversion", fmt.Errorf("smoke conversion failed: %#v", converted))
				} else {
					set("live_smoke_conversion", nil)
				}
			}
		} else {
			set("live_smoke", errors.New("interpreter check failed; smoke not attempted"))
		}
	}
	report.Healthy = true
	for _, status := range report.Checks {
		if !status.OK {
			report.Healthy = false
			break
		}
	}
	if !report.Healthy {
		return report, errors.New("harvestpy environment check failed")
	}
	return report, nil
}

func compareFile(path string, expected []byte) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !bytes.Equal(body, expected) {
		return fmt.Errorf("%s differs from embedded metadata", path)
	}
	return nil
}

func checkInterpreter(ctx context.Context, runner deps.Runner, path, wanted string) error {
	process := obs.NewProcess(ctx, "check")
	process.Started(0, nil)
	end := process.Request("version")
	result, err := runner.Run(ctx, []string{path, "--version"}, deps.RunOptions{})
	end(len(result.Stdout)+len(result.Stderr), err)
	process.Exited(recordedExitStatus(filepath.Base(path), result, err))
	if err != nil {
		return fmt.Errorf("run Python version: %w (output: %s)", err, interpreterOutput(result))
	}
	if result.ExitCode != 0 {
		return fmt.Errorf(
			"run Python version: exit %d (output: %s)",
			result.ExitCode,
			interpreterOutput(result),
		)
	}
	// Standalone Python reports its semantic version (3.11.15), while the
	// artifact build stamp (+20260610) is carried by the pinned target URL.
	// Compare the semantic portion here so a valid interpreter is not rejected
	// merely because --version omits the packaging suffix.
	semantic := strings.SplitN(wanted, "+", 2)[0]
	if !strings.Contains(string(result.Stdout), semantic) {
		return fmt.Errorf(
			"python version %q does not contain pinned %q",
			strings.TrimSpace(string(result.Stdout)),
			wanted,
		)
	}
	return nil
}

func interpreterOutput(result deps.RunResult) string {
	output := append(append([]byte(nil), result.Stdout...), result.Stderr...)
	return stderrTail(string(output))
}

func checkPythonBuild(path, wanted string) error {
	parts := strings.SplitN(wanted, "+", 2)
	if len(parts) != 2 || parts[1] == "" {
		return fmt.Errorf("pinned Python version has no standalone build stamp: %q", wanted)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read standalone Python BUILD %s: %w", path, err)
	}
	if strings.TrimSpace(string(body)) != parts[1] {
		return fmt.Errorf("standalone Python BUILD is %q, want %q", strings.TrimSpace(string(body)), parts[1])
	}
	return nil
}

func verifyDigestIntegrity(digest EnvironmentDigest) error {
	if digest.Digest == "" {
		return errors.New("environment digest is empty")
	}
	// Provision computes the identity before adding mutable state, sizes, and
	// smoke import details. Recreate exactly that canonical identity here.
	canonical := EnvironmentDigest{
		Schema: digest.Schema, Target: digest.Target, Python: digest.Python, UV: digest.UV,
		PythonSHA256: digest.PythonSHA256, UVSHA256: digest.UVSHA256,
		LockSHA256: digest.LockSHA256, SourceSHA256: digest.SourceSHA256, Features: digest.Features,
	}
	if got := digestID(canonical); got != digest.Digest {
		return fmt.Errorf("environment digest does not match pinned identity: got %s want %s", digest.Digest, got)
	}
	return nil
}

func verifyCurrentTarget(current string, digest EnvironmentDigest) error {
	if digest.Environment == "" {
		return errors.New("environment path is empty")
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return fmt.Errorf("resolve current target: %w", err)
	}
	want, err := filepath.Abs(digest.Environment)
	if err != nil {
		return fmt.Errorf("resolve recorded environment path: %w", err)
	}
	if canonical, symErr := filepath.EvalSymlinks(want); symErr == nil {
		want = canonical
	}
	if resolved != want {
		return fmt.Errorf("current target resolves to %s, recorded %s", resolved, want)
	}
	if filepath.Base(resolved) != digest.Digest {
		return fmt.Errorf("current target version %q does not equal digest %q", filepath.Base(resolved), digest.Digest)
	}
	return nil
}

func checkEnvironmentShape(root string) error {
	for _, relative := range []string{"uv", "python/BUILD", "project/converter.py", "project/pyproject.toml", "project/uv.lock", "project/.venv/bin/python"} {
		info, err := os.Stat(filepath.Join(root, relative))
		if err != nil {
			return fmt.Errorf("missing %s: %w", relative, err)
		}
		if relative != "project/.venv/bin/python" && !info.Mode().IsRegular() {
			return fmt.Errorf("environment entry %s is not a regular file", relative)
		}
	}
	return nil
}

func checkDependencies(ctx context.Context, runner deps.Runner, root string, platform Platform) error {
	uv := filepath.Join(root, "uv")
	python := filepath.Join(root, "project", ".venv", "bin", "python")
	if _, err := runCommandWithRunner(
		ctx,
		runner,
		uv,
		[]string{uvCommandPip, "check", uvFlagPython, python},
		filepath.Join(root, "project"),
	); err != nil {
		allowed, inspectErr := acceptPinnedArm64SBSAFailure(platform, root, err)
		if inspectErr != nil {
			return errors.Join(fmt.Errorf("uv pip check failed: %w", err), inspectErr)
		}
		if allowed {
			return nil
		}
		return fmt.Errorf("uv pip check failed: %w", err)
	}
	return nil
}

// acceptPinnedArm64SBSAFailure contains one upstream wheel metadata false-positive.
// nvidia-cusparselt-cu13 0.8.1 ships an aarch64 shared library but labels its
// wheel manylinux2014_sbsa, which uv reports as incompatible on linux-aarch64.
// The package is accepted only when the exact pinned lock, wheel metadata, and
// shared library are present; every other uv pip check failure remains fatal.
func acceptPinnedArm64SBSAFailure(platform Platform, root string, checkErr error) (bool, error) {
	if platform != (Platform{GOOS: goosLinux, GOARCH: goarchARM64}) {
		return false, nil
	}
	message := strings.ToLower(checkErr.Error())
	if !strings.Contains(message, "nvidia-cusparselt-cu13") ||
		!strings.Contains(message, "built for a different platform") {
		return false, nil
	}
	lock, err := os.ReadFile(filepath.Join(root, "project", "uv.lock"))
	if err != nil {
		return false, fmt.Errorf("read lock while checking pinned arm64 wheel exception: %w", err)
	}
	if !hasPinnedArm64PackageRecord(string(lock)) {
		return false, nil
	}
	pattern := filepath.Join(
		root,
		"project",
		".venv",
		"lib",
		"python*",
		"site-packages",
		"nvidia_cusparselt_cu13-0.8.1.dist-info",
		"WHEEL",
	)
	wheels, err := filepath.Glob(pattern)
	if err != nil {
		return false, fmt.Errorf("find pinned arm64 wheel metadata: %w", err)
	}
	if len(wheels) != 1 {
		return false, nil
	}
	wheel, err := os.ReadFile(wheels[0])
	if err != nil {
		return false, fmt.Errorf("read pinned arm64 wheel metadata %s: %w", wheels[0], err)
	}
	if !strings.Contains("\n"+string(wheel), "\nTag: py3-none-manylinux2014_sbsa\n") {
		return false, nil
	}
	libPath := filepath.Join(
		filepath.Dir(filepath.Dir(wheels[0])),
		"nvidia",
		"cusparselt",
		"lib",
		"libcusparseLt.so.0",
	)
	info, err := os.Stat(libPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect pinned arm64 wheel library %s: %w", libPath, err)
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	elfFile, err := elf.Open(libPath)
	if err != nil {
		return false, nil
	}
	machine := elfFile.Machine
	if err := elfFile.Close(); err != nil {
		return false, fmt.Errorf("close pinned arm64 wheel library %s: %w", libPath, err)
	}
	return machine == elf.EM_AARCH64, nil
}

func hasPinnedArm64PackageRecord(lock string) bool {
	for _, record := range strings.Split(lock, "[[package]]") {
		if !lockRecordHasLine(record, `name = "nvidia-cusparselt-cu13"`) ||
			!lockRecordHasLine(record, `version = "0.8.1"`) ||
			!lockRecordHasWheel(record, "nvidia_cusparselt_cu13-0.8.1-py3-none-manylinux2014_aarch64.whl") {
			continue
		}
		return true
	}
	return false
}

func lockRecordHasWheel(record, filename string) bool {
	const wheelsPrefix = "wheels = ["
	start := strings.Index(record, wheelsPrefix)
	if start < 0 {
		return false
	}
	body := record[start+len(wheelsPrefix):]
	end := strings.IndexByte(body, ']')
	if end < 0 {
		return false
	}
	for _, line := range strings.Split(body[:end], "\n") {
		const urlPrefix = `url = "`
		urlStart := strings.Index(line, urlPrefix)
		if urlStart < 0 {
			continue
		}
		urlStart += len(urlPrefix)
		urlEnd := strings.IndexByte(line[urlStart:], '"')
		if urlEnd < 0 {
			continue
		}
		url := line[urlStart : urlStart+urlEnd]
		if query := strings.IndexAny(url, "?#"); query >= 0 {
			url = url[:query]
		}
		if filepath.Base(url) == filename {
			return true
		}
	}
	return false
}

func lockRecordHasLine(record, want string) bool {
	for _, line := range strings.Split(record, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func checkInventory(ctx context.Context, runner deps.Runner, root string, expected EnvironmentDigest) error {
	uv := filepath.Join(root, "uv")
	python := filepath.Join(root, "project", ".venv", "bin", "python")
	output, err := runCommandWithRunner(
		ctx,
		runner,
		uv,
		[]string{uvCommandPip, uvCommandList, uvFlagFormat, uvListFormatFreeze, uvFlagPython, python},
		filepath.Join(root, "project"),
	)
	if err != nil {
		return fmt.Errorf("read installed distribution inventory: %w", err)
	}
	got, count, err := inventoryDigest(output)
	if err != nil {
		return fmt.Errorf("decode installed distribution inventory: %w", err)
	}
	if expected.InventorySHA256 == "" || got != expected.InventorySHA256 || count != expected.InventoryCount {
		return fmt.Errorf(
			"installed inventory differs from provisioned lock: got %s/%d want %s/%d",
			got,
			count,
			expected.InventorySHA256,
			expected.InventoryCount,
		)
	}
	return nil
}
