package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgentPersona(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeCodexCatalogue writes the Codex catalogue under home/.codex as models_cache.json.
func writeCodexCatalogue(t *testing.T, home, body string) {
	t.Helper()
	root := filepath.Join(home, ".codex")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if body == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(root, "models_cache.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodexModelChecksTheResolvedModelAgainstTheCatalogue(t *testing.T) {
	home := t.TempDir()
	writeCodexCatalogue(t, home, `{"fetched_at":"x","models":[{"slug":"gpt-6-astra"},{"slug":"gpt-6.1-sol"}]}`)
	writeAgentPersona(t, home, "astra", "---\nname: astra\nmodel: gpt-6-astra[1m]\n---\n")
	writeAgentPersona(t, home, "luna", "---\nname: luna\nmodel: gpt-6-luna\n---\n")
	catalogue := filepath.Join(home, ".codex", "models_cache.json")
	tests := []struct{ in, want, wantErr string }{
		{in: "gpt-6.1-sol", want: "gpt-6.1-sol"},
		{in: "astra", want: "gpt-6-astra"},
		{in: "", want: ""},
		{
			in:      "gpt-9-nova",
			wantErr: `codex model "gpt-9-nova" is not offered by Codex (catalogue ` + catalogue + `); offered: gpt-6-astra, gpt-6.1-sol`,
		},
		{
			in:      "nova",
			wantErr: `codex model "nova" is not offered by Codex (catalogue ` + catalogue + `); offered: gpt-6-astra, gpt-6.1-sol`,
		},
		{
			in:      "luna",
			wantErr: `codex model "gpt-6-luna" is not offered by Codex (catalogue ` + catalogue + `); offered: gpt-6-astra, gpt-6.1-sol`,
		},
	}
	for _, test := range tests {
		got, err := CodexModel(test.in, home, filepath.Join(home, ".codex"))
		if test.wantErr != "" {
			if err == nil || err.Error() != test.wantErr {
				t.Errorf("CodexModel(%q) err = %v, want %q", test.in, err, test.wantErr)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("CodexModel(%q) = %q, %v, want %q", test.in, got, err, test.want)
		}
	}
}

func TestCodexModelProceedsWhenTheCatalogueCannotBeRead(t *testing.T) {
	for name, body := range map[string]string{
		"missing": "", "not json": "{", "no models": `{"models":[]}`,
	} {
		home := t.TempDir()
		writeCodexCatalogue(t, home, body)
		got, err := CodexModel("gpt-9-nova", home, filepath.Join(home, ".codex"))
		if err != nil || got != "gpt-9-nova" {
			t.Errorf("%s catalogue: CodexModel = %q, %v, want pass-through", name, got, err)
		}
	}
}

func TestCodexModelResolvesBareTierFromAgentPersona(t *testing.T) {
	home := t.TempDir()
	writeAgentPersona(t, home, "astra", "---\nname: astra\ndescription: astra tier\nmodel: gpt-6-astra[1m]\n---\nb\n")
	writeAgentPersona(t, home, "sol", "---\r\nname: sol\r\nmodel: \"gpt-6.1-sol\"\r\n---\r\n")
	writeAgentPersona(t, home, "tracer", "---\nname: tracer\nmodel: claude-opus-5-5[1m]\n---\n")
	writeAgentPersona(t, home, "nested", "---\nname: nested\nhooks:\n  model: gpt-6-luna\n---\n")
	writeAgentPersona(t, home, "late", "---\nname: late\n---\nmodel: gpt-6-luna\n")
	writeAgentPersona(t, home, "bare", "model: gpt-6-luna\n")
	tests := []struct{ in, want string }{
		{"astra", "gpt-6-astra"},
		{" astra ", "gpt-6-astra"},
		{"sol", "gpt-6.1-sol"},
		{"gpt-6-astra", "gpt-6-astra"},
		{"gpt-6.1-sol", "gpt-6.1-sol"},
		{"", ""},
		{"unregistered", "unregistered"},
		{"tracer", "tracer"},
		{"nested", "nested"},
		{"late", "late"},
		{"bare", "bare"},
	}
	for _, test := range tests {
		got, err := CodexModel(test.in, home, filepath.Join(home, ".codex"))
		if err != nil {
			t.Fatalf("CodexModel(%q): %v", test.in, err)
		}
		if got != test.want {
			t.Errorf("CodexModel(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestCodexModelFullIDNeedsNoHome(t *testing.T) {
	got, err := CodexModel("gpt-6-astra", "", "")
	if err != nil || got != "gpt-6-astra" {
		t.Fatalf("CodexModel full id without home = %q, %v", got, err)
	}
}

func TestCodexModelRefusesWhatItCannotLookUp(t *testing.T) {
	if _, err := CodexModel("astra", "", ""); err == nil || !strings.Contains(err.Error(), "no home") {
		t.Fatalf("bare tier without home: err=%v, want a no-home error", err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "agents", "astra.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexModel(
		"astra",
		home,
		filepath.Join(home, ".codex"),
	); err == nil ||
		!strings.Contains(err.Error(), "read agent persona") {
		t.Fatalf("unreadable persona: err=%v, want a read error", err)
	}
}
