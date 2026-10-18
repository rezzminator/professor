package installer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditSettingsErrorsOnAnAbsentFileAndCreatesNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	called := false
	err := EditSettings(path, func(map[string]any) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want one naming %s as absent", err, path)
	}
	if called {
		t.Error("apply ran against a file that does not exist")
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the edit created %s: stat err = %v", path, statErr)
	}
}

func TestEditSettingsWritesThroughASymlinkKeepingModeAndNumberSpellings(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "settings.json")
	link := filepath.Join(dir, ".cc", "1", "settings.json")
	for _, parent := range []string{filepath.Dir(target), filepath.Dir(link)} {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		target,
		[]byte(`{"n": 1.50, "big": 12345678901234567890, "theme": "dark"}`),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil { // the umask must not narrow what the test pins
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := EditSettings(link, func(document map[string]any) error {
		document["theme"] = "custom:gold"
		return nil
	}); err != nil {
		t.Fatalf("EditSettings: %v", err)
	}

	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced by a file: %v, %v", info, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", info.Mode().Perm())
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"theme": "custom:gold"`, `"n": 1.50`, `"big": 12345678901234567890`} {
		if !strings.Contains(string(content), want) {
			t.Errorf("target lacks %s:\n%s", want, content)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the atomic write left %d files beside the target, want 1", len(entries))
	}
}

func TestEditSettingsLeavesTheFileAloneWhenApplyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const original = `{"theme":"dark"}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	err := EditSettings(path, func(document map[string]any) error {
		document["theme"] = "half-written"
		return errors.New("refused by the caller")
	})
	if err == nil || !strings.Contains(err.Error(), "refused by the caller") || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want the apply failure naming %s", err, path)
	}
	if content, _ := os.ReadFile(path); string(content) != original {
		t.Errorf("the file changed on a failed apply:\n%s", content)
	}
}

func TestEditSettingsLeavesAnUnparsableFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := EditSettings(path, func(document map[string]any) error {
		document["theme"] = "dark"
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want a parse error naming %s", err, path)
	}
	if content, _ := os.ReadFile(path); string(content) != `{` {
		t.Errorf("the unparsable file was rewritten:\n%s", content)
	}
}

func TestEditSettingsKeepsAKeyAnotherWriterAddedBetweenTheReadAndTheRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runs := 0
	err := EditSettings(path, func(document map[string]any) error {
		runs++
		if runs == 1 { // a running chat writes the file while the edit is planned
			if err := os.WriteFile(path, []byte(`{"theme":"dark","chat":"wrote this"}`), 0o644); err != nil {
				return err
			}
		}
		document["theme"] = "custom:gold"
		return nil
	})
	if err != nil {
		t.Fatalf("EditSettings: %v", err)
	}
	content, _ := os.ReadFile(path)
	if runs != 2 || !strings.Contains(string(content), `"chat": "wrote this"`) ||
		!strings.Contains(string(content), `"theme": "custom:gold"`) {
		t.Fatalf("apply ran %d times, file:\n%s\nwant the other writer's key kept and apply re-run once", runs, content)
	}
}
