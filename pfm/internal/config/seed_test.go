package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// The seed preview must equal what a load of the seeded target returns, the
// harvester sibling of the target included, and never create the target.
func TestLoadSeedMatchesLoadOfSeededTarget(t *testing.T) {
	home := t.TempDir()
	example := filepath.Join(t.TempDir(), "example.pfm.config.json")
	content := `{"version":2,"theme":"seeded"}`
	if err := os.WriteFile(example, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(HarvesterPath(target), []byte(`{"enabled":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	seeded, err := LoadSeed(example, target, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("LoadSeed wrote the target: %v", err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(target, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(seeded.SeedContent) != content {
		t.Fatalf("seed snapshot=%q", seeded.SeedContent)
	}
	seeded.SeedContent = nil
	if !reflect.DeepEqual(seeded, loaded) {
		t.Fatalf("seed preview differs from the seeded load:\nseed=%#v\nload=%#v", seeded, loaded)
	}
}

func TestLoadSeedNamesAMissingExample(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "example.pfm.config.json")
	_, err := LoadSeed(missing, filepath.Join(t.TempDir(), FileName), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing example err=%v, want it named", err)
	}
}

// pfm install seeds the tracked example without ask.engine so DefaultEngine
// resolves the available roster. A home with no roster still has account 1,
// so it resolves to Claude.
func TestSeededExampleLoadsWithoutACodexAccount(t *testing.T) {
	example := examplePath(t)
	t.Run("claude-only", func(t *testing.T) {
		body, err := os.ReadFile(example)
		if err != nil {
			t.Fatal(err)
		}
		var seeded map[string]any
		if err := json.Unmarshal(body, &seeded); err != nil {
			t.Fatalf("parse %s: %v", example, err)
		}
		seeded["accounts"] = []map[string]any{{"id": 1, "configDir": "~/claude-one"}}
		content, err := json.Marshal(seeded)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), FileName)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, t.TempDir(), nil)
		if err != nil {
			t.Fatalf("Load(seeded example, Claude-only home) error=%v, want nil", err)
		}
		engine, err := loaded.DefaultEngine()
		if err != nil || engine != pfmengine.Claude {
			t.Fatalf("DefaultEngine()=(%q,%v), want (%q,nil)", engine, err, pfmengine.Claude)
		}
	})
	t.Run("no-roster", func(t *testing.T) {
		home := t.TempDir()
		seed, err := LoadSeed(example, filepath.Join(t.TempDir(), FileName), home, nil)
		if err != nil {
			t.Fatalf("LoadSeed(example, empty home) error=%v, want nil", err)
		}
		if engine, err := seed.DefaultEngine(); err != nil || engine != pfmengine.Claude {
			t.Fatalf("DefaultEngine()=(%q,%v), want (%q,nil)", engine, err, pfmengine.Claude)
		}
		want := []Account{{ID: 1, ConfigDir: DefaultAccountDir(home, 1), Emoji: DefaultEmoji(1)}}
		if !reflect.DeepEqual(seed.Accounts, want) {
			t.Fatalf("seed.Accounts=%#v, want %#v", seed.Accounts, want)
		}
	})
}
