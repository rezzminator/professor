package codexappendix

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineUnregisterPreservesPersonalConfigAndSymlink(t *testing.T) {
	account := t.TempDir()
	physical := filepath.Join(account, "personal.toml")
	raw := "# keep this comment\nmodel='personal-model'\n[features]\nhooks=false\n[hooks.state.\"owned.path:key\"]\nenabled=true\ntrusted_hash='sha256:owned'\n[hooks.state.personal]\nenabled=false\ntrusted_hash='sha256:personal'\n"
	if err := os.WriteFile(physical, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(account, "config.toml")
	if err := os.Symlink(physical, path); err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, account, `{"owned.path:key": "sha256:owned"}`)
	if err := Unregister(account); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "sha256:owned") || !strings.Contains(string(got), "sha256:personal") ||
		!strings.HasPrefix(string(got), "# keep this comment\nmodel='personal-model'\n") {
		t.Fatalf("cleanup changed personal settings: %s", got)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("config symlink replaced")
	}
	if err := Unregister(account); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}
}

func TestTrustCleanupRejectsUnsafeInlineLayout(t *testing.T) {
	raw := "[hooks]\nstate={owned={enabled=true,trusted_hash='hash'},personal={enabled=false}}\n"
	if _, err := removeRecordedTrust(raw, map[string]string{"owned": "hash"}); err == nil {
		t.Fatal("unsafe mixed inline edit accepted")
	}
}

// A receipt holding JSON null decodes to a nil map. The cleanup must read it
// as "nothing recorded" and retire the receipt, never panic on it.
func TestNullReceiptFailsWithoutPanic(t *testing.T) {
	account := t.TempDir()
	writeReceipt(t, account, "null")
	if err := Unregister(account); err != nil {
		t.Fatalf("cleanup over a null receipt: %v", err)
	}
	if _, err := os.Stat(receiptPath(account)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("null receipt survived cleanup: %v", err)
	}
}

// writeReceipt stages the trust receipt the retired registration wrote, which
// is the only input the cleanup reads.
func writeReceipt(t *testing.T, account, body string) {
	t.Helper()
	if err := os.WriteFile(receiptPath(account), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
