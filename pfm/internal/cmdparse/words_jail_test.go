//go:build jail

package cmdparse

// bash is the oracle: what Quote emits must read back through a real shell as the one word it was.

import (
	"os/exec"
	"testing"
)

// TestQuoteSurvivesAShell: what Quote emits, bash reads back as the one word it was.
func TestQuoteSurvivesAShell(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"plain", "it's", `^Test(A|B)$`, "a b", `$HOME;rm -rf x`, "two\nlines", ""} {
		out, err := exec.Command("bash", "-c", "printf %s "+Quote(s)).Output()
		if err != nil || string(out) != s {
			t.Fatalf("Quote(%q) = %s round-trips through bash to %q (%v)", s, Quote(s), out, err)
		}
	}
}
