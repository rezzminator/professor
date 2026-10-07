package installer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestVSCodeExtensionLedgerRoundTripsSortedAndValidates pins
// writeVSCodeOwnership/readVSCodeOwnership directly: extensions come back
// sorted regardless of write order, a relative extension path is refused,
// and a duplicate is refused.
func TestVSCodeExtensionLedgerRoundTripsSortedAndValidates(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	path := filepath.Join(managed, vscodeOwnershipName)
	installer := &engine{
		options:     Options{Home: home, Stdout: &bytes.Buffer{}},
		apply:       true,
		managedRoot: managed,
		stamp:       "fixture",
	}

	unsorted := []string{
		filepath.Join(home, "z-product", "extensions", "professor"),
		filepath.Join(home, "a-product", "extensions", "professor"),
	}
	if err := installer.writeVSCodeOwnership(path, nil, map[string]vscodeOwnershipRecord{}, unsorted, nil); err != nil {
		t.Fatal(err)
	}
	_, extensions, _, _, err := readVSCodeOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), unsorted...)
	sort.Strings(want)
	if !reflect.DeepEqual(extensions, want) {
		t.Fatalf("round-tripped extensions = %v, want sorted %v", extensions, want)
	}

	relativeDoc := `{"version":1,"extensions":["relative/extensions/professor"]}`
	if err := os.WriteFile(path, []byte(relativeDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := readVSCodeOwnership(
		path,
	); err == nil ||
		!strings.Contains(err.Error(), "invalid extension link path") {
		t.Fatalf("a relative extension path was accepted: err=%v", err)
	}

	duplicateTarget := filepath.Join(home, "dup", "extensions", "professor")
	duplicateDoc := fmt.Sprintf(`{"version":1,"extensions":[%q,%q]}`, duplicateTarget, duplicateTarget)
	if err := os.WriteFile(path, []byte(duplicateDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := readVSCodeOwnership(
		path,
	); err == nil ||
		!strings.Contains(err.Error(), "duplicate extension link") {
		t.Fatalf("a duplicate extension path was accepted: err=%v", err)
	}
}
