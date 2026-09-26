package semver

import "testing"

func TestSelectHighestSemverUsesParsedComponents(t *testing.T) {
	got, err := SelectHighest([]string{"v0.9.0", "v0.10.0", "v0.10.0-rc1", "notes"})
	if err != nil {
		t.Fatalf("SelectHighest() error = %v", err)
	}
	if got != "v0.10.0" {
		t.Fatalf("SelectHighest() = %q, want v0.10.0", got)
	}
}
