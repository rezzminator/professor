package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
// to the clone's baseline. Match, instruction drift, unavailable baseline, and
// failed capture are distinct outcomes. Failed capture is never reported as drift.
func printModelHarnessPromptDoctor(
	ctx context.Context,
	stdout io.Writer,
	home string,
	model HarnessPromptModel,
) int {
	return printModelHarnessPromptDoctorWithDeps(
		ctx,
		stdout,
		home,
		config.Config{},
		model,
		"",
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
	dir, dirErr := paths.HarnessBaselineDir(home)
	if dirErr != nil {
		if errors.Is(dirErr, paths.ErrNoSourceRepoMarker) {
			fmt.Fprintf(
				stdout,
				"doctor: harness-prompt: BASELINE UNAVAILABLE identity=%s model=%q dir=(unresolved) — no source repo recorded, run pfm install from the clone\n",
				model.Stem,
				model.Alias,
			)
			return 1
		}
		fmt.Fprintf(
			stdout,
			"doctor: harness-prompt: BASELINE UNAVAILABLE identity=%s model=%q dir=(unresolved) error=%v — the recorded clone is unusable: restore it, or run pfm install from a working clone\n",
			model.Stem,
			model.Alias,
			dirErr,
		)
		return 1
	}
	baselinePath := filepath.Join(dir, model.Stem+".sha256")
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		state := "baseline file unreadable"
		if errors.Is(err, fs.ErrNotExist) {
			state = "baseline file missing"
		}
		return printHarnessBaselineUnavailable(stdout, model, dir, baselinePath, state, err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		return printHarnessBaselineUnavailable(stdout, model, dir, baselinePath, "baseline file malformed",
			fmt.Errorf("want \"<sha256> <body file>\", found %d field(s)", len(fields)))
	}
	decoded, decodeErr := hex.DecodeString(fields[0])
	if decodeErr == nil {
		switch {
		case len(decoded) != sha256.Size:
			decodeErr = fmt.Errorf("decoded digest has %d byte(s), want %d", len(decoded), sha256.Size)
		case filepath.Base(fields[1]) != fields[1]:
			decodeErr = fmt.Errorf("body file %q is not a base name", fields[1])
		}
	}
	if decodeErr != nil {
		return printHarnessBaselineUnavailable(stdout, model, dir, baselinePath, "baseline digest malformed", decodeErr)
	}
	baselineModel, baseline, unavailable := readHarnessBaseline(baselinePath, model.Stem, fields[0], fields[1])
	if unavailable != nil {
		return printHarnessBaselineUnavailable(
			stdout,
			HarnessPromptModel{
				Alias: baselineModel,
				Stem:  fields[1],
			},
			dir,
			baselinePath,
			"baseline inconsistent",
			unavailable,
		)
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
	for _, detail := range harnessPromptDetail(
		model.Alias,
		baselineModel,
		string(baseline),
		captured,
		captureErr,
		warn && strings.Contains(line, "DRIFT"),
	) {
		fmt.Fprintln(stdout, detail)
	}
	if warn {
		return 1
	}
	return 0
}

func printHarnessBaselineUnavailable(
	stdout io.Writer,
	model HarnessPromptModel,
	dir, path, state string,
	cause error,
) int {
	fmt.Fprintf(
		stdout,
		"doctor: harness-prompt: BASELINE UNAVAILABLE identity=%s model=%q dir=%s path=%s error=%v — %s; update or restore the clone at %s\n",
		model.Stem,
		model.Alias,
		dir,
		path,
		cause,
		state,
		dir,
	)
	return 1
}

// readHarnessBaseline reads the pinned model name and the pinned body beside
// baselinePath and returns the model and the body with nil, or the cause the baseline is
// unusable: the .model read error, an empty .model, the body's read error, or
// the digest the body no longer matches. The cause is what the BASELINE
// UNAVAILABLE row prints, so each of them names itself.
func readHarnessBaseline(baselinePath, stem, pinnedDigest, bodyName string) (string, []byte, error) {
	directory := filepath.Dir(baselinePath)
	modelPath := filepath.Join(directory, stem+".model")
	modelRaw, err := os.ReadFile(modelPath)
	if err != nil {
		return "", nil, err
	}
	baselineModel := strings.TrimSpace(string(modelRaw))
	if baselineModel == "" {
		return "", nil, fmt.Errorf("%s is empty", modelPath)
	}
	body, err := os.ReadFile(filepath.Join(directory, bodyName))
	if err != nil {
		return baselineModel, nil, err
	}
	sum := sha256.Sum256(body)
	if digest := hex.EncodeToString(sum[:]); digest != pinnedDigest {
		return baselineModel, nil, fmt.Errorf("digest %s of %s does not match %s", digest, bodyName, pinnedDigest)
	}
	return baselineModel, body, nil
}
