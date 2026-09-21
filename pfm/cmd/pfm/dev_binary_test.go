package main

import (
	"debug/buildinfo"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDevBinaryIsAbsentOrBuiltFromCurrentModule(t *testing.T) {
	path := filepath.Join("..", "..", "pfm.dev")
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s build info: %v", path, err)
	}
	if info.Path != "github.com/rezzminator/professor/pfm/cmd/pfm" {
		t.Fatalf(
			"%s was built from %q, want github.com/rezzminator/professor/pfm/cmd/pfm; remove the stale binary",
			path,
			info.Path,
		)
	}
}
