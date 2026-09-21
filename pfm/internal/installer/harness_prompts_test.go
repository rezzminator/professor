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
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// readHarnessPromptPart reads one part of the embedded tree through the same
// door the installer uses. There is exactly one copy of the tree — the
// pfm/harness-prompts package — so a part is read here, never compared
// against a second on-disk twin.
func readHarnessPromptPart(t *testing.T, relative string) []byte {
	t.Helper()
	content, err := readAsset(path.Join(harnessPromptsDirName, relative))
	if err != nil {
		t.Fatalf("read harness prompt part %s: %v", relative, err)
	}
	return content
}

func TestHarnessBaselineAssetPairIsCoherent(t *testing.T) {
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
	if _, err := readAsset(path.Join(harnessPromptsDirName, harnessPromptReadme)); err != nil {
		t.Fatalf("read embedded %s: %v", harnessPromptReadme, err)
	}
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	staged := path.Join(harnessPromptsDirName, harnessPromptReadme)
	parts := 0
	for _, asset := range assets {
		if _, isPart := harnessPromptAssetName(asset.path); !isPart {
			continue
		}
		parts++
		if asset.path == staged {
			t.Fatalf("%s is listed as a staged asset", staged)
		}
	}
	if parts == 0 {
		t.Fatal("no harness prompt parts listed among the staged assets")
	}
}

// A fresh apply stages one prompt per engine, and each one is its three parts
// with exactly one blank line at each seam. The expectation is spelled out
// here from the embedded parts rather than taken from composeHarnessPrompt,
// so a change to the joining rule has to be made twice to pass.
func TestInstallStagesComposedHarnessPrompts(t *testing.T) {
	home := t.TempDir()
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	head := string(readHarnessPromptPart(t, path.Join("share", "head.md")))
	tail := string(readHarnessPromptPart(t, path.Join("share", "tail.md")))
	for _, id := range harnessPromptEngines {
		long := pfmengine.MustLookup(id).LongName
		t.Run(long, func(t *testing.T) {
			middle := string(readHarnessPromptPart(t, path.Join(long, "professor.md")))
			staged := filepath.Join(home, ".local", "share", "pfm", "install", harnessPromptsDirName, long+".md")
			actual, err := os.ReadFile(staged)
			if err != nil {
				t.Fatalf("staged %s prompt: %v", long, err)
			}
			if id == pfmengine.Codex {
				// Codex's prompt is the same three parts with one mapping
				// applied at compose time: Claude's /code-review names a
				// command no Codex seat has, and an unmapped seat falls
				// through to Codex's whole-branch review. Head and middle
				// carry no invocation, so the seams are still asserted
				// byte-for-byte and only the tail is allowed to differ.
				prefix := strings.TrimRight(head, "\n") + "\n\n" + strings.TrimRight(middle, "\n") + "\n\n"
				if !strings.HasPrefix(string(actual), prefix) {
					t.Fatalf("staged %s.md is not head + middle + a tail", long)
				}
				mappedTail := strings.TrimPrefix(string(actual), prefix)
				if strings.Contains(mappedTail, "/code-review") {
					t.Fatalf("staged %s.md still spells /code-review:\n%s", long, mappedTail)
				}
				if !strings.Contains(mappedTail, `codex review -c model="`) {
					t.Fatalf("staged %s.md carries no scoped codex review command:\n%s", long, mappedTail)
				}
				return
			}
			want := strings.TrimRight(head, "\n") + "\n\n" +
				strings.TrimRight(middle, "\n") + "\n\n" +
				strings.TrimRight(tail, "\n") + "\n"
			if string(actual) != want {
				t.Fatalf("staged %s.md is not head + middle + tail", long)
			}
			if !strings.Contains(string(actual), "/code-review low") {
				t.Fatalf("staged %s.md lost /code-review low — its engine runs the Claude command", long)
			}
			// The parts each end in a single newline today, so the seams are
			// also plain concatenation — a second, independent reading of
			// "exactly one blank line, no other bytes added".
			if string(actual) != head+"\n"+middle+"\n"+tail {
				t.Fatalf("staged %s.md seams are not one blank line over the parts as shipped", long)
			}
		})
	}
	// The parts and the Claude drift baselines stage beside the composed
	// prompts, in the one place doctor is pointed at.
	for _, relative := range []string{
		filepath.Join("share", "head.md"),
		filepath.Join("claude", "baselines", "harness-original.sha256"),
		filepath.Join("claude", "baselines", "harness-opus.sha256"),
	} {
		staged := filepath.Join(home, ".local", "share", "pfm", "install", harnessPromptsDirName, relative)
		if _, err := os.Stat(staged); err != nil {
			t.Fatalf("staged %s: %v", relative, err)
		}
	}
}
