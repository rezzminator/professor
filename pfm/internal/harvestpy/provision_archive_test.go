package harvestpy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
