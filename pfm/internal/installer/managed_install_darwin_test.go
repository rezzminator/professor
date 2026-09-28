package installer

import (
	"slices"
	"testing"
)

// BSD install has no -D: the parent is made first, then the file installed.
func TestInstallManagedArgsDarwinAvoidsGNUInstallD(t *testing.T) {
	dir := "/Library/Application Support/ClaudeCode/managed-settings.d"
	got := managedInstallArgs("/src/pfm.json", dir+"/pfm.json")
	want := [][]string{{"mkdir", "-p", dir}, {"install", "-m", "0644", "/src/pfm.json", dir + "/pfm.json"}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("argv=%q, want %q", got, want)
	}
}
