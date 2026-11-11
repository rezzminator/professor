//go:build darwin

package themesync

import "testing"

// A darwin host serves darwinPlatform when Env.Platform is nil.
func TestHostPlatformIsDarwin(t *testing.T) {
	if hostPlatform != darwinPlatform {
		t.Errorf("hostPlatform = %+v, want darwinPlatform %+v", hostPlatform, darwinPlatform)
	}
}
