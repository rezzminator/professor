package harvestpy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

var ErrOfflineUnavailable = errors.New("harvestpy input is unavailable offline")

const incompleteMarkerName = "INCOMPLETE"

var errProvisioningIncomplete = errors.New("harvestpy provisioning did not finish")

// DownloadFunc is injected by tests/build tooling; the default is an atomic
// HTTP downloader with no shell interpolation.
type DownloadFunc func(context.Context, string, string) error

// RunFunc executes uv commands.  The working directory is explicit and the
// executable/arguments are never passed through a shell.
type RunFunc func(context.Context, string, []string, string) ([]byte, error)

// SmokeFunc verifies that one provisioned runtime can start the converter and
// complete its no-download smoke protocol.
type SmokeFunc func(context.Context, Runtime) (map[string]any, error)

type ProvisionOptions struct {
	Root     string
	Cache    string
	Platform Platform
	Offline  bool
	Download DownloadFunc
	Run      RunFunc
	Smoke    SmokeFunc
}

type ProvisionResult struct {
	Digest      string
	Environment EnvironmentDigest
	Runtime     Runtime
}

// InstallPlan is the read-only, machine-renderable price of a pinned target.
// PackageDownloadBytes is cold-cache measured for linux-amd64 and computed from
// the exact pinned artifact set for the other supported targets; blocked targets
// carry an explicit reason instead of an estimate.
type InstallPlan struct {
	Platform              string   `json:"platform"`
	PythonVersion         string   `json:"python_version"`
	UVVersion             string   `json:"uv_version"`
	PythonURL             string   `json:"python_url"`
	PythonSHA256          string   `json:"python_sha256"`
	PythonDownloadBytes   int64    `json:"python_download_bytes"`
	UVURL                 string   `json:"uv_url"`
	UVSHA256              string   `json:"uv_sha256"`
	UVDownloadBytes       int64    `json:"uv_download_bytes"`
	LockSHA256            string   `json:"lock_sha256"`
	SourceSHA256          string   `json:"source_sha256"`
	PackageDownloadBytes  int64    `json:"package_download_bytes"`
	PackageDownloadStatus string   `json:"package_download_status"`
	PackageBlockers       []string `json:"package_blockers,omitempty"`
	EnvironmentBytes      int64    `json:"environment_bytes"`
}

// Plan returns pinned URLs/hashes and measured/reported size fields without
// touching disk or network.
func Plan(platform Platform) (InstallPlan, error) {
	if platform.GOOS == "" {
		platform.GOOS, platform.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	target, ok := immutableTargets[platform]
	if !ok {
		return InstallPlan{}, fmt.Errorf("harvestpy has no pinned target for %s", platform)
	}
	packageBytes, packageStatus, blockers := packagePlan(platform)
	return InstallPlan{
		Platform: platform.String(), PythonVersion: target.PythonVersion, UVVersion: target.UVVersion,
		PythonURL: target.Python.URL, PythonSHA256: target.Python.SHA256, PythonDownloadBytes: target.Python.Size,
		UVURL: target.UV.URL, UVSHA256: target.UV.SHA256, UVDownloadBytes: target.UV.Size,
		LockSHA256: lockSHA256(), SourceSHA256: sourceSHA256(), PackageDownloadBytes: packageBytes,
		PackageDownloadStatus: packageStatus, PackageBlockers: blockers,
		EnvironmentBytes: environmentBytes(platform),
	}, nil
}

func environmentBytes(platform Platform) int64 {
	if platform == (Platform{GOOS: "linux", GOARCH: "amd64"}) {
		return 5786939761
	}
	return -1
}

func packagePlan(platform Platform) (int64, string, []string) {
	switch platform {
	case Platform{GOOS: "linux", GOARCH: "amd64"}:
		return 3106174573, "cold-cache-download", nil
	case Platform{GOOS: "linux", GOARCH: "arm64"}:
		return 3179527419, "pinned-lock-artifact-sum", nil
	case Platform{GOOS: "darwin", GOARCH: "arm64"}:
		return 389353114, "pinned-lock-artifact-sum", nil
	case Platform{GOOS: "darwin", GOARCH: "amd64"}:
		return -1, "blocked-exact-lock", []string{
			"torch==2.12.1 has no compatible darwin-amd64 wheel or source",
			"torchvision==0.27.1 has no compatible darwin-amd64 wheel or source",
			"onnxruntime==1.27.0 has no compatible darwin-amd64 wheel or source",
		}
	default:
		return -1, "unmeasured-target", nil
	}
}

// RuntimeRoot is the stable current pointer consumed by installer/doctor.
func RuntimeRoot(root string, platform Platform) string {
	return filepath.Join(root, "env", platform.String(), "current")
}

// Provision downloads/verifies inputs, converges an isolated environment at
// its final versioned path, runs two no-download smokes, then atomically
// publishes the current pointer.
func Provision(ctx context.Context, options ProvisionOptions) (ProvisionResult, error) {
	return provision(ctx, options, immutableTargets)
}

func provision(ctx context.Context, options ProvisionOptions, targets map[Platform]Target) (ProvisionResult, error) {
	if options.Root == "" {
		return ProvisionResult{}, errors.New("harvestpy provision root is empty")
	}
	platform := options.Platform
	if platform.GOOS == "" {
		platform.GOOS, platform.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	target, ok := targets[platform]
	if !ok {
		return ProvisionResult{}, fmt.Errorf("harvestpy has no pinned target for %s", platform)
	}
	if options.Cache == "" {
		options.Cache = filepath.Join(options.Root, "cache")
	}
	if options.Run == nil {
		options.Run = runCommand
	}
	if options.Smoke == nil {
		options.Smoke = smokeRuntime
	}
	if err := os.MkdirAll(options.Cache, 0o700); err != nil {
		return ProvisionResult{}, fmt.Errorf("create harvestpy cache: %w", err)
	}
	base := EnvironmentDigest{
		Schema: 1, Target: platform.String(), Python: target.PythonVersion, UV: target.UVVersion,
		PythonSHA256: target.Python.SHA256, UVSHA256: target.UV.SHA256,
		LockSHA256: lockSHA256(), SourceSHA256: sourceSHA256(),
		Features: FeatureStatus{OCR: "disabled", Layout: "disabled", Models: "not-requested"},
	}
	desired := digestID(base)
	base.Digest = desired
	current := RuntimeRoot(options.Root, platform)
	if existing, err := ReadEnvironmentDigest(
		filepath.Join(current, "environment.json"),
	); err == nil && existing.Digest == desired &&
		existing.State == "ready" {
		if _, checkErr := Check(ctx, options.Root, platform); checkErr == nil {
			return ProvisionResult{
				Digest:      desired,
				Environment: existing,
				Runtime: Runtime{
					Python: filepath.Join(current, "project", ".venv", "bin", "python"),
					Script: filepath.Join(current, "project", "converter.py"),
				},
			}, nil
		}
	}
	uvArchive := filepath.Join(options.Cache, "uv-"+platform.String()+".tar.gz")
	pythonArchive := filepath.Join(options.Cache, "python-"+platform.String()+".tar.gz")
	if err := ensureInput(ctx, uvArchive, target.UV, options.Offline, options.Download); err != nil {
		return ProvisionResult{}, fmt.Errorf("prepare harvestpy uv input: %w", err)
	}
	if err := ensureInput(ctx, pythonArchive, target.Python, options.Offline, options.Download); err != nil {
		return ProvisionResult{}, fmt.Errorf("prepare harvestpy Python input: %w", err)
	}
	envRoot := filepath.Join(options.Root, "env", platform.String())
	if err := os.MkdirAll(envRoot, 0o700); err != nil {
		return ProvisionResult{}, fmt.Errorf("create harvestpy environment root: %w", err)
	}
	final := filepath.Join(envRoot, desired)
	backup := ""
	if _, err := os.Stat(final); err == nil {
		backup = final + fmt.Sprintf(".repair-%d", time.Now().UnixNano())
		if err := os.Rename(final, backup); err != nil {
			return ProvisionResult{}, fmt.Errorf("quarantine invalid harvestpy versioned environment: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ProvisionResult{}, fmt.Errorf("inspect harvestpy versioned environment: %w", err)
	}
	staging := final
	restoreOld := func() {
		_ = os.RemoveAll(staging)
		if backup != "" {
			_ = os.Rename(backup, final)
		}
	}
	finished := false
	defer func() {
		if !finished {
			restoreOld()
		}
	}()
	if err := os.Mkdir(staging, 0o700); err != nil {
		return ProvisionResult{}, fmt.Errorf("create harvestpy versioned environment: %w", err)
	}
	if err := writePrivate(
		filepath.Join(staging, incompleteMarkerName),
		[]byte(errProvisioningIncomplete.Error()+"\n"),
	); err != nil {
		return ProvisionResult{}, fmt.Errorf("mark harvestpy environment incomplete: %w", err)
	}
	project := filepath.Join(staging, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		return ProvisionResult{}, fmt.Errorf("create harvestpy project: %w", err)
	}
	if err := writePrivate(filepath.Join(project, "converter.py"), ConverterSource()); err != nil {
		return ProvisionResult{}, err
	}
	if err := writePrivate(filepath.Join(project, "pyproject.toml"), ProjectMetadata()); err != nil {
		return ProvisionResult{}, err
	}
	if err := writePrivate(filepath.Join(project, "uv.lock"), LockMetadata()); err != nil {
		return ProvisionResult{}, err
	}
	uvPath := filepath.Join(staging, "uv")
	if err := extractNamedBinary(uvArchive, "uv", uvPath); err != nil {
		return ProvisionResult{}, fmt.Errorf("extract harvestpy uv: %w", err)
	}
	// The standalone archive already carries its top-level `python/` tree;
	// extract into the final version root so the published layout is
	// <digest>/python/{BUILD,bin,lib,...}, not a double python/python nesting.
	pythonPath, err := extractPython(pythonArchive, staging)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("extract harvestpy Python: %w", err)
	}
	if err := stampPythonBuild(filepath.Join(staging, "python", "BUILD"), target.PythonVersion); err != nil {
		return ProvisionResult{}, fmt.Errorf("stamp harvestpy Python build: %w", err)
	}
	if err := checkPythonBuild(filepath.Join(staging, "python", "BUILD"), target.PythonVersion); err != nil {
		return ProvisionResult{}, fmt.Errorf("verify harvestpy Python build: %w", err)
	}
	args := []string{"sync", "--frozen", "--no-install-project", "--project", project, "--python", pythonPath}
	if options.Offline {
		args = append(args, "--offline")
	}
	if _, err := options.Run(ctx, uvPath, args, project); err != nil {
		return ProvisionResult{}, fmt.Errorf("install harvestpy locked environment: %w", err)
	}
	venvPython := filepath.Join(project, ".venv", "bin", "python")
	if _, err := os.Stat(venvPython); err != nil {
		return ProvisionResult{}, fmt.Errorf("harvestpy uv sync did not create Python environment: %w", err)
	}
	if _, err := options.Run(ctx, uvPath, []string{"pip", "check", "--python", venvPython}, project); err != nil {
		return ProvisionResult{}, fmt.Errorf("harvestpy locked dependency check failed: %w", err)
	}
	inventoryOutput, err := options.Run(
		ctx,
		uvPath,
		[]string{"pip", "list", "--format", "freeze", "--python", venvPython},
		project,
	)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("harvestpy installed inventory failed: %w", err)
	}
	inventorySHA, inventoryCount, err := inventoryDigest(inventoryOutput)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("harvestpy installed inventory is invalid: %w", err)
	}
	base.InventorySHA256 = inventorySHA
	base.InventoryCount = inventoryCount
	smoke, err := options.Smoke(ctx, Runtime{Python: venvPython, Script: filepath.Join(project, "converter.py")})
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("harvestpy no-download smoke: %w", err)
	}
	imports, ok := smoke["imports"].(map[string]any)
	if !ok {
		return ProvisionResult{}, fmt.Errorf("harvestpy smoke omitted structured imports: %#v", smoke)
	}
	conversion, ok := smoke["conversion"].(map[string]any)
	if !ok {
		return ProvisionResult{}, fmt.Errorf("harvestpy smoke omitted live conversion result: %#v", smoke)
	}
	if converted, _ := conversion["ok"].(bool); !converted {
		return ProvisionResult{}, fmt.Errorf("harvestpy smoke live conversion failed: %#v", conversion)
	}
	base.Imports = imports
	base.State = "ready"
	base.Environment = final
	// The environment is never renamed after uv sync; both smokes judge the
	// same final runtime path.
	finalRuntime := Runtime{
		Python: filepath.Join(final, "project", ".venv", "bin", "python"),
		Script: filepath.Join(final, "project", "converter.py"),
	}
	_, smokeErr := options.Smoke(ctx, finalRuntime)
	if smokeErr != nil {
		return ProvisionResult{}, fmt.Errorf("harvestpy post-publish smoke: %w", smokeErr)
	}
	if err := os.Remove(filepath.Join(final, incompleteMarkerName)); err != nil {
		return ProvisionResult{}, fmt.Errorf("complete harvestpy environment: %w", err)
	}
	base.Sizes = measureTree(final)
	marker, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("marshal harvestpy environment digest: %w", err)
	}
	if err := writePrivate(filepath.Join(final, "environment.json"), append(marker, '\n')); err != nil {
		return ProvisionResult{}, err
	}
	if err := atomicCurrent(envRoot, desired); err != nil {
		return ProvisionResult{}, err
	}
	if backup != "" {
		if err := os.RemoveAll(backup); err != nil {
			return ProvisionResult{}, fmt.Errorf("remove quarantined harvestpy environment: %w", err)
		}
	}
	finished = true
	return ProvisionResult{Digest: desired, Environment: base, Runtime: Runtime{
		Python: filepath.Join(current, "project", ".venv", "bin", "python"),
		Script: filepath.Join(current, "project", "converter.py"),
	}}, nil
}

func smokeRuntime(ctx context.Context, runtime Runtime) (map[string]any, error) {
	converter := NewConverter(runtime)
	result, err := converter.Smoke(ctx)
	_ = converter.Close()
	return result, err
}

// Inspect reads the current machine-readable environment record without
// executing a converter.
func Inspect(root string, platform Platform) (EnvironmentDigest, error) {
	if platform.GOOS == "" {
		platform.GOOS, platform.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	if incomplete, found, err := findIncompleteEnvironment(root, platform); err != nil {
		return EnvironmentDigest{}, err
	} else if found {
		return incomplete, fmt.Errorf("%w: %s", errProvisioningIncomplete, incomplete.Environment)
	}
	return ReadEnvironmentDigest(filepath.Join(RuntimeRoot(root, platform), "environment.json"))
}

func findIncompleteEnvironment(root string, platform Platform) (EnvironmentDigest, bool, error) {
	envRoot := filepath.Join(root, "env", platform.String())
	entries, err := os.ReadDir(envRoot)
	if errors.Is(err, os.ErrNotExist) {
		return EnvironmentDigest{}, false, nil
	}
	if err != nil {
		return EnvironmentDigest{}, false, fmt.Errorf(
			"inspect harvestpy environment root for incomplete provisioning: %w",
			err,
		)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.Contains(entry.Name(), ".repair-") {
			continue
		}
		environment := filepath.Join(envRoot, entry.Name())
		marker := filepath.Join(environment, incompleteMarkerName)
		info, err := os.Lstat(marker)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return EnvironmentDigest{}, false, fmt.Errorf("inspect harvestpy incomplete marker %s: %w", marker, err)
		}
		if !info.Mode().IsRegular() {
			return EnvironmentDigest{}, false, fmt.Errorf(
				"harvestpy incomplete marker is not a regular file: %s",
				marker,
			)
		}
		return EnvironmentDigest{
			Target: platform.String(), Environment: environment,
			Digest: entry.Name(), State: "incomplete",
		}, true, nil
	}
	return EnvironmentDigest{}, false, nil
}

func ensureInput(ctx context.Context, path string, input Artifact, offline bool, download DownloadFunc) error {
	if _, err := os.Stat(path); err == nil {
		if err := VerifySHA256(path, input.SHA256); err == nil {
			return nil
		} else if offline {
			return fmt.Errorf("cached input failed SHA-256 verification: %w: %w", ErrOfflineUnavailable, err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove corrupt cached input: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect cached input: %w", err)
	} else if offline {
		return ErrOfflineUnavailable
	}
	if download == nil {
		download = func(ctx context.Context, url, destination string) error {
			return downloadFile(ctx, url, destination, input.Size)
		}
	}
	staging := path + fmt.Sprintf(".download-%d", time.Now().UnixNano())
	defer os.Remove(staging)
	if err := download(ctx, input.URL, staging); err != nil {
		return fmt.Errorf("download %s: %w", input.URL, err)
	}
	if info, err := os.Stat(staging); err != nil {
		return fmt.Errorf("stat downloaded input: %w", err)
	} else if input.Size > 0 && info.Size() != input.Size {
		return fmt.Errorf("downloaded input size mismatch: got %d want %d", info.Size(), input.Size)
	}
	if err := VerifySHA256(staging, input.SHA256); err != nil {
		return fmt.Errorf("verify downloaded input: %w", err)
	}
	if err := os.Rename(staging, path); err != nil {
		return fmt.Errorf("publish downloaded input: %w", err)
	}
	return nil
}

func downloadFile(ctx context.Context, url, path string, expectedSize int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("download returned HTTP %s", response.Status)
	}
	if expectedSize > 0 && response.ContentLength > expectedSize {
		return fmt.Errorf("download content length %d exceeds pinned size %d", response.ContentLength, expectedSize)
	}
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return fmt.Errorf("create download staging file: %w", err)
	}
	reader := io.Reader(response.Body)
	if expectedSize > 0 {
		reader = io.LimitReader(response.Body, expectedSize+1)
	}
	written, err := io.Copy(output, reader)
	if err != nil {
		_ = output.Close()
		return fmt.Errorf("copy download: %w", err)
	}
	if expectedSize > 0 && written != expectedSize {
		_ = output.Close()
		return fmt.Errorf("downloaded %d bytes, want pinned %d", written, expectedSize)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close download: %w", err)
	}
	return nil
}

func VerifySHA256(path, expected string) error {
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s for SHA-256: %w", path, err)
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("SHA-256 mismatch for %s: got %s want %s", path, actual, expected)
	}
	return nil
}

func writePrivate(path string, body []byte) error {
	staging, err := os.CreateTemp(filepath.Dir(path), ".asset-")
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	name := staging.Name()
	defer os.Remove(name)
	if err := staging.Chmod(0o600); err != nil {
		_ = staging.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if _, err := staging.Write(body); err != nil {
		_ = staging.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := staging.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("publish %s: %w", path, err)
	}
	return nil
}

func atomicCurrent(root, desired string) error {
	temporary := filepath.Join(root, ".current-") + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := os.Symlink(desired, temporary); err != nil {
		return fmt.Errorf("stage harvestpy current pointer: %w", err)
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, filepath.Join(root, "current")); err != nil {
		return fmt.Errorf("publish harvestpy current pointer: %w", err)
	}
	return nil
}

func extractNamedBinary(path, name, destination string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	archive := tar.NewReader(gzipReader)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(header.Name) != name || !header.FileInfo().Mode().IsRegular() {
			continue
		}
		if err := copyLimited(destination, archive, header.Size); err != nil {
			return err
		}
		return os.Chmod(destination, 0o700)
	}
	return fmt.Errorf("binary %q not found in archive", name)
}

func extractPython(path, destination string) (string, error) {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", fmt.Errorf("create Python extraction root: %w", err)
	}
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return "", err
	}
	defer gzipReader.Close()
	archive := tar.NewReader(gzipReader)
	var total int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		name, err := safeArchiveName(header.Name)
		if err != nil {
			return "", err
		}
		destinationPath := filepath.Join(destination, filepath.FromSlash(name))
		if header.Typeflag == tar.TypeXGlobalHeader || header.Typeflag == tar.TypeXHeader ||
			header.Typeflag == tar.TypeGNULongName ||
			header.Typeflag == tar.TypeGNULongLink {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := ensureArchiveParentSafe(destination, destinationPath); err != nil {
				return "", err
			}
			if err := os.MkdirAll(destinationPath, header.FileInfo().Mode().Perm()); err != nil {
				return "", fmt.Errorf("create Python directory %s: %w", name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > 512<<20 || total > 2<<30-header.Size {
				return "", fmt.Errorf("Python archive is too large at %d bytes", total+header.Size)
			}
			if err := ensureArchiveParentSafe(destination, destinationPath); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
				return "", fmt.Errorf("create Python parent directory: %w", err)
			}
			if err := copyLimited(destinationPath, archive, header.Size); err != nil {
				return "", fmt.Errorf("extract Python file %s: %w", name, err)
			}
			total += header.Size
			if err := os.Chmod(destinationPath, header.FileInfo().Mode().Perm()); err != nil {
				return "", fmt.Errorf("preserve Python file mode %s: %w", name, err)
			}
		case tar.TypeSymlink:
			target, err := safeSymlinkTarget(name, header.Linkname)
			if err != nil {
				return "", fmt.Errorf("unsafe Python symlink %s: %w", name, err)
			}
			if err := ensureArchiveParentSafe(destination, destinationPath); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
				return "", err
			}
			if err := os.Symlink(target, destinationPath); err != nil {
				return "", fmt.Errorf("extract Python symlink %s: %w", name, err)
			}
		case tar.TypeLink:
			target, err := safeArchiveName(header.Linkname)
			if err != nil {
				return "", fmt.Errorf("unsafe Python hardlink %s: %w", name, err)
			}
			if err := ensureArchiveParentSafe(destination, destinationPath); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
				return "", err
			}
			if err := os.Link(filepath.Join(destination, filepath.FromSlash(target)), destinationPath); err != nil {
				return "", fmt.Errorf("extract Python hardlink %s: %w", name, err)
			}
		default:
			return "", fmt.Errorf("unsupported Python archive entry %s (type %d)", name, header.Typeflag)
		}
	}
	for _, relative := range []string{"python/bin/python3", "python/bin/python"} {
		candidate := filepath.Join(destination, filepath.FromSlash(relative))
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", errors.New("Python executable not found in extracted archive")
}

func safeArchiveName(name string) (string, error) {
	name = filepath.ToSlash(name)
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || clean != strings.TrimSuffix(name, "/") || strings.HasPrefix(clean, "../") ||
		strings.Contains(clean, "/../") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return strings.TrimSuffix(clean, "/"), nil
}

func safeSymlinkTarget(entry, linkname string) (string, error) {
	linkname = filepath.ToSlash(linkname)
	if linkname == "" || strings.HasPrefix(linkname, "/") || strings.ContainsRune(linkname, 0) {
		return "", fmt.Errorf("unsafe symlink target %q", linkname)
	}
	resolved := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(entry), linkname)))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("symlink target escapes archive root: %q", linkname)
	}
	return filepath.ToSlash(filepath.Clean(linkname)), nil
}

func ensureArchiveParentSafe(root, path string) error {
	relative, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive path escapes extraction root: %s", path)
	}
	current := root
	if info, err := os.Lstat(current); err != nil || !info.IsDir() {
		if err != nil {
			return fmt.Errorf("inspect extraction root: %w", err)
		}
		return fmt.Errorf("extraction root is not a directory: %s", root)
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect archive parent %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive parent is a symlink: %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("archive parent is not a directory: %s", current)
		}
	}
	return nil
}

func copyLimited(destination string, source io.Reader, size int64) error {
	if size < 0 || size > 512<<20 {
		return fmt.Errorf("archive file size %d exceeds limit", size)
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	written, err := io.Copy(output, io.LimitReader(source, size))
	if err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return err
	}
	if written != size {
		_ = output.Close()
		_ = os.Remove(destination)
		return fmt.Errorf("archive entry ended at %d bytes, want %d", written, size)
	}
	return output.Close()
}

func runCommand(ctx context.Context, executable string, arguments []string, directory string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = directory
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		return stdout.Bytes(), fmt.Errorf(
			"%s %s: %w (stderr: %s)",
			executable,
			strings.Join(arguments, " "),
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	return stdout.Bytes(), nil
}

func measureTree(root string) SizeReport {
	var report SizeReport
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.Mode().IsRegular() {
			report.EnvironmentBytes += info.Size()
			report.EnvironmentFiles++
		}
		return nil
	})
	return report
}

func stampPythonBuild(path, version string) error {
	parts := strings.SplitN(version, "+", 2)
	if len(parts) != 2 || parts[1] == "" {
		return fmt.Errorf("Python pin has no standalone build stamp: %q", version)
	}
	want := []byte(parts[1] + "\n")
	if body, err := os.ReadFile(path); err == nil {
		if strings.TrimSpace(string(body)) != parts[1] {
			return fmt.Errorf("archive BUILD is %q, want %q", strings.TrimSpace(string(body)), parts[1])
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read archive BUILD: %w", err)
	}
	return writePrivate(path, want)
}

func inventoryDigest(output []byte) (string, int, error) {
	lines := make([]string, 0)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(string(output), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Count(line, "==") != 1 {
			return "", 0, fmt.Errorf("invalid installed distribution record %q", line)
		}
		parts := strings.SplitN(line, "==", 2)
		if parts[0] == "" || parts[1] == "" {
			return "", 0, fmt.Errorf("invalid installed distribution record %q", line)
		}
		canonical := strings.ToLower(strings.TrimSpace(parts[0])) + "==" + strings.TrimSpace(parts[1])
		if _, ok := seen[canonical]; ok {
			return "", 0, fmt.Errorf("duplicate installed distribution record %q", canonical)
		}
		seen[canonical] = struct{}{}
		lines = append(lines, canonical)
	}
	if len(lines) == 0 {
		return "", 0, errors.New("installed distribution inventory is empty")
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	return hex.EncodeToString(sum[:]), len(lines), nil
}
