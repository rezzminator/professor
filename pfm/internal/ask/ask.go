// Package ask defines the content-agnostic process contract shared by
// prepared-source callers. Process lifecycle is owned by headless/run; this
// package remains the compatibility adapter that renders the evidence prompt
// and takes usage from the parsed engine output.
package ask

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	headlessrun "github.com/rezzminator/professor/pfm/internal/headless/run"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

const engineTimeout = 60 * time.Second

type AskInput struct {
	ContentFiles []string
	SourceLabels []string
	Prompt       string
	Engine       pfmengine.ID
	Model        string
	Effort       string
	Timeout      time.Duration
}

type SourceSpan struct {
	Kind       string
	Start, End int
}
type Evidence struct {
	File, Label string
	Span        SourceSpan
	Quote       string
}
type FileStatus struct {
	File, Status, Note string
}

type TokenUsage = headlessrun.TokenUsage

type AskResult struct {
	Answer   string
	Evidence []Evidence
	PerFile  []FileStatus
	Usage    *TokenUsage
	Duration time.Duration
}

type Engine interface {
	Run(context.Context, AskInput) (AskResult, error)
}

// HarnessPrompt is the fixed instruction contract passed to either engine.
// Keep the wording byte-stable.
const HarnessPrompt = `Read the prepared content files listed below. Work ONLY from them; no network access, no other files.
{numbered file list: "N. <file path> — source: <source label>"}
TASK: {user prompt}
Rules: if a file is truncated or unusable, say so explicitly for that file instead of guessing.
After your answer, append a section titled exactly "EVIDENCE" listing one line per load-bearing claim:
  [file N] <location: line range, turn number, or chunk id> — "<short verbatim quote>"`

func ResolveInput(input AskInput, machine pfmconfig.Config) (AskInput, error) {
	if len(input.ContentFiles) == 0 {
		return AskInput{}, fmt.Errorf("content files must not be empty")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return AskInput{}, fmt.Errorf("prompt must not be empty")
	}
	for index, file := range input.ContentFiles {
		if strings.TrimSpace(file) == "" {
			return AskInput{}, fmt.Errorf("content file %d is empty", index+1)
		}
	}
	if len(input.SourceLabels) != 0 && len(input.SourceLabels) != len(input.ContentFiles) {
		return AskInput{}, fmt.Errorf(
			"source labels length %d does not match content files length %d",
			len(input.SourceLabels),
			len(input.ContentFiles),
		)
	}
	resolved := input
	if resolved.Engine == "" {
		var err error
		resolved.Engine, err = machine.DefaultEngine()
		if err != nil {
			return AskInput{}, err
		}
	}
	prefs := machine.Ask.PrefsFor(resolved.Engine)
	if resolved.Model == "" {
		resolved.Model = prefs.Model
	}
	if resolved.Effort == "" {
		resolved.Effort = prefs.Effort
	}
	if len(resolved.SourceLabels) == 0 {
		resolved.SourceLabels = append([]string(nil), resolved.ContentFiles...)
	}
	return resolved, nil
}

func BuildPrompt(input AskInput) (string, error) {
	if len(input.ContentFiles) == 0 {
		return "", fmt.Errorf("content files must not be empty")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return "", fmt.Errorf("prompt must not be empty")
	}
	if len(input.SourceLabels) != len(input.ContentFiles) {
		return "", fmt.Errorf(
			"source labels length %d does not match content files length %d",
			len(input.SourceLabels),
			len(input.ContentFiles),
		)
	}
	var builder strings.Builder
	builder.WriteString(
		"Read the prepared content files listed below. Work ONLY from them; no network access, no other files.\n",
	)
	for index, file := range input.ContentFiles {
		fmt.Fprintf(&builder, "%d. %s — source: %s\n", index+1, file, input.SourceLabels[index])
	}
	fmt.Fprintf(&builder, "TASK: %s\n", input.Prompt)
	builder.WriteString(
		"Rules: if a file is truncated or unusable, say so explicitly for that file instead of guessing.\n",
	)
	builder.WriteString(
		"After your answer, append a section titled exactly \"EVIDENCE\" listing one line per load-bearing claim:\n",
	)
	builder.WriteString("  [file N] <location: line range, turn number, or chunk id> — \"<short verbatim quote>\"")
	return builder.String(), nil
}

// BinaryMissingError is retained as an alias for callers of the ask package.
type BinaryMissingError = headlessrun.BinaryMissingError

func ResolveEngine(id pfmengine.ID, machine pfmconfig.Config) (Engine, error) {
	runner, err := RunnerFor(id)
	if err != nil {
		return nil, err
	}
	return runner.Resolve(machine)
}

func resolveProcess(id pfmengine.ID, machine pfmconfig.Config) (Engine, error) {
	if id == pfmengine.Claude && len(machine.Accounts) == 0 {
		return nil, fmt.Errorf("no Claude accounts configured")
	}
	if id == pfmengine.Codex && len(machine.CodexAccounts) == 0 {
		return nil, fmt.Errorf("no Codex accounts configured")
	}
	if _, err := headlessrun.Resolve(headlessrun.Request{Config: machine, Engine: id}); err != nil {
		return nil, err
	}
	return processEngine{machine: machine, engine: id}, nil
}

func ResolveClaude(machine pfmconfig.Config) (Engine, error) {
	return resolveProcess(pfmengine.Claude, machine)
}

func ResolveCodex(machine pfmconfig.Config) (Engine, error) {
	return resolveProcess(pfmengine.Codex, machine)
}

type processEngine struct {
	machine pfmconfig.Config
	engine  pfmengine.ID
}

func (engine processEngine) Run(parent context.Context, input AskInput) (AskResult, error) {
	prompt, err := BuildPrompt(input)
	if err != nil {
		return AskResult{}, err
	}
	timeout := input.Timeout
	if timeout == 0 {
		timeout = engineTimeout
	}
	request := headlessrun.Request{
		Config: engine.machine, Engine: engine.engine, Model: input.Model, Effort: input.Effort,
		Prompt: prompt, Timeout: timeout, NoSessionPersistence: engine.engine == pfmengine.Codex,
	}
	result, runErr := headlessrun.Run(parent, request)
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) {
			return AskResult{}, fmt.Errorf(
				"%s ask timed out: %w",
				pfmengine.MustLookup(engine.engine).LongName,
				context.DeadlineExceeded,
			)
		}
		if parent.Err() != nil {
			return AskResult{}, fmt.Errorf(
				"%s ask canceled: %w",
				pfmengine.MustLookup(engine.engine).LongName,
				parent.Err(),
			)
		}
		return AskResult{}, fmt.Errorf("%s ask failed: %w", pfmengine.MustLookup(engine.engine).LongName, runErr)
	}
	answer, usage := result.Answer, result.Usage
	if answer == "" {
		return AskResult{}, fmt.Errorf("%s ask returned an empty answer", pfmengine.MustLookup(engine.engine).LongName)
	}
	return AskResult{Answer: answer, Usage: usage, Duration: result.Duration}, nil
}

// claudeModelAliases are the model aliases Claude Code's --model takes for
// the latest model of a family (its --help names fable, opus and sonnet);
// claudeLongContext is the suffix it takes on an alias for the 1M-token
// context window (sonnet[1m]).
var claudeModelAliases = []string{"fable", "opus", "sonnet", "haiku"}

const claudeLongContext = "[1m]"

// CheckModel refuses, by name, a model the engine id cannot run: Claude takes
// one of its aliases (with or without the [1m] suffix) or a model id the
// price table lists as a claude row; any other engine a model id the table
// lists as its own. table is the machine's served price table
// (pricing.LoadPrices), the repo's one model registry. It answers the model
// to launch: trimmed, and an alias in the lower case Claude Code spells it.
func CheckModel(id pfmengine.ID, model string, table pricing.Table) (string, error) {
	descriptor, err := pfmengine.Lookup(id)
	if err != nil {
		return "", err
	}
	model = strings.TrimSpace(model)
	if alias := strings.ToLower(model); id == pfmengine.Claude &&
		slices.Contains(claudeModelAliases, strings.TrimSuffix(alias, claudeLongContext)) {
		return alias, nil
	}
	if row, ok := table.Resolve(model); ok && row.Engine == descriptor.LongName {
		return model, nil
	}
	if id == pfmengine.Claude {
		return "", fmt.Errorf("model %q is not a claude model: name an alias (%s) or a model id the price table lists",
			model, strings.Join(claudeModelAliases, ", "))
	}
	return "", fmt.Errorf(
		"model %q is not a %s model: name a model id the price table lists",
		model,
		descriptor.LongName,
	)
}
