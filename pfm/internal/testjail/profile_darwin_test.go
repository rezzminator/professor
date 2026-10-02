package testjail

import (
	"strings"
	"testing"
)

func TestDarwinRunDelayAndPSIAreErrorsNamingDarwin(t *testing.T) {
	if got, err := runDelaySeconds(); err == nil || !strings.Contains(err.Error(), "darwin") {
		t.Fatalf("runDelaySeconds = %v, %v, want an error naming darwin", got, err)
	}
	if got, err := readPSI(psiDir); err == nil || got != nil || !strings.Contains(err.Error(), "darwin") {
		t.Fatalf("readPSI = %v, %v, want an error naming darwin and no values", got, err)
	}
}

func TestMaxRSSIsBytesOnDarwin(t *testing.T) {
	if got := maxRSSKB(2048 * 1024); got != 2048 {
		t.Fatalf("maxRSSKB(2 MiB) = %d, want 2048 KB: Darwin reports bytes", got)
	}
}
