package installer

import "testing"

func TestVSCodePlatformDefaultsToTheHost(t *testing.T) {
	t.Parallel()
	got, err := (&engine{}).vscodePlatform()
	if err != nil || got != vscodePlatformOSX {
		t.Fatalf("vscodePlatform() = (%q, %v), want (%q, nil)", got, err, vscodePlatformOSX)
	}
}
