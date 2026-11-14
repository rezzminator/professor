package servicemanager

import "testing"

func TestNativeIsTheHostsServiceManager(t *testing.T) {
	if got := Native(); got != KindLaunchd {
		t.Fatalf("Native() on darwin = %q, want %q", got, KindLaunchd)
	}
}
