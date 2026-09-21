package doctor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
)

// The Codex fleet prompt reaches a session through config.toml's
// developer_instructions: it is the first developer item of every thread,
// rebuilt verbatim after compaction, inherited by every default-role
// sub-agent. If the key is absent, or holds anything but the prompt this
// binary composes, every Codex seat started from that account runs without
// the fleet prompt and nothing else says so. This row says so — and it keeps
// its outcomes apart, because "the prompt is there", "something else is
// there", "the key is absent" and "the config could not be read" are four
// different facts, and only the first is a clean bill.

// codexInstructionsReport is one account's verdict. Err is set ONLY when the
// config could not be read or parsed — a config that WAS read and holds the
// wrong value reports Bytes/Want, never an error.
type codexInstructionsReport struct {
	Path    string
	Present bool
	Bytes   int
	Want    int
	Err     error
}

// PrintCodexDeveloperInstructions checks every Codex account's config.toml
// against the prompt this binary composes and returns the failure count. An
// account whose config carries something else is a state `pfm install --yes`
// owns and did not produce, so it is a FAILURE, never a warning.
func PrintCodexDeveloperInstructions(stdout io.Writer, homes []string) int {
	// codexgen is the one door that composes this prompt: it applies the Codex
	// mappings (review.go) the shared parts do not carry on disk, so comparing
	// a config against the raw parts would read a correctly installed account
	// as a mismatch.
	prompt, err := codexgen.FleetPrompt()
	if err != nil {
		fmt.Fprintf(
			stdout,
			"doctor: codex developer_instructions=CHECK FAILED error=%v — this binary could not compose the "+
				"Codex prompt, so whether any account carries it is UNKNOWN, not clean\n",
			err,
		)
		return 1
	}
	if len(homes) == 0 {
		fmt.Fprint(stdout, "doctor: codex developer_instructions=no-accounts — no Codex account is configured\n")
		return 0
	}
	failures := 0
	for _, home := range homes {
		failures += printCodexInstructionsReport(stdout, inspectCodexInstructions(home, prompt))
	}
	return failures
}

func inspectCodexInstructions(home, prompt string) codexInstructionsReport {
	path := filepath.Join(home, "config.toml")
	report := codexInstructionsReport{Path: path, Want: len(prompt)}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			report.Err = fmt.Errorf("no config.toml at %s (run pfm install --yes)", path)
			return report
		}
		report.Err = fmt.Errorf("read %s: %w", path, err)
		return report
	}
	var document map[string]any
	if _, err := toml.Decode(string(raw), &document); err != nil {
		report.Err = fmt.Errorf("parse %s: %w", path, err)
		return report
	}
	value, present := document["developer_instructions"]
	if !present {
		return report
	}
	instructions, ok := value.(string)
	if !ok {
		report.Err = fmt.Errorf("developer_instructions in %s is not a string", path)
		return report
	}
	report.Bytes = len(instructions)
	report.Present = instructions == prompt
	return report
}

func printCodexInstructionsReport(stdout io.Writer, report codexInstructionsReport) int {
	switch {
	case report.Err != nil:
		fmt.Fprintf(
			stdout,
			"doctor: codex developer_instructions=CHECK FAILED file=%s error=%v — the config could not be read, "+
				"so whether it carries the fleet prompt is UNKNOWN, not clean\n",
			report.Path,
			report.Err,
		)
	case report.Present:
		fmt.Fprintf(
			stdout,
			"doctor: codex developer_instructions=ok file=%s bytes=%d\n",
			report.Path,
			report.Bytes,
		)
		return 0
	case report.Bytes == 0:
		fmt.Fprintf(
			stdout,
			"doctor: codex developer_instructions=MISSING file=%s want=%d bytes — every Codex session from this "+
				"account starts with no fleet prompt; run pfm install --yes\n",
			report.Path,
			report.Want,
		)
	default:
		fmt.Fprintf(
			stdout,
			"doctor: codex developer_instructions=MISMATCH file=%s bytes=%d want=%d — the config carries a "+
				"developer_instructions that is not this binary's composed Codex prompt; run pfm install --yes, "+
				"and if the value is hand-written, pfm preserved it and installed nothing\n",
			report.Path,
			report.Bytes,
			report.Want,
		)
	}
	return 1
}
