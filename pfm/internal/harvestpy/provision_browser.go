package harvestpy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// BrowserRuntimeRoot is the stable current pointer for the opt-in real-browser
// environment. It lives under a DISTINCT root suffix ("env-browser") so the
// conversion environment's digest and directory are never touched by browser
// provisioning — adding patchright to the conversion lock would invalidate the
// 5.79 GB Docling/Torch closure on every host.
func BrowserRuntimeRoot(root string, platform Platform) string {
	return filepath.Join(root, "env-browser", platform.String(), "current")
}

// ProvisionBrowser converges the opt-in real-browser environment lazily: it
// reuses the pinned uv + CPython toolchain from targets.json (no new
// toolchain downloads), installs ONLY the embedded browser lock, and NEVER
// downloads Chromium — patchright drives system Chrome via channel="chrome".
// The conversion environment is not touched.
func ProvisionBrowser(ctx context.Context, options ProvisionOptions) (ProvisionResult, error) {
	return provisionBrowser(ctx, options, immutableTargets)
}

func provisionBrowser(
	ctx context.Context,
	options ProvisionOptions,
	targets map[Platform]Target,
) (ProvisionResult, error) {
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
		options.Smoke = smokeBrowserRuntime
	}
	if err := os.MkdirAll(options.Cache, 0o700); err != nil {
		return ProvisionResult{}, fmt.Errorf("create harvestpy cache: %w", err)
	}
	base := EnvironmentDigest{
		Schema: 1, Target: platform.String(), Python: target.PythonVersion, UV: target.UVVersion,
		PythonSHA256: target.Python.SHA256, UVSHA256: target.UV.SHA256,
		LockSHA256: browserLockSHA256(), SourceSHA256: browserSourceSHA256(),
		Features: FeatureStatus{OCR: "disabled", Layout: "disabled", Models: "not-requested"},
	}
	desired := digestID(base)
	base.Digest = desired
	current := BrowserRuntimeRoot(options.Root, platform)
	envRoot := filepath.Join(options.Root, "env-browser", platform.String())
	if existing, err := ReadEnvironmentDigest(
		filepath.Join(current, "environment.json"),
	); err == nil && existing.Digest == desired &&
		existing.State == "ready" {
		browserRuntime := Runtime{
			Python: filepath.Join(current, "project", ".venv", "bin", "python"),
			Script: filepath.Join(current, "project", "browser.py"),
		}
		if _, smokeErr := options.Smoke(ctx, browserRuntime); smokeErr == nil {
			return ProvisionResult{Digest: desired, Environment: existing, Runtime: browserRuntime}, nil
		}
	}
	uvArchive := filepath.Join(options.Cache, "uv-"+platform.String()+".tar.gz")
	pythonArchive := filepath.Join(options.Cache, "python-"+platform.String()+".tar.gz")
	if err := ensureInput(ctx, uvArchive, target.UV, options.Offline, options.Download); err != nil {
		return ProvisionResult{}, fmt.Errorf("prepare browser uv input: %w", err)
	}
	if err := ensureInput(ctx, pythonArchive, target.Python, options.Offline, options.Download); err != nil {
		return ProvisionResult{}, fmt.Errorf("prepare browser Python input: %w", err)
	}
	if err := os.MkdirAll(envRoot, 0o700); err != nil {
		return ProvisionResult{}, fmt.Errorf("create browser environment root: %w", err)
	}
	final := filepath.Join(envRoot, desired)
	if _, err := os.Stat(final); err == nil {
		// A stale or broken environment at the exact digest path is replaced
		// wholesale; there is nothing inside worth keeping.
		if err := os.RemoveAll(final); err != nil {
			return ProvisionResult{}, fmt.Errorf("clear stale browser environment: %w", err)
		}
	}
	// Build DIRECTLY at the final path, like the conversion provisioner:
	// uv records .venv/bin/python as an absolute symlink into the extracted
	// interpreter, so a staging→final rename would dangle every link.
	defer func() {
		// A failed provision leaves nothing behind: honest absence
		// (NOT_PROVISIONED) instead of a half-built tree.
		if _, statErr := os.Stat(filepath.Join(final, "environment.json")); errors.Is(statErr, os.ErrNotExist) {
			_ = os.RemoveAll(final)
		}
	}()
	if err := os.Mkdir(final, 0o700); err != nil {
		return ProvisionResult{}, fmt.Errorf("create browser environment: %w", err)
	}
	project := filepath.Join(final, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		return ProvisionResult{}, fmt.Errorf("create browser project: %w", err)
	}
	if err := writePrivate(filepath.Join(project, "browser.py"), BrowserWorkerSource()); err != nil {
		return ProvisionResult{}, err
	}
	if err := writePrivate(filepath.Join(project, "pyproject.toml"), BrowserProjectMetadata()); err != nil {
		return ProvisionResult{}, err
	}
	if err := writePrivate(filepath.Join(project, "uv.lock"), BrowserLockMetadata()); err != nil {
		return ProvisionResult{}, err
	}
	uvPath := filepath.Join(final, "uv")
	if err := extractNamedBinary(uvArchive, "uv", uvPath); err != nil {
		return ProvisionResult{}, fmt.Errorf("extract browser uv: %w", err)
	}
	pythonPath, err := extractPython(pythonArchive, final)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("extract browser Python: %w", err)
	}
	if err := stampPythonBuild(filepath.Join(final, "python", "BUILD"), target.PythonVersion); err != nil {
		return ProvisionResult{}, fmt.Errorf("stamp browser Python build: %w", err)
	}
	args := []string{"sync", "--frozen", "--no-install-project", "--project", project, "--python", pythonPath}
	if options.Offline {
		args = append(args, "--offline")
	}
	// Deliberately NO `patchright install chromium`: the worker drives system
	// Chrome (channel="chrome"); downloading a browser here would contradict
	// the rung's whole point and add ~150 MB per host.
	if _, err := options.Run(ctx, uvPath, args, project); err != nil {
		return ProvisionResult{}, fmt.Errorf("install browser locked environment: %w", err)
	}
	venvPython := filepath.Join(project, ".venv", "bin", "python")
	if _, err := os.Stat(venvPython); err != nil {
		return ProvisionResult{}, fmt.Errorf("browser uv sync did not create Python environment: %w", err)
	}
	inventoryOutput, err := options.Run(
		ctx,
		uvPath,
		[]string{"pip", "list", "--format", "freeze", "--python", venvPython},
		project,
	)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("browser installed inventory failed: %w", err)
	}
	inventorySHA, inventoryCount, err := inventoryDigest(inventoryOutput)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("browser installed inventory is invalid: %w", err)
	}
	base.InventorySHA256 = inventorySHA
	base.InventoryCount = inventoryCount
	smoke, err := options.Smoke(ctx, Runtime{Python: venvPython, Script: filepath.Join(project, "browser.py")})
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("browser no-download smoke: %w", err)
	}
	base.Imports = map[string]any{
		"patchright":  smoke["patchright"],
		"chrome_path": smoke["chrome_path"],
	}
	base.State = "ready"
	base.Environment = final
	finalRuntime := Runtime{
		Python: filepath.Join(final, "project", ".venv", "bin", "python"),
		Script: filepath.Join(final, "project", "browser.py"),
	}
	// The environment is never renamed after uv sync (the venv interpreter
	// symlink is absolute); both smokes judge the same final runtime path.
	if _, smokeErr := options.Smoke(ctx, finalRuntime); smokeErr != nil {
		return ProvisionResult{}, fmt.Errorf("browser post-publish smoke: %w", smokeErr)
	}
	base.Sizes = measureTree(final)
	marker, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("marshal browser environment digest: %w", err)
	}
	if err := writePrivate(filepath.Join(final, "environment.json"), append(marker, '\n')); err != nil {
		return ProvisionResult{}, err
	}
	if err := atomicCurrent(envRoot, desired); err != nil {
		return ProvisionResult{}, err
	}
	return ProvisionResult{Digest: desired, Environment: base, Runtime: Runtime{
		Python: filepath.Join(current, "project", ".venv", "bin", "python"),
		Script: filepath.Join(current, "project", "browser.py"),
	}}, nil
}

func smokeBrowserRuntime(ctx context.Context, browserRuntime Runtime) (map[string]any, error) {
	worker := NewBrowserWorker(browserRuntime)
	result, err := worker.Smoke(ctx)
	_ = worker.Close()
	return result, err
}

// InspectBrowser reads the browser environment's machine-readable record
// without executing anything. Absence is reported as absence (os.ErrNotExist
// preserved), so doctor can distinguish NOT-provisioned from BROKEN.
func InspectBrowser(root string, platform Platform) (EnvironmentDigest, error) {
	if platform.GOOS == "" {
		platform.GOOS, platform.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	return ReadEnvironmentDigest(filepath.Join(BrowserRuntimeRoot(root, platform), "environment.json"))
}

// ensureProvision is the provisioner EnsureBrowser converges with; tests swap it.
var ensureProvision = ProvisionBrowser

// EnsureBrowser resolves the browser worker's runtime for one fetch,
// provisioning first when the environment is missing, its record unreadable,
// or it is stale. Presence alone is not enough: an environment an older pfm
// provisioned keeps its interpreter, and running it runs THAT pfm's worker.
// An empty Platform{} stringifies to "-", a path provisioning never writes,
// so the platform is always normalized. It NEVER downloads Chromium.
func EnsureBrowser(ctx context.Context, options ProvisionOptions) (Runtime, error) {
	if options.Platform.GOOS == "" {
		options.Platform = Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	}
	current := BrowserRuntimeRoot(options.Root, options.Platform)
	resolved := Runtime{
		Python: filepath.Join(current, "project", ".venv", "bin", "python"),
		Script: filepath.Join(current, "project", "browser.py"),
	}
	reason := ""
	if _, statErr := os.Stat(resolved.Python); errors.Is(statErr, os.ErrNotExist) {
		reason = "NOT provisioned"
	} else if statErr != nil {
		return Runtime{}, fmt.Errorf("probe browser environment interpreter %s: %w", resolved.Python, statErr)
	} else if digest, inspectErr := InspectBrowser(options.Root, options.Platform); inspectErr != nil {
		reason = fmt.Sprintf("UNREADABLE (%v)", inspectErr)
	} else if stale := BrowserEnvironmentStale(digest); stale != "" {
		reason = "STALE (" + stale + ")"
	}
	if reason == "" {
		return resolved, nil
	}
	log.Printf("harvestpy: browser environment is %s — provisioning before this fetch", reason)
	if _, err := ensureProvision(ctx, options); err != nil {
		return Runtime{}, fmt.Errorf(
			"browser environment is %s and provisioning failed (%v) — it provisions on the first browser fetch once fetch.browser is true in harvester.config.json; check uv and network access, then retry",
			reason,
			err,
		)
	}
	return resolved, nil
}

// BrowserEnvironmentStale names why a provisioned browser environment was
// built from a different worker source or lock than this binary embeds, or
// returns "" when it is current. An upgrade leaves the old environment
// intact — interpreter present, record and on-disk worker agreeing with each
// other — so only this comparison sees that the worker is not this pfm's.
func BrowserEnvironmentStale(digest EnvironmentDigest) string {
	var drift []string
	if digest.SourceSHA256 != browserSourceSHA256() {
		drift = append(drift, "worker source")
	}
	if digest.LockSHA256 != browserLockSHA256() {
		drift = append(drift, "dependency lock")
	}
	if len(drift) == 0 {
		return ""
	}
	return "provisioned from a different browser " + strings.Join(drift, " and ") + " than this pfm embeds"
}

// BrowserSourceState is doctor's source_hash value: "ok" for a current
// environment, SOURCE_STALE naming the drift for one an older pfm left behind.
func BrowserSourceState(digest EnvironmentDigest) string {
	if reason := BrowserEnvironmentStale(digest); reason != "" {
		return "SOURCE_STALE(" + reason + "; the next browser fetch re-provisions it)"
	}
	return "ok"
}

func browserLockSHA256() string { return sha256Hex(BrowserLockMetadata()) }

func browserSourceSHA256() string { return sha256Hex(BrowserWorkerSource()) }

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
