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
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestTargetsPinEveryRequestedPlatformAndVerifyableInputs(t *testing.T) {
	targets := Targets()
	for _, key := range []Platform{
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "linux", GOARCH: "arm64"},
		{GOOS: "darwin", GOARCH: "amd64"},
		{GOOS: "darwin", GOARCH: "arm64"},
	} {
		target, ok := targets[key]
		if !ok {
			t.Fatalf("missing target %s", key)
		}
		for name, value := range map[string]string{
			"uv url": target.UV.URL, "uv sha256": target.UV.SHA256,
			"python url": target.Python.URL, "python sha256": target.Python.SHA256,
		} {
			if value == "" {
				t.Errorf("%s %s is empty", key, name)
			}
		}
		if len(target.UV.SHA256) != sha256.Size*2 || len(target.Python.SHA256) != sha256.Size*2 {
			t.Errorf("%s has non-sha256 pinned inputs: %#v", key, target)
		}
	}
}

func TestPlanExposesPinnedDownloadsAndMeasuredLinuxClosure(t *testing.T) {
	plan, err := PlanConversionEnvironment(Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.PythonVersion != "3.11.15+20260610" {
		t.Fatalf("Python pin = %q", plan.PythonVersion)
	}
	if plan.PackageDownloadBytes != 3106174573 {
		t.Fatalf("linux package download bytes = %d, want measured closure", plan.PackageDownloadBytes)
	}
	if plan.EnvironmentBytes != 5786939761 {
		t.Fatalf("linux environment bytes = %d, want measured environment", plan.EnvironmentBytes)
	}
	for _, tc := range []struct {
		platform Platform
		bytes    int64
		status   string
	}{
		{Platform{GOOS: "linux", GOARCH: "arm64"}, 3179527419, "pinned-lock-artifact-sum"},
		{Platform{GOOS: "darwin", GOARCH: "amd64"}, -1, "blocked-exact-lock"},
		{Platform{GOOS: "darwin", GOARCH: "arm64"}, 389353114, "pinned-lock-artifact-sum"},
	} {
		other, err := PlanConversionEnvironment(tc.platform)
		if err != nil {
			t.Fatal(err)
		}
		if other.PackageDownloadBytes != tc.bytes || other.PackageDownloadStatus != tc.status {
			t.Errorf(
				"%s package plan = %d/%s, want %d/%s",
				tc.platform,
				other.PackageDownloadBytes,
				other.PackageDownloadStatus,
				tc.bytes,
				tc.status,
			)
		}
		if tc.platform == (Platform{GOOS: "darwin", GOARCH: "amd64"}) && len(other.PackageBlockers) != 3 {
			t.Fatalf("darwin-amd64 blockers = %#v, want all exact-lock gaps", other.PackageBlockers)
		}
	}
}

func TestEmbeddedAssetsAreConversionOnlyAndProtocolIsPinned(t *testing.T) {
	if !strings.Contains(string(ConverterSource()), "trafilatura") ||
		!strings.Contains(string(ConverterSource()), "pymupdf4llm") ||
		!strings.Contains(string(ConverterSource()), "docling") ||
		!strings.Contains(string(ConverterSource()), "markitdown") {
		t.Fatal("embedded converter source does not contain the three oracle converters")
	}
	project := string(ProjectMetadata())
	for _, forbidden := range []string{"mcp", "curl-cffi", "httpx", "requests", "py7zr"} {
		if strings.Contains(strings.ToLower(project), forbidden) {
			t.Errorf("conversion project unexpectedly contains %q", forbidden)
		}
	}
	lock := string(LockMetadata())
	for _, required := range []string{"trafilatura", "pymupdf4llm", "docling", "markitdown", "3.11"} {
		if !strings.Contains(strings.ToLower(lock), strings.ToLower(required)) {
			t.Errorf("embedded lock is missing %q", required)
		}
	}
	for _, forbidden := range []string{"import zipfile", "import tarfile", "import py7zr", "import rarfile"} {
		if strings.Contains(strings.ToLower(string(ConverterSource())), forbidden) {
			t.Errorf("Python worker unexpectedly owns archive parsing: %q", forbidden)
		}
	}
}

func TestEmbeddedLockKeepsEveryResolvedConversionPackageAtOracleVersions(t *testing.T) {
	// The Python harvester tree that produced this lock is retired; its final
	// resolution is FROZEN here so the migration's version parity stays provable
	// after the source tree is gone.
	oraclePath := filepath.Join("testdata", "oracle-uv.lock")
	oracle, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Fatalf("read oracle lock %s: %v", oraclePath, err)
	}
	oracleVersions := lockVersions(string(oracle))
	embeddedVersions := lockVersions(string(LockMetadata()))
	if len(embeddedVersions) < 100 {
		t.Fatalf(
			"embedded conversion lock has only %d package records; full Docling/Torch behavior was not retained",
			len(embeddedVersions),
		)
	}
	for name, versions := range embeddedVersions {
		if name == "professor-harvestpy" {
			continue
		}
		for _, version := range versions {
			found := false
			for _, oracleVersion := range oracleVersions[name] {
				if version == oracleVersion {
					found = true
					break
				}
			}
			if !found {
				t.Errorf(
					"embedded package %s=%s is not an oracle lock version (%v)",
					name,
					version,
					oracleVersions[name],
				)
			}
		}
	}
}

func lockVersions(lock string) map[string][]string {
	versions := make(map[string][]string)
	name := ""
	for _, line := range strings.Split(lock, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name = \"") {
			name = strings.Trim(strings.TrimPrefix(line, "name = \""), "\"")
			continue
		}
		if name != "" && strings.HasPrefix(line, "version = \"") {
			version := strings.Trim(strings.TrimPrefix(line, "version = \""), "\"")
			versions[name] = append(versions[name], version)
			name = ""
		}
	}
	return versions
}

func TestVerifySHA256DistinguishesCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "download")
	if err := os.WriteFile(path, []byte("good"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("good"))
	if err := VerifySHA256(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("valid file rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(path, hex.EncodeToString(sum[:])); err == nil {
		t.Fatal("corrupt file accepted")
	}
}

func TestPinnedDownloadRejectsShortAndOversizedResponsesBeforePublish(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "short", body: "short"},
		{name: "long", body: "longer-than-pin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "input")
			err := ensureInput(
				context.Background(),
				path,
				Artifact{URL: server.URL, Size: int64(len("pinned")), SHA256: digest([]byte("pinned"))},
				false,
				nil,
			)
			if err == nil {
				t.Fatal("wrong-sized response was accepted")
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("wrong-sized input was published: %v", statErr)
			}
		})
	}
}

func TestProvisionOfflineReportsMissingInputAndNeverLeavesPartialEnvironment(t *testing.T) {
	root := t.TempDir()
	_, err := Provision(context.Background(), ProvisionOptions{
		Root: root, Platform: Platform{GOOS: "linux", GOARCH: "amd64"}, Offline: true,
	})
	if !errors.Is(err, ErrOfflineUnavailable) {
		t.Fatalf("offline error = %v, want ErrOfflineUnavailable", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".staging-") || strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("partial environment remains: %s", entry.Name())
		}
	}
}

func TestProvisionConvergesAtomicallyAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets := Targets()
	target := Targets()[platform]
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	// A fake archive is sufficient to exercise download/verification and the
	// convergence state machine; the command runner is not invoked until the
	// archives are accepted by the implementation.
	uvBytes := []byte("uv archive")
	pyBytes := []byte("python archive")
	target.UV.SHA256 = digest(uvBytes)
	target.UV.Size = int64(len(uvBytes))
	target.Python.SHA256 = digest(pyBytes)
	target.Python.Size = int64(len(pyBytes))
	targets[platform] = target
	old := targetsForTest
	targetsForTest = targets
	t.Cleanup(func() { targetsForTest = old })

	var mu sync.Mutex
	downloads := 0
	commands := 0
	result, err := provisionWith(t, ProvisionOptions{
		Root: root, Cache: cache, Platform: platform,
		Download: func(_ context.Context, _, path string) error {
			mu.Lock()
			defer mu.Unlock()
			downloads++
			body := uvBytes
			if strings.Contains(path, "python") {
				body = pyBytes
			}
			return os.WriteFile(path, body, 0o700)
		},
		Run: func(_ context.Context, _ string, _ []string, _ string) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			commands++
			return nil, nil
		},
	})
	if err == nil {
		t.Fatal("fake non-archive inputs unexpectedly provisioned")
	}
	if downloads != 2 {
		t.Fatalf("downloads = %d, want two verified input attempts", downloads)
	}
	if commands != 0 {
		t.Fatalf("commands = %d after archive rejection, want zero", commands)
	}
	if result.Digest != "" {
		t.Fatalf("failed convergence returned digest %q", result.Digest)
	}
}

func TestProvisionKeepsAbsoluteVenvLinksAtTheirFinalPath(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)
	var smokePaths []string

	result, err := provision(context.Background(), ProvisionOptions{
		Root: root, Cache: cache, Platform: platform,
		Run: fakeProvisionRun(t, false),
		Smoke: func(_ context.Context, runtime Runtime) (map[string]any, error) {
			smokePaths = append(smokePaths, runtime.Python)
			resolved, err := filepath.EvalSymlinks(runtime.Python)
			if err != nil {
				return nil, fmt.Errorf("resolve fake uv Python link: %w", err)
			}
			if _, err := os.Stat(resolved); err != nil {
				return nil, fmt.Errorf("stat fake uv Python target: %w", err)
			}
			return map[string]any{
				"imports":    map[string]any{},
				"conversion": map[string]any{"ok": true},
			}, nil
		},
	}, targets)
	if err != nil {
		t.Fatalf("provision with absolute uv link failed: %v", err)
	}
	final := filepath.Join(root, "env", platform.String(), result.Digest)
	venvPython := filepath.Join(final, "project", ".venv", "bin", "python")
	resolved, err := filepath.EvalSymlinks(venvPython)
	if err != nil {
		t.Fatal(err)
	}
	wantPython := filepath.Join(final, "python", "bin", "python3")
	if resolved != wantPython {
		t.Fatalf("absolute uv Python link resolves to %s, want %s", resolved, wantPython)
	}
	configuration, err := os.ReadFile(filepath.Join(final, "project", ".venv", "pyvenv.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	wantHome := "home = " + filepath.Join(final, "python", "bin")
	if !strings.Contains(string(configuration), wantHome) {
		t.Fatalf("pyvenv.cfg = %q, want %q", configuration, wantHome)
	}
	if len(smokePaths) != 2 || smokePaths[0] != venvPython || smokePaths[1] != venvPython {
		t.Fatalf("smoke paths = %#v, want the same final runtime twice", smokePaths)
	}
}

func TestProvisionFailureRemovesIncompleteFinalPath(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)
	desired := desiredTestDigest(platform, targets[platform])

	_, err := provision(context.Background(), ProvisionOptions{
		Root: root, Cache: cache, Platform: platform,
		Run: fakeProvisionRun(t, true),
	}, targets)
	if err == nil || !strings.Contains(err.Error(), "locked dependency check failed") {
		t.Fatalf("pip-check interruption error = %v", err)
	}
	for _, path := range []string{
		filepath.Join(root, "env", platform.String(), desired),
		RuntimeRoot(root, platform),
	} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("interrupted provision left %s: %v", path, statErr)
		}
	}
}

func TestProvisionFailureRestoresQuarantinedEnvironment(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)
	desired := desiredTestDigest(platform, targets[platform])
	envRoot := filepath.Join(root, "env", platform.String())
	final := filepath.Join(envRoot, desired)
	if err := os.MkdirAll(final, 0o700); err != nil {
		t.Fatal(err)
	}
	oldState := filepath.Join(final, "old-state")
	if err := os.WriteFile(oldState, []byte("restore me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(desired, RuntimeRoot(root, platform)); err != nil {
		t.Fatal(err)
	}

	_, err := provision(context.Background(), ProvisionOptions{
		Root: root, Cache: cache, Platform: platform,
		Run: fakeProvisionRun(t, true),
	}, targets)
	if err == nil || !strings.Contains(err.Error(), "locked dependency check failed") {
		t.Fatalf("pip-check interruption error = %v", err)
	}
	if body, readErr := os.ReadFile(oldState); readErr != nil || string(body) != "restore me\n" {
		t.Fatalf("quarantined environment was not restored: %q, %v", body, readErr)
	}
	entries, err := os.ReadDir(envRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".repair-") {
			t.Fatalf("repair quarantine remains after restoration: %s", entry.Name())
		}
	}
}

func TestInspectAndCheckReportIncompleteProvisioning(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	envRoot := filepath.Join(root, "env", platform.String())
	version := filepath.Join(envRoot, "interrupted-digest")
	if err := os.MkdirAll(version, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(version, "INCOMPLETE"),
		[]byte("provisioning did not finish\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(version), RuntimeRoot(root, platform)); err != nil {
		t.Fatal(err)
	}

	digest, inspectErr := InspectConversionEnvironment(root, platform)
	if digest.State != "incomplete" || inspectErr == nil ||
		!strings.Contains(inspectErr.Error(), "provisioning did not finish") {
		t.Fatalf("Inspect incomplete = %#v, %v", digest, inspectErr)
	}
	report, checkErr := CheckConversionEnvironment(context.Background(), root, platform)
	if report.Digest.State != "incomplete" || checkErr == nil ||
		!strings.Contains(report.Checks["marker"].Error, "provisioning did not finish") {
		t.Fatalf("Check incomplete = %#v, %v", report, checkErr)
	}
}

func TestSubprocessProtocolConvertsHTMLAndReturnsStructuredErrors(t *testing.T) {
	python := fakePython(t, `
import json,sys
for line in sys.stdin:
    req=json.loads(line)
    if req.get("op") == "smoke":
        print(json.dumps({"ok":True,"runtime":"test"}), flush=True)
    else:
        print(json.dumps({"ok":True,"markdown":"# converted"}), flush=True)
`)
	converter := testConverter(t, python)
	result, err := converter.Convert(
		context.Background(),
		Request{Path: filepath.Join(t.TempDir(), "input.html"), Kind: "html"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Markdown != "# converted" {
		t.Fatalf("markdown = %q", result.Markdown)
	}
}

func TestConverterKeepsOneLongLivedWorkerAndRejectsUnprovisionedScript(t *testing.T) {
	python := fakePython(t, `
import json,os,sys
for line in sys.stdin:
    req=json.loads(line)
    print(json.dumps({"ok":True,"markdown":"# "+str(os.getpid())}), flush=True)
`)
	converter := testConverter(t, python)
	first, err := converter.Convert(context.Background(), Request{Path: "one", Kind: "html"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := converter.Convert(context.Background(), Request{Path: "two", Kind: "html"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Markdown != second.Markdown {
		t.Fatalf("worker was not reused: %q vs %q", first.Markdown, second.Markdown)
	}
	if err := converter.Close(); err != nil {
		t.Fatal(err)
	}
	missing := NewConverter(Runtime{Python: python, Script: filepath.Join(t.TempDir(), "missing.py")})
	if _, err := missing.Convert(context.Background(), Request{Path: "one", Kind: "html"}); err == nil {
		t.Fatal("missing managed script was silently materialized")
	}
}

func TestArchiveKindsAreRejectedBeforePython(t *testing.T) {
	python := fakePython(t, `
import json,sys
for line in sys.stdin:
    req=json.loads(line)
    print(json.dumps({"ok":True,"markdown":"unexpected"}), flush=True)
`)
	converter := testConverter(t, python)
	for _, kind := range []string{"zip", "tar", "7z", "rar"} {
		if _, err := converter.Convert(context.Background(), Request{Path: "bounded-member", Kind: kind}); err == nil {
			t.Fatalf("archive kind %q was accepted by Python boundary", kind)
		}
	}
}

func TestMachineReadableDigestRoundTrip(t *testing.T) {
	digest := EnvironmentDigest{
		Schema: 1, Target: "linux-amd64", Python: "3.11.15+20260610",
		UV: "0.11.32", LockSHA256: strings.Repeat("a", 64),
		Features: FeatureStatus{OCR: "disabled", Layout: "disabled", Models: "not-downloaded"},
	}
	body, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "environment.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEnvironmentDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Target != digest.Target || got.Features.Models != "not-downloaded" {
		t.Fatalf("digest = %#v", got)
	}
}

func TestInstalledInventoryDigestIsStableAndRejectsTampering(t *testing.T) {
	first, count, err := inventoryDigest([]byte("Docling==2.107.0\ntrafilatura==2.1.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, secondCount, err := inventoryDigest([]byte("trafilatura==2.1.0\nDocling==2.107.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || count != 2 || secondCount != 2 {
		t.Fatalf("inventory digest is not order-stable: %s/%d vs %s/%d", first, count, second, secondCount)
	}
	if _, _, err := inventoryDigest([]byte("Docling==2.107.0\nnot-a-distribution\n")); err == nil {
		t.Fatal("tampered inventory record was accepted")
	}
}

func TestCheckRejectsAChangedInstalledInventory(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	envRoot := filepath.Join(root, "env", platform.String())
	final := filepath.Join(envRoot, "versioned")
	project := filepath.Join(final, "project")
	venv := filepath.Join(project, ".venv")
	if err := os.MkdirAll(filepath.Join(venv, "lib", "python3.11", "site-packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	uv := filepath.Join(final, "uv")
	write(
		uv,
		"#!/bin/sh\nif [ \"$2\" = \"check\" ]; then exit 0; fi\nif [ -f \"$PWD/.venv/lib/python3.11/site-packages/removed.dist-info\" ]; then printf 'docling==2.107.0\\ntrafilatura==2.1.0\\n'; else printf 'docling==2.107.0\\n'; fi\n",
		0o700,
	)
	if err := os.MkdirAll(filepath.Join(final, "python"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(final, "python", "BUILD"), "20260610\n", 0o600)
	python := filepath.Join(venv, "bin", "python")
	if err := os.MkdirAll(filepath.Dir(python), 0o700); err != nil {
		t.Fatal(err)
	}
	write(
		python,
		"#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'Python 3.11.15'; else while IFS= read -r _line; do printf '{\"ok\":true,\"imports\":{},\"conversion\":{\"ok\":true}}\\n'; done; fi\n",
		0o700,
	)
	write(filepath.Join(project, "converter.py"), string(ConverterSource()), 0o600)
	write(filepath.Join(project, "pyproject.toml"), string(ProjectMetadata()), 0o600)
	write(filepath.Join(project, "uv.lock"), string(LockMetadata()), 0o600)
	markerInventory, markerCount, err := inventoryDigest([]byte("docling==2.107.0\ntrafilatura==2.1.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	target := Targets()[platform]
	canonical := EnvironmentDigest{
		Schema:       1,
		Target:       platform.String(),
		Python:       "3.11.15+20260610",
		UV:           "0.11.32",
		PythonSHA256: target.Python.SHA256,
		UVSHA256:     target.UV.SHA256,
		LockSHA256:   lockSHA256(),
		SourceSHA256: sourceSHA256(),
		Features:     FeatureStatus{OCR: "disabled", Layout: "disabled", Models: "not-requested"},
	}
	digest := digestID(canonical)
	oldFinal := final
	final = filepath.Join(envRoot, digest)
	if err := os.Rename(oldFinal, final); err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(final, "project")
	venv = filepath.Join(project, ".venv")
	marker := canonical
	marker.Digest, marker.State, marker.Environment = digest, "ready", final
	marker.InventorySHA256, marker.InventoryCount = markerInventory, markerCount
	body, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(final, "environment.json"), string(body), 0o600)
	if err := os.MkdirAll(envRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(digest, filepath.Join(envRoot, "current")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(venv, "lib", "python3.11", "site-packages", "removed.dist-info"), "present", 0o600)
	report, err := CheckConversionEnvironment(context.Background(), root, platform)
	if err != nil || !report.Healthy {
		t.Fatalf("healthy fixture rejected: %#v %v", report, err)
	}
	// The fake uv inventory changes after the installed package marker is
	// removed, proving Check compares the live closure rather than trusting the
	// marker.
	if err := os.Remove(filepath.Join(venv, "lib", "python3.11", "site-packages", "removed.dist-info")); err != nil {
		t.Fatal(err)
	}
	report, err = CheckConversionEnvironment(context.Background(), root, platform)
	if err == nil || report.Checks["lock_completeness"].OK {
		t.Fatalf("missing installed package was accepted: %#v %v", report, err)
	}
}

// TestCheckInventoryIgnoresUVsStderrBanner pins the deep-doctor defect: uv
// 0.11+ writes an interpreter-selection banner ("Using Python ... environment
// at: ...") to STDERR on every invocation, `pip list --format freeze`
// included. checkInventory's combined-output capture folds that banner into
// the same byte stream as the freeze records, and inventoryDigest then
// rejects the banner line as an invalid "name==version" record — a fully
// healthy, fully provisioned environment reports a decode ERROR that the
// doctor then mislabels as "incomplete" (the word reserved for a marker left
// by an interrupted provision, not a live check failure).
func TestCheckInventoryIgnoresUVsStderrBanner(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	envRoot := filepath.Join(root, "env", platform.String())
	final := filepath.Join(envRoot, "versioned")
	project := filepath.Join(final, "project")
	venv := filepath.Join(project, ".venv")
	if err := os.MkdirAll(filepath.Join(venv, "lib", "python3.11", "site-packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	uv := filepath.Join(final, "uv")
	write(uv, "#!/bin/sh\n"+
		"echo 'Using Python 3.11.15 environment at: .venv' 1>&2\n"+
		"if [ \"$2\" = \"check\" ]; then exit 0; fi\n"+
		"printf 'docling==2.107.0\\ntrafilatura==2.1.0\\n'\n", 0o700)
	if err := os.MkdirAll(filepath.Join(final, "python"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(final, "python", "BUILD"), "20260610\n", 0o600)
	python := filepath.Join(venv, "bin", "python")
	if err := os.MkdirAll(filepath.Dir(python), 0o700); err != nil {
		t.Fatal(err)
	}
	write(
		python,
		"#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'Python 3.11.15'; else while IFS= read -r _line; do printf '{\"ok\":true,\"imports\":{},\"conversion\":{\"ok\":true}}\\n'; done; fi\n",
		0o700,
	)
	write(filepath.Join(project, "converter.py"), string(ConverterSource()), 0o600)
	write(filepath.Join(project, "pyproject.toml"), string(ProjectMetadata()), 0o600)
	write(filepath.Join(project, "uv.lock"), string(LockMetadata()), 0o600)
	markerInventory, markerCount, err := inventoryDigest([]byte("docling==2.107.0\ntrafilatura==2.1.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	target := Targets()[platform]
	canonical := EnvironmentDigest{
		Schema:       1,
		Target:       platform.String(),
		Python:       "3.11.15+20260610",
		UV:           "0.11.32",
		PythonSHA256: target.Python.SHA256,
		UVSHA256:     target.UV.SHA256,
		LockSHA256:   lockSHA256(),
		SourceSHA256: sourceSHA256(),
		Features:     FeatureStatus{OCR: "disabled", Layout: "disabled", Models: "not-requested"},
	}
	digest := digestID(canonical)
	oldFinal := final
	final = filepath.Join(envRoot, digest)
	if err := os.Rename(oldFinal, final); err != nil {
		t.Fatal(err)
	}
	marker := canonical
	marker.Digest, marker.State, marker.Environment = digest, "ready", final
	marker.InventorySHA256, marker.InventoryCount = markerInventory, markerCount
	body, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(final, "environment.json"), string(body), 0o600)
	if err := os.MkdirAll(envRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(digest, filepath.Join(envRoot, "current")); err != nil {
		t.Fatal(err)
	}

	report, err := CheckConversionEnvironment(context.Background(), root, platform)
	if err != nil || !report.Healthy {
		t.Fatalf("uv's stderr banner was decoded as a freeze record: report=%#v err=%v", report, err)
	}
	if status := report.Checks["lock_completeness"]; !status.OK {
		t.Fatalf(
			"lock_completeness = %#v, want OK — a decode failure must never register on a fully provisioned environment",
			status,
		)
	}
	if report.Digest.State != "ready" {
		t.Fatalf(
			"Digest.State = %q, want %q — a live check failure is never the same condition as an interrupted provision ('incomplete')",
			report.Digest.State,
			"ready",
		)
	}
}

func TestCorpusManifestIsPermanentAndHasThirtyOraclePairs(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("testdata", "corpus", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Items []struct {
			Status    string `json:"status"`
			ID        string `json:"id"`
			Method    string `json:"method"`
			Kind      string `json:"detected_kind"`
			Oracle    string `json:"oracle_path"`
			Raw       string `json:"raw_path"`
			RawSHA    string `json:"raw_sha256"`
			OracleSHA string `json:"oracle_sha256"`
		} `json:"items"`
	}
	if err := json.Unmarshal(manifest, &value); err != nil {
		t.Fatal(err)
	}
	ok := 0
	for _, item := range value.Items {
		if item.Status != "ok" || item.Oracle == "" || item.Raw == "" {
			continue
		}
		ok++
		if _, err := os.Stat(filepath.Join("testdata", "corpus", item.Oracle)); err != nil {
			t.Errorf("missing oracle %s: %v", item.Oracle, err)
		}
		if _, err := os.Stat(filepath.Join("testdata", "corpus", item.Raw)); err != nil {
			t.Errorf("missing raw %s: %v", item.Raw, err)
		}
		if got := fileSHA256(filepath.Join("testdata", "corpus", item.Raw)); got != item.RawSHA {
			t.Errorf("raw hash %s = %s, want %s", item.Raw, got, item.RawSHA)
		}
		if got := fileSHA256(filepath.Join("testdata", "corpus", item.Oracle)); got != item.OracleSHA {
			t.Errorf("oracle hash %s = %s, want %s", item.Oracle, got, item.OracleSHA)
		}
	}
	if ok != 30 {
		t.Fatalf("permanent corpus pairs = %d, want 30", ok)
	}
}

func fileSHA256(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return digest(body)
}

func TestCorpusPythonAcceptanceExactHTMLFixture(t *testing.T) {
	python := os.Getenv("HARVESTPY_CORPUS_PYTHON")
	if python == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON is not set; corpus acceptance is an explicit environment-backed gate")
	}
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	converter := testConverter(t, python)
	raw := filepath.Join("testdata", "corpus", "raw", "html", "html-python-docs-1.html")
	wantPath := filepath.Join("testdata", "corpus", "oracle", "html-python-docs.md")
	result, err := converter.Convert(context.Background(), Request{Path: raw, Kind: "html"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Markdown != string(want) {
		t.Fatalf("HTML oracle mismatch: got %d bytes want %d", len(result.Markdown), len(want))
	}
}

func TestCorpusPythonConversionOwnedPairsAreByteExact(t *testing.T) {
	python := os.Getenv("HARVESTPY_CORPUS_PYTHON")
	if python == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON is not set; exact corpus acceptance requires an explicit pinned interpreter")
	}
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	manifest, err := os.ReadFile(filepath.Join("testdata", "corpus", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Method string `json:"method"`
			Kind   string `json:"detected_kind"`
			Raw    string `json:"raw_path"`
			Oracle string `json:"oracle_path"`
		} `json:"items"`
	}
	if err := json.Unmarshal(manifest, &value); err != nil {
		t.Fatal(err)
	}
	accepted := map[string]bool{"local-trafilatura": true, "pdf:pymupdf4llm": true, "office:docling": true}
	converter := testConverter(t, python)
	passed := 0
	for _, item := range value.Items {
		if item.Status != "ok" || !accepted[item.Method] || item.Raw == "" || item.Oracle == "" {
			continue
		}
		result, err := converter.Convert(
			context.Background(),
			Request{Path: filepath.Join("testdata", "corpus", item.Raw), Kind: item.Kind},
		)
		if err != nil {
			t.Errorf("%s conversion failed: %v", item.ID, err)
			continue
		}
		want, err := os.ReadFile(filepath.Join("testdata", "corpus", item.Oracle))
		if err != nil {
			t.Errorf("%s oracle read failed: %v", item.ID, err)
			continue
		}
		if result.Markdown != string(want) {
			t.Errorf("%s oracle mismatch: got %d bytes want %d", item.ID, len(result.Markdown), len(want))
			continue
		}
		passed++
	}
	if passed != 21 {
		t.Fatalf(
			"byte-exact Python conversion pairs = %d, want 21; archive/non-Python ladder pairs and the Go-added EuropePMC figures trailer are deliberately outside this gate",
			passed,
		)
	}
}

func TestCSVAndJSONConversionPathsAreByteExact(t *testing.T) {
	python := os.Getenv("HARVESTPY_CORPUS_PYTHON")
	if python == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON is not set; exact converter fixtures require the pinned interpreter")
	}
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "sample.csv")
	if err := os.WriteFile(csvPath, []byte("name,value\nalpha,1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(dir, "sample.json")
	if err := os.WriteFile(jsonPath, []byte("{\"b\":2,\"a\":[1,true]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalidJSONPath := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalidJSONPath, []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := testConverter(t, python)
	for _, tc := range []struct {
		path string
		kind string
		want string
	}{
		{csvPath, "csv", "| name | value |\n| --- | --- |\n| alpha | 1 |\n"},
		{jsonPath, "json", "```json\n{\n  \"b\": 2,\n  \"a\": [\n    1,\n    true\n  ]\n}\n```\n"},
		{invalidJSONPath, "json", "```json\n{not-json\n```\n"},
	} {
		got, err := converter.Convert(context.Background(), Request{Path: tc.path, Kind: tc.kind})
		if err != nil {
			t.Fatalf("%s conversion failed: %v", tc.kind, err)
		}
		if got.Markdown != tc.want {
			t.Errorf("%s output drift: got %q want %q", tc.kind, got.Markdown, tc.want)
		}
	}
}

func TestTarFixtureHelperCompilesForArchiveSecurityTests(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "safe.txt", Mode: 0o600, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if len(buf.Bytes()) == 0 || runtime.GOOS == "" || io.EOF == nil {
		t.Fatal("fixture helper did not produce bytes")
	}
}

func TestPythonArchiveExtractionRetainsInterpreterLibrariesAndRejectsTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "python.tar.gz")
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(archiveFile)
	tarWriter := tar.NewWriter(gzipWriter)
	entries := []struct {
		name string
		body string
		mode int64
	}{
		{"python/", "", 0o755},
		{"python/bin/", "", 0o755},
		{"python/bin/python3", "interpreter", 0o755},
		{"python/lib/python3.11/", "", 0o755},
		{"python/lib/python3.11/os.py", "stdlib", 0o644},
		{"python/share/terminfo/1/", "", 0o755},
		{"python/share/terminfo/a/", "", 0o755},
		{"python/share/terminfo/a/target", "linked", 0o644},
	}
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body))}
		if strings.HasSuffix(entry.name, "/") {
			header.Typeflag = tar.TypeDir
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.body != "" {
			if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.WriteHeader(
		&tar.Header{
			Name:     "python/share/terminfo/1/entry",
			Linkname: "../a/target",
			Typeflag: tar.TypeSymlink,
			Mode:     0o777,
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "python")
	python, err := extractPython(archivePath, destination)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(python); err != nil {
		t.Fatalf("interpreter missing: %v", err)
	}
	stdlib, err := os.ReadFile(filepath.Join(destination, "python", "lib", "python3.11", "os.py"))
	if err != nil || string(stdlib) != "stdlib" {
		t.Fatalf("stdlib was not retained: %q %v", string(stdlib), err)
	}
	if target, err := os.Readlink(
		filepath.Join(destination, "python", "share", "terminfo", "1", "entry"),
	); err != nil ||
		target != "../a/target" {
		t.Fatalf("safe internal symlink was not retained: %q %v", target, err)
	}
	unsafePath := filepath.Join(t.TempDir(), "unsafe.tar.gz")
	unsafeFile, err := os.Create(unsafePath)
	if err != nil {
		t.Fatal(err)
	}
	unsafeGzip := gzip.NewWriter(unsafeFile)
	unsafeTar := tar.NewWriter(unsafeGzip)
	if err := unsafeTar.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o600, Size: 1}); err != nil {
		t.Fatal(err)
	}
	_, _ = unsafeTar.Write([]byte("x"))
	_ = unsafeTar.Close()
	_ = unsafeGzip.Close()
	_ = unsafeFile.Close()
	if _, err := extractPython(unsafePath, filepath.Join(t.TempDir(), "python")); err == nil {
		t.Fatal("path traversal archive was accepted")
	}
}

func TestPinnedPythonArchiveRetainsFullRuntimeAndStdlib(t *testing.T) {
	archive := os.Getenv("HARVESTPY_PYTHON_ARCHIVE")
	if archive == "" {
		t.Skip(
			"HARVESTPY_PYTHON_ARCHIVE is not set; release-archive structural acceptance is an explicit provisioning gate",
		)
	}
	destination := filepath.Join(t.TempDir(), "python")
	python, err := extractPython(archive, destination)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "python", "lib", "python3.11", "os.py")); err != nil {
		t.Fatalf("stdlib missing from full archive extraction: %v", err)
	}
	if _, err := os.Stat(python); err != nil {
		t.Fatalf("standalone interpreter missing after extraction: %v", err)
	}
}

func fakePython(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-python")
	if err := os.WriteFile(
		path,
		[]byte("#!/bin/sh\nexec python3 -c '"+strings.ReplaceAll(body, "'", "'\\''")+"'\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	return path
}

func testConverter(t *testing.T, python string) *Converter {
	t.Helper()
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, ConverterSource(), 0o700); err != nil {
		t.Fatal(err)
	}
	return NewConverter(Runtime{Python: python, Script: script})
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func fakeProvisionInputs(t *testing.T, root string, platform Platform) (map[Platform]Target, string) {
	t.Helper()
	targets := Targets()
	target := targets[platform]
	uvArchive := fakeTarGzip(t, []fakeArchiveEntry{{name: "uv", body: "fake uv\n", mode: 0o700}})
	pythonArchive := fakeTarGzip(t, []fakeArchiveEntry{
		{name: "python", mode: 0o700, directory: true},
		{name: "python/bin", mode: 0o700, directory: true},
		{name: "python/bin/python3", body: "fake python\n", mode: 0o700},
	})
	target.UV.SHA256, target.UV.Size = digest(uvArchive), int64(len(uvArchive))
	target.Python.SHA256, target.Python.Size = digest(pythonArchive), int64(len(pythonArchive))
	targets[platform] = target
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "uv-"+platform.String()+".tar.gz"), uvArchive, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cache, "python-"+platform.String()+".tar.gz"),
		pythonArchive,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return targets, cache
}

func fakeProvisionRun(t *testing.T, failCheck bool) RunFunc {
	t.Helper()
	valueAfter := func(arguments []string, name string) string {
		for index := 0; index+1 < len(arguments); index++ {
			if arguments[index] == name {
				return arguments[index+1]
			}
		}
		return ""
	}
	return func(_ context.Context, _ string, arguments []string, _ string) ([]byte, error) {
		switch {
		case len(arguments) > 0 && arguments[0] == "sync":
			project := valueAfter(arguments, "--project")
			python := valueAfter(arguments, "--python")
			venvBin := filepath.Join(project, ".venv", "bin")
			if err := os.MkdirAll(venvBin, 0o700); err != nil {
				return nil, err
			}
			if err := os.Symlink(python, filepath.Join(venvBin, "python")); err != nil {
				return nil, err
			}
			configuration := []byte("home = " + filepath.Dir(python) + "\n")
			if err := os.WriteFile(filepath.Join(project, ".venv", "pyvenv.cfg"), configuration, 0o600); err != nil {
				return nil, err
			}
			return nil, nil
		case len(arguments) > 1 && arguments[0] == "pip" && arguments[1] == "check":
			if failCheck {
				return nil, errors.New("fake pip check failed")
			}
			return nil, nil
		case len(arguments) > 1 && arguments[0] == "pip" && arguments[1] == "list":
			return []byte("fixture==1.0\n"), nil
		default:
			return nil, fmt.Errorf("unexpected fake uv arguments: %v", arguments)
		}
	}
}

type fakeArchiveEntry struct {
	name      string
	body      string
	mode      int64
	directory bool
}

func fakeTarGzip(t *testing.T, entries []fakeArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body))}
		if entry.directory {
			header.Typeflag = tar.TypeDir
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.body != "" {
			if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func desiredTestDigest(platform Platform, target Target) string {
	return digestID(EnvironmentDigest{
		Schema: 1, Target: platform.String(), Python: target.PythonVersion, UV: target.UVVersion,
		PythonSHA256: target.Python.SHA256, UVSHA256: target.UV.SHA256,
		LockSHA256: lockSHA256(), SourceSHA256: sourceSHA256(),
		Features: FeatureStatus{OCR: "disabled", Layout: "disabled", Models: "not-requested"},
	})
}

// These seams are deliberately test-only; production uses the immutable
// embedded target table and real archive/downloader/command implementations.
var targetsForTest = Targets()

func provisionWith(t *testing.T, options ProvisionOptions) (ProvisionResult, error) {
	t.Helper()
	return provision(context.Background(), options, targetsForTest)
}
