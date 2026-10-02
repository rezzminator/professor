package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A confinement list whose roots all fail to resolve must refuse every read.
// Before, each unresolvable root was logged and dropped, leaving an empty
// resolved list — which reads as UNCONFINED: an external gateway whose cache
// root was missing could read anywhere a local caller could.
func TestDenyLocalPathFailsClosedWhenNoRootResolves(t *testing.T) {
	t.Parallel()
	outside := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "never-created-cache")
	reason := DenyLocalPath(outside, []string{missing})
	if reason == "" || !strings.Contains(reason, "refusing") {
		t.Fatalf("DenyLocalPath(outside, [missing root]) = %q; want a refusal", reason)
	}
	if reason := DenyLocalPath(outside, nil); reason != "" {
		t.Fatalf("an empty confinement list is unconfined, got refusal %q", reason)
	}
}
