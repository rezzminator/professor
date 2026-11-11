package servicemanager

import "testing"

func TestNativeIsTheHostsServiceManager(t *testing.T) {
	if got := Native(); got != KindSystemd {
		t.Fatalf("Native() on linux = %q, want %q", got, KindSystemd)
	}
}
