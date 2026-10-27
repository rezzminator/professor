//go:build darwin

package hookentry

import "testing"

func TestSeatHostDarwinForegroundRestore(t *testing.T) {
	if err := seatForeground(-1, 17); err == nil {
		t.Fatal("invalid terminal accepted a foreground process group")
	}
}
