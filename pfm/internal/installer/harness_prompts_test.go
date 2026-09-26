package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// readHarnessPromptPart reads one part of the embedded tree through the same
// door the installer uses. There is exactly one copy of the tree — the
// pfm/harness-prompts package — so a part is read here, never compared
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

// Applying to a clean home leaves the clone's prompts in place and preserves
// any pre-existing legacy directory owned by the operator.
func TestInstallUsesClonePromptsWithoutStaging(t *testing.T) {
	home := t.TempDir()
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
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
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
		t.Fatalf("existing legacy dir changed: %q %v", raw, err)
	}
}
