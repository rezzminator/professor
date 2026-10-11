//go:build !linux && !darwin

package atomicfile

import "testing"

func TestSwapPathsIsUnsupportedHere(t *testing.T) {
	if err := swapPaths("a", "b"); err == nil {
		t.Fatal("swapPaths succeeded on a platform without an atomic exchange")
	}
}
