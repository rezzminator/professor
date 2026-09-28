package installer

import (
	"slices"
	"testing"
)

func TestInstallManagedArgsLinuxIsGNUInstallD(t *testing.T) {
	got := managedInstallArgs("/src/pfm.json", "/etc/claude-code/managed-settings.d/pfm.json")
	want := [][]string{{"install", "-D", "-m", "0644", "/src/pfm.json", "/etc/claude-code/managed-settings.d/pfm.json"}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("argv=%q, want %q", got, want)
	}
}
