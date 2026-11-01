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

// TestParseVersionAcceptsOnlyAFullReleaseTag: ParseVersion takes vMAJOR.MINOR.PATCH
// alone, while Valid also takes a prerelease and build, never a short form.
func TestParseVersionAcceptsOnlyAFullReleaseTag(t *testing.T) {
	for _, tc := range []struct {
		tag            string
		release, valid bool
	}{
		{"v1.2.3", true, true},
		{" v1.2.3\n", true, false},
		{"v1.2.3-alpha", false, true},
		{"v1.2.3-dev.2", false, true},
		{"v1.2.3+build", false, true},
		{"v1.2", false, false},
		{"v1", false, false},
		{"1.2.3", false, false},
		{"v01.2.3", false, false},
		{"", false, false},
	} {
		_, release := ParseVersion(tc.tag)
		if release != tc.release || Valid(tc.tag) != tc.valid {
			t.Fatalf(
				"%q: ParseVersion ok=%t Valid=%t, want %t %t",
				tc.tag,
				release,
				Valid(tc.tag),
				tc.release,
				tc.valid,
			)
		}
	}
	if !Version("v0.9.0").Less(Version("v0.10.0")) || Compare("v1.0.0-dev.2", "v1.0.0-dev.10") != -1 ||
		Compare("v1.0.0-alpha", "v1.0.0") != -1 {
		t.Fatal("ordering is not SemVer precedence")
	}
}
