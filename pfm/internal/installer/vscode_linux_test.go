package installer

import "testing"

func TestVSCodePlatformDefaultsToTheHost(t *testing.T) {
	t.Parallel()
	got, err := (&engine{}).vscodePlatform()
	if err != nil || got != vscodePlatformLinux {
		t.Fatalf("vscodePlatform() = (%q, %v), want (%q, nil)", got, err, vscodePlatformLinux)
	}
}
