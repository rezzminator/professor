package main

import (
	"strings"
	"testing"
)

// A Codex /rename fires name-sync for the chat's native Herdr seat; under load
// the tmux window half took seconds, so the seat's name must not wait behind
// it, nor be lost when it fails.
func TestNameSyncHalvesRunsTheNativeSeatsFirst(t *testing.T) {
	for _, row := range []struct {
		name      string
		apply     bool
		nativeOK  bool
		windows   int
		wantOrder string
		wantCode  int
	}{
		{"apply, both halves pass", true, true, 0, "native windows", 0},
		{"apply, the window half fails", true, true, 1, "native windows", 1},
		{"apply, the native half fails", true, false, 0, "native windows", 1},
		{"apply, both halves fail", true, false, 1, "native windows", 1},
		{"preview", false, true, 0, "windows", 0},
		{"preview, the window half fails", false, true, 1, "windows", 1},
	} {
		t.Run(row.name, func(t *testing.T) {
			var ran []string
			code := nameSyncHalves(
				row.apply,
				func() bool { ran = append(ran, "native"); return row.nativeOK },
				func() int { ran = append(ran, "windows"); return row.windows },
			)
			if got := strings.Join(ran, " "); got != row.wantOrder || code != row.wantCode {
				t.Fatalf("nameSyncHalves ran %q and exited %d, want %q and %d", got, code, row.wantOrder, row.wantCode)
			}
		})
	}
}
