//go:build linux

package themesync

import "testing"

// A linux host serves linuxPlatform when Env.Platform is nil.
func TestHostPlatformIsLinux(t *testing.T) {
	if hostPlatform != linuxPlatform {
		t.Errorf("hostPlatform = %+v, want linuxPlatform %+v", hostPlatform, linuxPlatform)
	}
}
