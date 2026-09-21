package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	config "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// StateUnknown is the stable diagnostic state for a value a probe could not determine.
const StateUnknown = "unknown"

// Baselines follow stable requested aliases. Resolved IDs and the baseline's
// captured model are provenance, never evidence of behavioral drift by themselves.
type HarnessPromptModel struct{ Alias, Stem string }

var HarnessPromptModels = []HarnessPromptModel{{"sonnet", "harness-original"}, {"opus", "harness-opus"}}

func printHarnessPromptDoctor(
	ctx context.Context,
	stdout io.Writer,
	home string,
	machine config.Config,
	verboseDir string,
) int {
	return printHarnessPromptDoctorWithDeps(
		ctx,
		stdout,
		home,
		machine,
		verboseDir,
		normalizeDependencies(Dependencies{}),
	)
}

func printHarnessPromptDoctorWithDeps(
	ctx context.Context,
	stdout io.Writer,
	home string,
	machine config.Config,
	verboseDir string,
	dependencies Dependencies,
) int {
	warnings := 0
	for _, model := range HarnessPromptModels {
		warnings += printModelHarnessPromptDoctorWithDeps(ctx, stdout, home, machine, model, verboseDir, dependencies)
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
	model HarnessPromptModel,
	verboseDir string,
) int {
	return printModelHarnessPromptDoctorWithDeps(
		ctx,
		stdout,
		home,
		machine,
		model,
		verboseDir,
		normalizeDependencies(Dependencies{}),
	)
}

func printModelHarnessPromptDoctorWithDeps(
	ctx context.Context,
	stdout io.Writer,
	home string,
	machine config.Config,
	model HarnessPromptModel,
	verboseDir string,
	dependencies Dependencies,
) int {
	fmt.Fprintf(stdout, "doctor: harness-prompt requested=%s\n", model.Alias)
	baselinePath := filepath.Join(paths.HarnessBaselineDir(home), model.Stem+".sha256")
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
	modelRaw, modelErr := os.ReadFile(filepath.Join(filepath.Dir(baselinePath), model.Stem+".model"))
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
	captured, captureErr := configuredHarnessCaptureWithDeps(ctx, home, machine, model.Alias, verboseDir, dependencies)
	resolved, version := captured.ResolvedModel, captured.CLIVersion
	if resolved == "" {
		resolved = StateUnknown
	}
	if version == "" {
		version = StateUnknown
	}
	fmt.Fprintf(
		stdout,
		"doctor: harness-prompt scope=claude-model-baseline runtime=claude-code requested=%s resolved=%s cli=%q baseline=%s baseline_model=%s unchecked=active-chat,fable,codex\n",
		model.Alias,
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
