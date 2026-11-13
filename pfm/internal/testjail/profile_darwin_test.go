package testjail

import (
	"fmt"
	"testing"
)

func TestDarwinRunDelayAndPSIAreErrorsNamingDarwin(t *testing.T) {
	delay, delayErr := runDelaySeconds()
	psi, psiErr := readPSI(psiDir)
	got := fmt.Sprint(delay, " ", delayErr, " ", psi, " ", psiErr)
	if want := "0 run delay: not available on darwin map[] psi: not available on darwin"; got != want {
		t.Fatalf("run delay and psi = %q, want %q", got, want)
	}
}

func TestMaxRSSIsBytesOnDarwin(t *testing.T) {
	if got := maxRSSKB(2048 * 1024); got != 2048 {
		t.Fatalf("maxRSSKB(2 MiB) = %d, want 2048 KB: Darwin reports bytes", got)
	}
}
