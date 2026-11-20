package paths

import (
	"runtime"
	"testing"
)

func TestPlatformIsTheBinarysOSAndArch(t *testing.T) {
	goos, goarch := Platform()
	if goos != runtime.GOOS || goarch != runtime.GOARCH {
		t.Fatalf("Platform() = %q, %q; want %q, %q", goos, goarch, runtime.GOOS, runtime.GOARCH)
	}
}
