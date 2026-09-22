package harvestpy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestCheckInterpreterFailureIncludesStderr(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.Script(
		[]string{"fake-python", "--version"},
		deps.RunResult{Stdout: []byte("python banner\n"), Stderr: []byte("loader: wrong architecture\n"), ExitCode: -1},
		errors.New("start failed"),
	)

	err := checkInterpreter(context.Background(), fake, "fake-python", "3.11.15+20260610")
	if err == nil || !strings.Contains(err.Error(), "loader: wrong architecture") {
		t.Fatalf("checkInterpreter() error = %v, want stderr diagnostic", err)
	}
}

// readyFixtureEnvironment builds a self-consistent, ready conversion
// environment under root from the given converter source: fake uv and python
// shims, the project files, an environment.json whose digest matches them,
// and the current pointer. It returns the environment's final directory.
func readyFixtureEnvironment(t *testing.T, root string, platform Platform, source []byte) string {
	t.Helper()
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
	write(filepath.Join(project, "converter.py"), string(source), 0o600)
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
		SourceSHA256: digest(source),
		Features:     FeatureStatus{OCR: "disabled", Layout: "disabled", Models: "not-requested"},
	}
	id := digestID(canonical)
	oldFinal := final
	final = filepath.Join(envRoot, id)
	if err := os.Rename(oldFinal, final); err != nil {
		t.Fatal(err)
	}
	marker := canonical
	marker.Digest, marker.State, marker.Environment = id, "ready", final
	marker.InventorySHA256, marker.InventoryCount = markerInventory, markerCount
	body, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(final, "environment.json"), string(body), 0o600)
	if err := os.Symlink(id, filepath.Join(envRoot, "current")); err != nil {
		t.Fatal(err)
	}
	write(
		filepath.Join(final, "project", ".venv", "lib", "python3.11", "site-packages", "removed.dist-info"),
		"present",
		0o600,
	)
	return final
}

// TestCheckRejectsAnEnvironmentBuiltFromAnOlderConverterSource pins the
// upgrade hole the browser environment already closes (BrowserEnvironmentStale)
// and the conversion environment did not: an environment an older pfm
// provisioned is self-consistent — record, on-disk converter.py and digest
// all agree — so every existing check passes and `pfm install` takes its
// Check fast-path, leaving the OLD converter.py in service under a binary
// that embeds a new one. Watched FAILING before the fix (the stale fixture
// reported Healthy).
func TestCheckRejectsAnEnvironmentBuiltFromAnOlderConverterSource(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	older := append([]byte("# an older pfm's converter\n"), ConverterSource()...)
	readyFixtureEnvironment(t, root, platform, older)
	report, err := CheckConversionEnvironment(context.Background(), root, platform)
	if err == nil || report.Healthy {
		t.Fatalf("an environment built from an older converter source reported healthy: %#v", report)
	}
	status := report.Checks["source_current"]
	if status.OK || !strings.Contains(status.Error, "converter source") {
		t.Fatalf("source_current did not name the drift: %#v", status)
	}
	if !report.Checks["source_hash"].OK {
		t.Fatalf(
			"source_hash (record vs disk) must still pass for a self-consistent stale environment: %#v",
			report.Checks["source_hash"],
		)
	}
}

// TestCheckAcceptsAnEnvironmentBuiltFromTheEmbeddedConverterSource is the
// healthy half of the stale-converter check: an environment provisioned from
// this pfm's own converter source and lock passes source_current.
func TestCheckAcceptsAnEnvironmentBuiltFromTheEmbeddedConverterSource(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	readyFixtureEnvironment(t, root, platform, ConverterSource())
	report, err := CheckConversionEnvironment(context.Background(), root, platform)
	if err != nil || !report.Healthy {
		t.Fatalf("an environment built from the embedded converter source was rejected: %#v %v", report, err)
	}
	if status, ok := report.Checks["source_current"]; !ok || !status.OK {
		t.Fatalf("source_current missing or failing for a current environment: %#v", report.Checks)
	}
}

// TestConversionEnvironmentStaleNamesEachDrift pins the drift names and the
// operator's remedy for a record whose converter source, dependency lock, or
// both differ from what this pfm embeds.
func TestConversionEnvironmentStaleNamesEachDrift(t *testing.T) {
	current := EnvironmentDigest{SourceSHA256: sourceSHA256(), LockSHA256: lockSHA256()}
	if err := conversionEnvironmentStale(current); err != nil {
		t.Fatalf("a current record was reported stale: %v", err)
	}
	cases := []struct {
		name   string
		digest EnvironmentDigest
		want   string
	}{
		{"converter source", EnvironmentDigest{SourceSHA256: "older", LockSHA256: lockSHA256()}, "converter source"},
		{"dependency lock", EnvironmentDigest{SourceSHA256: sourceSHA256(), LockSHA256: "older"}, "dependency lock"},
		{
			"both",
			EnvironmentDigest{SourceSHA256: "older", LockSHA256: "older"},
			"converter source and dependency lock",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := conversionEnvironmentStale(tc.digest)
			if err == nil {
				t.Fatalf("drift in %s was not reported", tc.name)
			}
			if !strings.Contains(err.Error(), "different "+tc.want+" than this pfm embeds") ||
				!strings.Contains(err.Error(), "`pfm install` re-provisions it") {
				t.Fatalf("stale error = %q, want the %s drift and the pfm install remedy", err, tc.want)
			}
		})
	}
}
