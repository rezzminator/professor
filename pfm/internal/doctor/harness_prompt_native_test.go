package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// Explicit REAL-SESSION opt-in: a loopback sink rejects the request before any
// inference. Ordinary jailed tests never need a native CLI or account.
func TestHarnessPromptNativeRepeatedCapture(t *testing.T) {
	binary := os.Getenv("PFM_TEST_CLAUDE_NATIVE")
	if binary == "" {
		t.Skip("set PFM_TEST_CLAUDE_NATIVE for localhost-only real CLI capture")
	}
	machine := config.Config{}
	machine.Claude.Binary = binary
	for _, model := range harnessPromptModels {
		baselineDir := filepath.Join("..", "..", "harness-prompts", "claude", "baselines")
		pin, err := os.ReadFile(filepath.Join(baselineDir, model.Stem+".sha256"))
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(pin))
		if len(fields) != 2 {
			t.Fatal("invalid native baseline pin")
		}
		baseline, err := os.ReadFile(filepath.Join(baselineDir, fields[1]))
		if err != nil {
			t.Fatal(err)
		}

		var previous HarnessCapture
		for attempt := 0; attempt < 3; attempt++ {
			captured, err := captureHarnessPrompt(context.Background(), t.TempDir(), machine, model.Alias, "")
			if err != nil {
				t.Fatal(err)
			}
			if normalizeHarnessPrompt(captured.Prompt) != normalizeHarnessPrompt(string(baseline)) {
				t.Fatalf("%s built-in instructions differ from reviewed baseline", model.Alias)
			}
			if captured.ResolvedModel == "" || captured.CLIVersion == "" {
				t.Fatalf("capture lacks identity: %+v", captured)
			}
			if attempt > 0 &&
				(captured.ResolvedModel != previous.ResolvedModel || normalizeHarnessPrompt(captured.Prompt) != normalizeHarnessPrompt(previous.Prompt)) {
				t.Fatal("repeated native captures changed beyond normalized metadata")
			}
			sum := sha256.Sum256([]byte(normalizeHarnessPrompt(captured.Prompt)))
			t.Logf(
				"capture=%d requested=%s resolved=%s cli=%s normalized_sha256=%s",
				attempt+1,
				model.Alias,
				captured.ResolvedModel,
				captured.CLIVersion,
				hex.EncodeToString(sum[:]),
			)
			if dir := os.Getenv("PFM_HARNESS_CAPTURE_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(captured)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, model.Alias+"-capture.json"), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			previous = captured
		}
	}
}
