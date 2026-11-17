package installping

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallIDIsMadeOnceAndReused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	first, err := installID(dir)
	if err != nil || !idPattern.MatchString(first) {
		t.Fatalf("first installID = %q, %v; want a lowercase UUIDv4", first, err)
	}
	second, err := installID(dir)
	raw, _ := os.ReadFile(filepath.Join(dir, "install-ping-id"))
	if err != nil || second != first || string(raw) != first+"\n" {
		t.Fatalf("second installID = %q, %v, file %q; want %q kept", second, err, raw, first)
	}
	if err := os.WriteFile(filepath.Join(dir, "install-ping-id"), []byte("not-an-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replaced, err := installID(dir)
	if err != nil || !idPattern.MatchString(replaced) || replaced == first {
		t.Fatalf("installID over a malformed file = %q, %v; want a fresh UUIDv4", replaced, err)
	}
	if _, err := installID(
		"relative/state",
	); err == nil ||
		err.Error() != `state directory "relative/state" is not absolute` {
		t.Fatalf("installID(relative) error = %v, want the absolute-path refusal", err)
	}
}

func TestClaimDayIsTakenOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	for _, want := range []bool{true, false} {
		if claimed, err := claimDay(dir, "2026-11-17"); err != nil || claimed != want {
			t.Fatalf("claimDay(%s, 2026-11-17) = %v, %v; want %v, nil", dir, claimed, err, want)
		}
	}
	if claimed, err := claimDay(dir, "2026-11-18"); err != nil || !claimed {
		t.Fatalf("claimDay(%s, 2026-11-18) = %v, %v; want true, nil", dir, claimed, err)
	}
}
