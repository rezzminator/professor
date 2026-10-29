package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// readHarnessPromptPart reads one part of the embedded tree, the copy a host
// with no source clone composes from. There is exactly one source of the tree
// — the pfm/harness-prompts package — so a part is read here, never compared
// against a second on-disk twin.
func readHarnessPromptPart(t *testing.T, relative string) []byte {
	t.Helper()
	content, err := harnessprompts.ReadPart(relative)
	if err != nil {
		t.Fatalf("read harness prompt part %s: %v", relative, err)
	}
	return content
}

func TestHarnessBaselineAssetPairIsCoherent(t *testing.T) {
	t.Parallel()
	for _, stem := range []string{"harness-original", "harness-opus"} {
		t.Run(stem, func(t *testing.T) {
			baselines := path.Join("claude", "baselines")
			pin := readHarnessPromptPart(t, path.Join(baselines, stem+".sha256"))
			fields := bytes.Fields(pin)
			if len(fields) != 2 {
				t.Fatalf("malformed baseline pin: %q", pin)
			}
			name := string(fields[1])
			prompt := readHarnessPromptPart(t, path.Join(baselines, name))
			sum := sha256.Sum256(prompt)
			if hex.EncodeToString(sum[:]) != string(fields[0]) {
				t.Fatal("baseline body does not match pinned hash")
			}
			model := readHarnessPromptPart(t, path.Join(baselines, stem+".model"))
			if len(bytes.TrimSpace(model)) == 0 {
				t.Fatal("baseline model provenance missing")
			}
		})
	}
}

// The tree's README is embedded so doctor can compare both trees whole, and
// must never reach an operator's managed root as a staged asset.
func TestHarnessPromptReadmeIsEmbeddedButNeverStaged(t *testing.T) {
	t.Parallel()
	if _, err := harnessprompts.ReadPart("README.md"); err != nil {
		t.Fatalf("read embedded README.md: %v", err)
	}
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.path == path.Join(harnessprompts.DirName, "README.md") {
			t.Fatalf("README is listed as a staged asset")
		}
	}
}

// Applying to a clean clone generates every engine prompt before consumers
// run, remains idempotent, and preserves operator-owned legacy files.
func TestInstallGeneratesClonePrompts(t *testing.T) {
	home := t.TempDir()
	clone := t.TempDir()
	configPath := filepath.Join(home, "pfm.config.json")
	if err := os.WriteFile(configPath, []byte("{\"version\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvConfig, configPath)
	if _, err := Run(context.Background(), Options{
		Mode:          ModeApply,
		Home:          home,
		SourceRepo:    clone,
		MCPConfigPath: configPath,
		Runner:        &fakeRunner{},
		Stdout:        io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	generated := map[string]os.FileInfo{}
	for _, engine := range []string{"claude", "codex", "opencode"} {
		filename := filepath.Join(clone, "pfm", "harness-prompts", "generated", engine+".md")
		got, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("install did not generate %s: %v", engine, err)
		}
		id, err := pfmengine.Parse(engine)
		if err != nil {
			t.Fatal(err)
		}
		want, err := assembledHarnessPrompt(clone, id)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) || len(bytes.TrimSpace(got)) == 0 {
			t.Fatalf("generated %s prompt differs from its assembled source parts", engine)
		}
		info, err := os.Stat(filename)
		if err != nil {
			t.Fatal(err)
		}
		generated[filename] = info
	}
	legacy := paths.LegacyHarnessPromptsDir(home)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("install created legacy prompt dir: %v", err)
	}
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(legacy, "operator-file")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(
		context.Background(),
		Options{
			Mode:          ModeApply,
			Home:          home,
			SourceRepo:    clone,
			MCPConfigPath: configPath,
			Runner:        &fakeRunner{},
			Stdout:        io.Discard,
		},
	); err != nil {
		t.Fatal(err)
	}
	for filename, before := range generated {
		after, err := os.Stat(filename)
		if err != nil {
			t.Fatal(err)
		}
		if !before.ModTime().Equal(after.ModTime()) {
			t.Fatalf("reinstall rewrote unchanged prompt %s", filename)
		}
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
		t.Fatalf("existing legacy dir changed: %q %v", raw, err)
	}
}

// A failed output enumeration is an install error, not permission to continue
// configuring consumers against a missing or empty prompt.
func TestInstallNamesHarnessGenerationFailure(t *testing.T) {
	home, clone := t.TempDir(), t.TempDir()
	seedHarnessPromptTree(t, clone, nil)
	configPath := filepath.Join(home, "pfm.config.json")
	writeFixture(t, configPath, "{\"version\":2}\n")
	blocked := filepath.Join(clone, "pfm", "harness-prompts", "generated")
	writeFixture(t, blocked, "not a directory")
	t.Setenv(paths.EnvConfig, configPath)
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, SourceRepo: clone, MCPConfigPath: configPath,
		Runner: &fakeRunner{}, Stdout: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "generate harness prompts") ||
		!strings.Contains(err.Error(), blocked) {
		t.Fatalf("generation failure was not named: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("consumer ran after generation failure: %v", err)
	}
}

// cloneHeadLine marks a clone's head part that the embedded tree never holds.
const cloneHeadLine = "clone-only head line: edited after this binary was built"

// seedHarnessPromptTree lays the embedded harness-prompt tree into clone as
// its source parts, each override replacing one part's content.
func seedHarnessPromptTree(t *testing.T, clone string, overrides map[string]string) {
	t.Helper()
	root := filepath.Join(clone, "pfm", harnessprompts.DirName)
	err := fs.WalkDir(harnessprompts.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(harnessprompts.FS(), name)
		if err != nil {
			return err
		}
		if override, ok := overrides[name]; ok {
			content = []byte(override)
		}
		writeFixture(t, filepath.Join(root, filepath.FromSlash(name)), string(content))
		return nil
	})
	if err != nil {
		t.Fatalf("seed harness prompt tree in %s: %v", clone, err)
	}
}

// Install composes from the clone's parts: a head edited after the binary was
// built reaches the generated prompt, where composing from the embedded parts
// would overwrite a fresh `pfm generate` with the stale head.
func TestInstallComposesFromTheCloneTreeNotTheEmbed(t *testing.T) {
	home, clone := t.TempDir(), t.TempDir()
	head, err := harnessprompts.ReadPart("share/head.md")
	if err != nil {
		t.Fatal(err)
	}
	seedHarnessPromptTree(t, clone, map[string]string{"share/head.md": string(head) + "\n" + cloneHeadLine + "\n"})
	configPath := filepath.Join(home, "pfm.config.json")
	writeFixture(t, configPath, "{\"version\":2}\n")
	t.Setenv(paths.EnvConfig, configPath)
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, SourceRepo: clone, MCPConfigPath: configPath,
		Runner: &fakeRunner{}, Stdout: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	generated := readFixture(t, filepath.Join(clone, "pfm", "harness-prompts", "generated", "claude.md"))
	if !strings.Contains(generated, cloneHeadLine) {
		t.Fatalf("install composed generated/claude.md without the clone's edited head:\n%.300s", generated)
	}
}
