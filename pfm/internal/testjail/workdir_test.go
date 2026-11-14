package testjail

import (
	"os"
	"testing"
)

func TestChdirUnnamableLeavesGetwdNoName(t *testing.T) {
	ChdirUnnamable(t)
	if _, err := os.Stat("."); err != nil {
		t.Fatalf("stat . = %v, want the working directory to exist", err)
	}
	dir, err := os.Getwd()
	if err == nil || err.Error() != "getwd: file name too long" {
		t.Fatalf("os.Getwd() = %q, %v; want getwd: file name too long", dir, err)
	}
}
