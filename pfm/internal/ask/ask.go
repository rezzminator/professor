// Package ask defines the content-agnostic process contract shared by
// prepared-source callers. Process lifecycle is owned by headless/run; this
// package remains the compatibility adapter that renders the evidence prompt
// and extracts the older usage-line shape.
package ask

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	headlessrun "github.com/rezzminator/professor/pfm/internal/headless/run"
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

type TokenUsage struct {
	Input, CachedInput, Output int
}

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
	args := []string{"--output-format", "text"}
	if engine.engine == pfmengine.Codex {
		args = []string{"--ephemeral", "--skip-git-repo-check", "--color", "never", "-"}
	}
	timeout := input.Timeout
	if timeout == 0 {
		timeout = engineTimeout
	}
	request := headlessrun.Request{
		Config: engine.machine, Engine: engine.engine, Model: input.Model, Effort: input.Effort,
		Prompt: prompt, Timeout: timeout, Native: true, Args: args,
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
	answer, usage := extractUsage(result.Stdout, result.Stderr)
	if answer == "" {
		return AskResult{}, fmt.Errorf("%s ask returned an empty answer", pfmengine.MustLookup(engine.engine).LongName)
	}
	return AskResult{Answer: answer, Usage: usage, Duration: result.Duration}, nil
}

var usageField = regexp.MustCompile(`(?i)\b(cached_input_tokens|input_tokens|output_tokens)\b\s*[:=]\s*([0-9]+)`)

func extractUsage(stdout, stderr string) (string, *TokenUsage) {
	var usage *TokenUsage
	kept := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if parsed, ok := parseUsage(line); ok {
			usage = mergeUsage(usage, parsed)
			continue
		}
		kept = append(kept, line)
	}
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if parsed, ok := parseUsage(line); ok {
			usage = mergeUsage(usage, parsed)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), usage
}

func parseUsage(line string) (TokenUsage, bool) {
	normalized := strings.NewReplacer(`"`, "", `'`, "").Replace(line)
	matches := usageField.FindAllStringSubmatch(normalized, -1)
	if len(matches) == 0 {
		return TokenUsage{}, false
	}
	var usage TokenUsage
	for _, match := range matches {
		value, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		switch strings.ToLower(match[1]) {
		case "input_tokens":
			usage.Input = value
		case "cached_input_tokens":
			usage.CachedInput = value
		case "output_tokens":
			usage.Output = value
		}
	}
	return usage, true
}

func mergeUsage(current *TokenUsage, next TokenUsage) *TokenUsage {
	if current == nil {
		current = &TokenUsage{}
	}
	if next.Input != 0 {
		current.Input = next.Input
	}
	if next.CachedInput != 0 {
		current.CachedInput = next.CachedInput
	}
	if next.Output != 0 {
		current.Output = next.Output
	}
	return current
}
