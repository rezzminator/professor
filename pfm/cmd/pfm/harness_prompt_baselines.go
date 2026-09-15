package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	config "hostops/pfm/internal/config"
)

const unknownState = "unknown"

// Baselines follow stable requested aliases. Resolved IDs and the baseline's
// captured model are provenance, never evidence of behavioral drift by themselves.
type harnessPromptModel struct{ alias, stem string }

var harnessPromptModels = []harnessPromptModel{{"sonnet", "harness-original"}, {"opus", "harness-opus"}}

func printHarnessPromptDoctor(
	ctx context.Context,
	stdout io.Writer,
	home string,
	machine config.Config,
	verboseDir string,
) int {
	warnings := 0
	for _, model := range harnessPromptModels {
		warnings += printModelHarnessPromptDoctor(ctx, stdout, home, machine, model, verboseDir)
	}
	return warnings
}

// printModelHarnessPromptDoctor re-captures the live Claude CLI's built-in system
// prompt through a localhost sink — the request dies at the listener, so no
// tokens are spent and nothing leaves the machine — and compares its sha256
// to the staged baseline. Match, instruction drift, unavailable baseline, and
// failed capture are distinct outcomes. Failed capture is never reported as drift.
func printModelHarnessPromptDoctor(
	ctx context.Context,
	stdout io.Writer,
	home string,
	machine config.Config,
	model harnessPromptModel,
	verboseDir string,
) int {
	fmt.Fprintf(stdout, "doctor: harness-prompt requested=%s\n", model.alias)
	baselinePath := filepath.Join(home, ".local", "share", "pfm", installCommand, "prompts", model.stem+".sha256")
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: harness-prompt: baseline unreadable (%v) — run pfm install\n", err)
		return 1
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		fmt.Fprintf(stdout, "doctor: harness-prompt: baseline malformed at %s — run pfm install\n", baselinePath)
		return 1
	}
	decoded, decodeErr := hex.DecodeString(fields[0])
	if decodeErr != nil || len(decoded) != sha256.Size || filepath.Base(fields[1]) != fields[1] {
		fmt.Fprintln(stdout, "doctor: harness-prompt: baseline malformed — run pfm install")
		return 1
	}
	modelRaw, modelErr := os.ReadFile(filepath.Join(filepath.Dir(baselinePath), model.stem+".model"))
	baselineModel := strings.TrimSpace(string(modelRaw))
	baseline, baselineErr := os.ReadFile(filepath.Join(filepath.Dir(baselinePath), fields[1]))
	baselineSum := sha256.Sum256(baseline)
	if modelErr != nil || baselineModel == "" || baselineErr != nil || hex.EncodeToString(baselineSum[:]) != fields[0] {
		fmt.Fprintf(
			stdout,
			"doctor: harness-prompt: BASELINE UNAVAILABLE identity=%s model=%q — missing, unreadable or inconsistent baseline; run pfm install\n",
			fields[1],
			baselineModel,
		)
		return 1
	}
	captured, captureErr := configuredHarnessCapture(ctx, home, machine, model.alias, verboseDir)
	resolved, version := captured.ResolvedModel, captured.CLIVersion
	if resolved == "" {
		resolved = unknownState
	}
	if version == "" {
		version = unknownState
	}
	fmt.Fprintf(
		stdout,
		"doctor: harness-prompt scope=claude-model-baseline runtime=claude-code requested=%s resolved=%s cli=%q baseline=%s baseline_model=%s unchecked=active-chat,fable,codex\n",
		model.alias,
		resolved,
		version,
		fields[1],
		baselineModel,
	)
	canonicalBaseline := sha256.Sum256([]byte(normalizeHarnessPrompt(string(baseline))))
	line, warn := harnessPromptVerdict(hex.EncodeToString(canonicalBaseline[:]), fields[1], captured.Prompt, captureErr)
	fmt.Fprintln(stdout, line)
	if warn {
		return 1
	}
	return 0
}
