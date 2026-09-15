package professor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashTemplateUsesExactBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "template.md")
	if err := os.WriteFile(path, []byte("tokens {INTACT}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := HashTemplate(path)
	if err != nil {
		t.Fatalf("HashTemplate() error = %v", err)
	}
	if want := "sha256:412cbdff5f79034e7859d0a78867404b09b44d2a46c8f38509ae8dc169563df6"; got != want {
		t.Fatalf("HashTemplate() = %q, want %q", got, want)
	}
}

func TestHashTemplateNamesUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "template.md")
	if err := os.WriteFile(path, []byte("secret\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := HashTemplate(path)
	if err == nil || !strings.Contains(err.Error(), "UNREADABLE") ||
		!strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("HashTemplate() error = %v, want UNREADABLE with OS error", err)
	}
}

func TestResolveStoreDefaultsToSelfHostedUnknownWithoutGit(t *testing.T) {
	home := t.TempDir()
	blueprint := filepath.Join(home, ".professor")
	if err := os.MkdirAll(filepath.Join(blueprint, "templates", "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blueprint, "VERSION"), []byte("0.65.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := ResolveStore(t.TempDir(), home)
	if err != nil {
		t.Fatalf("ResolveStore() error = %v", err)
	}
	if store.Root != blueprint || store.Version != "0.65.0" || store.SHA != "self-hosted@unknown" {
		t.Fatalf("ResolveStore() = %#v", store)
	}
}
