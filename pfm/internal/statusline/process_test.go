package statusline

import (
	"context"
	"testing"
)

// TestReadCodexRateLimitsNamesAMissingBinary: a Codex binary absent from
// PATH fails at resolution with its name, never as a later exec error.
func TestReadCodexRateLimitsNamesAMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := ReadCodexRateLimitsWithBinaryAtHome(context.Background(), "codex", "")
	want := `resolve Codex binary "codex": exec: "codex": executable file not found in $PATH`
	if err == nil || err.Error() != want {
		t.Fatalf("ReadCodexRateLimitsWithBinaryAtHome() error = %v, want %q", err, want)
	}
}
