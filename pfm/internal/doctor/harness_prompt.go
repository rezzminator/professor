package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	config "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	headlessrun "github.com/rezzminator/professor/pfm/internal/headless/run"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

var harnessCaptureSinkGrace = 2 * time.Second

// harnessRemoveAll removes the capture's throwaway config dir; a test swaps it
// to provoke the removal failure a root-run fence cannot produce by mode bits.
var harnessRemoveAll = os.RemoveAll

// harnessCaptureOverride is nil in production; printHarnessPromptDoctor then
// runs the real capture below. A jail has no genuine `claude` binary to spawn
// — that is REAL-SESSION territory (TESTPLAN.md), never jailable — so the
// command-package TestMain supplies a deterministic stub here, the same
// pattern as dependencyProbeOverride and installer.HookProbeOverride. Only the CAPTURE
// step is ever swapped; the baseline read and the verdict comparison stay
// real, so a test still exercises the actual match/DRIFT/CHECK-FAILED logic.
type HarnessCapture struct {
	Prompt        string
	ResolvedModel string
	CLIVersion    string
}

var HarnessCaptureOverride func(context.Context, string, config.Config, string, string) (HarnessCapture, error)

// errClaudeAbsent marks a capture failure as "no Claude Code binary
// installed" — installer.ClaudeAbsent's verdict — so the doctor row can
// skip it rather than counting a warning it never earned.
var errClaudeAbsent = errors.New("no Claude Code binary installed")

// errHarnessBypassedSink marks a capture where the CLI produced a real,
// priced answer (stop_reason and usage both present) without ever reaching
// the local capture sink — OAuth/subscription routing that ignores
// ANTHROPIC_BASE_URL (issue #24 finding 6). Distinct from errClaudeAbsent and
// from a bare timeout: the doctor row names it CANNOT CAPTURE, not CHECK
// FAILED, because a real request answered and may have been billed.
var errHarnessBypassedSink = errors.New("the CLI answered from the real endpoint and ignored ANTHROPIC_BASE_URL")

func configuredHarnessCaptureWithDeps(
	ctx context.Context,
	home string,
	machine config.Config,
	model, verboseDir string,
	dependencies Dependencies,
) (HarnessCapture, error) {
	if HarnessCaptureOverride != nil {
		return HarnessCaptureOverride(ctx, home, machine, model, verboseDir)
	}
	return captureHarnessPromptWithDeps(ctx, home, machine, model, verboseDir, dependencies)
}

// harnessBuildStamp is the CLI build stamp inside the billing-header system
// block. Every Claude Code release changes it, so both the live capture and
// the stored baseline are masked before hashing — DRIFT means prose drift.
var harnessBuildStamp = regexp.MustCompile(`cc_version=[^; ]*;`)

// Only complete, known metadata lines are excluded. Trailing instructions and
// metadata-like prose elsewhere remain visible to the comparison. Code fences
// never establish metadata sections or carry removable metadata.
var (
	harnessModelIdentity = regexp.MustCompile(
		`^ - You are powered by the model named [A-Za-z0-9 _-]+(?:\.[0-9]+[A-Za-z0-9 _-]*)*\. The exact model ID is [A-Za-z0-9._:-]+\.$`,
	)
	harnessKnowledgeCutoff = regexp.MustCompile(`^ - Assistant knowledge cutoff is [A-Za-z]+ \d{4}\.$`)
	// harnessModelCatalog is the whole model-catalog line: every Claude release
	// adds or drops an entry, so the line is masked to one placeholder — its
	// trailing "default to the latest models" sentence included, by ruling.
	// The line vanishing still changes the hash.
	harnessModelCatalog = regexp.MustCompile(`^ - The most recent Claude models are .*Model IDs — .*$`)
)

// Model and release tokens change with every Claude catalog update without
// changing an instruction, so normalizeHarnessPrompt masks them on every kept
// line, fenced lines included, in this order: a model ID (`claude-<family>`
// then one or more `-<digits>` groups, a date suffix being one more group —
// `claude-code` has none and stays), a display name (`Opus 5.5`), then a
// dotted version (`2.1.280`, `2.1.280-beta.1`) — only one that follows the
// word "version" (any case, an optional `v`) or is a `cc_version=` value. Any
// other dotted number (`127.0.0.1`, a schema number) is instruction text.
var (
	harnessModelID       = regexp.MustCompile(`\bclaude-[a-z]+(?:-\d+)+`)
	harnessModelName     = regexp.MustCompile(`\b(?:Claude|Opus|Sonnet|Haiku|Fable)\s+\d+(?:\.\d+)*`)
	harnessDottedVersion = regexp.MustCompile(
		`((?i:\bversion\s+v?)|\bcc_version=)\d+\.\d+\.\d+(?:[.-][A-Za-z0-9]+)*`,
	)
)

// harnessFence tracks Markdown code fences line by line, so a `#` or metadata
// line inside a fence is never read as a heading or as removable metadata.
type harnessFence struct {
	char  byte
	width int
}

// advance consumes one line and reports whether it belongs to a fence — its
// opening, its body, or its closing line.
func (fence *harnessFence) advance(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	width := 0
	if len(line)-len(trimmed) <= 3 && trimmed != "" && (trimmed[0] == '`' || trimmed[0] == '~') {
		for width < len(trimmed) && trimmed[width] == trimmed[0] {
			width++
		}
	}
	if fence.width > 0 {
		if width >= fence.width && trimmed[0] == fence.char && strings.TrimSpace(trimmed[width:]) == "" {
			fence.width = 0
		}
		return true
	}
	if width >= 3 {
		fence.char = trimmed[0]
		fence.width = width
		return true
	}
	return false
}

// harnessPromptHeading reports whether a non-fenced line opens a section.
func harnessPromptHeading(line string) bool {
	return strings.HasPrefix(line, "#") || line == "=== SYSTEM BLOCK ==="
}

// normalizeHarnessPrompt is the single canonical form of a harness prompt. It
// excludes CLI identity metadata — Claude may omit these two Environment lines
// even with dynamic sections excluded — and masks model IDs, model display
// names and dotted versions everywhere, and replaces the whole model-catalog
// line with one placeholder, so a catalog or release change alone is never
// drift. Instructions in that section, and matching text elsewhere,
// remain part of the drift hash.
func normalizeHarnessPrompt(prompt string) string {
	lines := strings.Split(prompt, "\n")
	kept := lines[:0]
	inEnvironment := false
	var fence harnessFence
	for index, line := range lines {
		if !fence.advance(line) {
			if index == 0 && len(lines) > 2 && lines[1] == "" && lines[2] == "=== SYSTEM BLOCK ===" &&
				strings.HasPrefix(line, "x-anthropic-billing-header: ") {
				line = harnessBuildStamp.ReplaceAllLiteralString(line, "cc_version=*;")
			}
			if harnessPromptHeading(line) {
				inEnvironment = line == "# Environment"
			}
			if inEnvironment && (harnessModelIdentity.MatchString(line) || harnessKnowledgeCutoff.MatchString(line)) {
				continue
			}
			if harnessModelCatalog.MatchString(line) {
				kept = append(kept, " - <model-catalog>")
				continue
			}
		}
		line = harnessModelID.ReplaceAllLiteralString(line, "<model-id>")
		line = harnessModelName.ReplaceAllLiteralString(line, "<model-name>")
		kept = append(kept, harnessDottedVersion.ReplaceAllString(line, "${1}<version>"))
	}
	return strings.Join(kept, "\n")
}

// harnessPromptVerdict is the pure comparator: baseline hash + name, the
// captured prompt, and the capture error map to exactly one doctor line.
func harnessPromptVerdict(baselineSHA, baselineName, captured string, captureErr error) (string, bool) {
	residue, captureErr := splitHarnessScratchResidue(captureErr)
	line, warn := harnessCaptureVerdict(baselineSHA, baselineName, captured, captureErr)
	if residue != nil {
		return line + "\ndoctor: " + residue.Error(), true
	}
	return line, warn
}

// harnessScratchResidue is a capture's throwaway config dir that os.RemoveAll
// could not remove. It travels in the capture's error, but it is no capture
// failure: the verdict still reads the capture and adds a line naming the dir.
type harnessScratchResidue struct {
	path string
	err  error
}

func (residue *harnessScratchResidue) Error() string {
	return fmt.Sprintf("harness-prompt scratch dir %s not removed: %v", residue.path, residue.err)
}

// splitHarnessScratchResidue separates a scratch residue from the capture's
// own error: the residue alone leaves a nil capture error.
func splitHarnessScratchResidue(err error) (*harnessScratchResidue, error) {
	var residue *harnessScratchResidue
	if !errors.As(err, &residue) {
		return nil, err
	}
	if err == error(residue) {
		return residue, nil
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return residue, err
	}
	var rest []error
	for _, part := range joined.Unwrap() {
		if part != error(residue) {
			rest = append(rest, part)
		}
	}
	return residue, errors.Join(rest...)
}

// harnessCaptureVerdict is harnessPromptVerdict without the scratch residue.
func harnessCaptureVerdict(baselineSHA, baselineName, captured string, captureErr error) (string, bool) {
	if errors.Is(captureErr, errClaudeAbsent) {
		return "doctor: harness-prompt: skipped (no Claude Code binary installed) — nothing to compare", false
	}
	if errors.Is(captureErr, errHarnessBypassedSink) {
		return fmt.Sprintf("doctor: harness-prompt: CANNOT CAPTURE (%v) — drift unknown", captureErr), true
	}
	if captureErr != nil {
		return fmt.Sprintf("doctor: harness-prompt: CHECK FAILED to run (%v) — drift unknown", captureErr), true
	}
	sum := sha256.Sum256([]byte(normalizeHarnessPrompt(captured)))
	live := hex.EncodeToString(sum[:])
	if live == baselineSHA {
		return "doctor: harness-prompt: matches baseline " + baselineName, false
	}
	return fmt.Sprintf(
		"doctor: harness-prompt: DRIFT live=%s baseline=%s (%s) — harness instructions changed; review before re-pinning",
		live[:16],
		baselineSHA[:16],
		baselineName,
	), true
}

// harnessPromptDetailLimit caps the section lines one DRIFT verdict prints.
const harnessPromptDetailLimit = 20

type harnessPromptSection struct {
	key  string
	body string
}

// harnessPromptSections splits a canonical prompt at every non-fenced heading
// line. The heading is the key; text before the first heading is
// "(preamble)"; a repeated heading is keyed "<heading> (2)", "(3)", in order.
func harnessPromptSections(canonical string) []harnessPromptSection {
	var sections []harnessPromptSection
	seen := map[string]int{}
	var body []string
	key, open := "(preamble)", false
	flush := func() {
		if open {
			// The prompt's final newline belongs to whichever section is last; it
			// is never an instruction, so trailing blank lines do not count.
			sections = append(
				sections,
				harnessPromptSection{key: key, body: strings.TrimRight(strings.Join(body, "\n"), "\n")},
			)
		}
	}
	var fence harnessFence
	for _, line := range strings.Split(canonical, "\n") {
		if !fence.advance(line) && harnessPromptHeading(line) {
			flush()
			seen[line]++
			key, open, body = line, true, nil
			if seen[line] > 1 {
				key = fmt.Sprintf("%s (%d)", line, seen[line])
			}
			continue
		}
		open = true
		body = append(body, line)
	}
	flush()
	return sections
}

// harnessPromptDetail explains a verdict: a model line when the resolved model
// differs from the baseline's, then — on DRIFT only — each removed and changed
// section in baseline order and each added section in live order (or, when no
// section text differs, the reorder or blank-line change), capped at
// harnessPromptDetailLimit. A failed capture explains nothing: its verdict
// already says drift is unknown.
func harnessPromptDetail(
	alias, baselineModel string,
	baseline string,
	captured HarnessCapture,
	captureErr error,
	drift bool,
) []string {
	if _, captureErr = splitHarnessScratchResidue(captureErr); captureErr != nil {
		return nil
	}
	var detail []string
	if captured.ResolvedModel != "" && captured.ResolvedModel != baselineModel {
		detail = append(detail, fmt.Sprintf(
			"doctor: harness-prompt:   model changed %s: %s → %s",
			alias,
			baselineModel,
			captured.ResolvedModel,
		))
	}
	if !drift {
		return detail
	}
	baselineSections := harnessPromptSections(normalizeHarnessPrompt(baseline))
	liveSections := harnessPromptSections(normalizeHarnessPrompt(captured.Prompt))
	live := make(map[string]string, len(liveSections))
	for _, section := range liveSections {
		live[section.key] = section.body
	}
	inBaseline := make(map[string]bool, len(baselineSections))
	var sections []string
	for _, section := range baselineSections {
		inBaseline[section.key] = true
		body, present := live[section.key]
		switch {
		case !present:
			sections = append(sections, "section removed: "+section.key)
		case body != section.body:
			sections = append(sections, "section changed: "+section.key)
		}
	}
	for _, section := range liveSections {
		if !inBaseline[section.key] {
			sections = append(sections, "section added: "+section.key)
		}
	}
	// Same keys, same bodies, different hash: the sections moved, or only the
	// blank lines trimmed from each body did. A DRIFT always names its cause.
	if len(sections) == 0 {
		sections = append(sections, "blank lines changed (no section text differs)")
		for index := range baselineSections {
			if baselineSections[index].key != liveSections[index].key {
				sections[0] = "section order changed"
				break
			}
		}
	}
	for index, section := range sections {
		if index == harnessPromptDetailLimit {
			detail = append(detail, fmt.Sprintf("doctor: harness-prompt:   … and %d more", len(sections)-index))
			break
		}
		detail = append(detail, "doctor: harness-prompt:   "+section)
	}
	return detail
}

// captureHarnessPrompt uses the shared headless runner with the unmodified
// harness prompt and a local sink that captures the request and returns 400.
// verboseDir, when non-empty, keeps the run's raw stdout/stderr and the sink
// hit count under it (change A) — a check that fails still leaves what it
// saw. The run launches under a throwaway CLAUDE_CONFIG_DIR, created here and
// removed before return (change B): the documented Keychain scoping means
// that directory is always logged out, so the dummy ANTHROPIC_API_KEY is the
// only credential the CLI can find.
func captureHarnessPrompt(
	ctx context.Context,
	home string,
	machine config.Config,
	model, verboseDir string,
) (HarnessCapture, error) {
	return captureHarnessPromptWithDeps(ctx, home, machine, model, verboseDir, normalizeDependencies(Dependencies{}))
}

func captureHarnessPromptWithDeps(
	ctx context.Context,
	home string,
	machine config.Config,
	model, verboseDir string,
	dependencies Dependencies,
) (capture HarnessCapture, captureErr error) {
	dependencies = normalizeDependencies(dependencies)
	listener, err := dependencies.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return HarnessCapture{}, fmt.Errorf("open capture sink: %w", err)
	}
	bodies := make(chan []byte, 1)
	hits := &harnessSinkHits{}
	server := &http.Server{Handler: hits.wrap(harnessSinkHandler(bodies))}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	resolvedPaths, pathErr := paths.Resolve()
	if pathErr != nil {
		return HarnessCapture{}, fmt.Errorf("resolve harness capture scratch directory: %w", pathErr)
	}
	if err := os.MkdirAll(resolvedPaths.SIDDir, 0o700); err != nil {
		return HarnessCapture{}, fmt.Errorf("create harness capture scratch base %s: %w", resolvedPaths.SIDDir, err)
	}
	configDir, err := os.MkdirTemp(resolvedPaths.SIDDir, paths.SIDHarnessConfigDirPrefix)
	if err != nil {
		return HarnessCapture{}, fmt.Errorf("create throwaway CLAUDE_CONFIG_DIR: %w", err)
	}
	defer func() {
		removeErr := harnessRemoveAll(configDir)
		if removeErr == nil {
			return
		}
		residue := &harnessScratchResidue{path: configDir, err: removeErr}
		if captureErr == nil {
			captureErr = residue
			return
		}
		captureErr = errors.Join(captureErr, residue)
	}()

	binary := machine.Claude.Binary
	if binary == "" {
		binary = pfmengine.MustLookup(pfmengine.Claude).Binary
	}
	versionCtx, versionCancel := context.WithTimeout(ctx, 5*time.Second)
	versionResult, versionErr := dependencies.Runner.Run(versionCtx, []string{binary, "--version"}, deps.RunOptions{
		Env:       harnessCaptureEnv(os.Environ(), "http://"+listener.Addr().String(), configDir),
		WaitDelay: 500 * time.Millisecond,
	})
	versionCancel()
	versionExit := versionResult.ExitCode
	if versionErr != nil {
		versionExit = -1
	}
	if versionErr != nil || versionResult.ExitCode != 0 {
		resolved := binary
		if !filepath.IsAbs(resolved) {
			if looked, lookErr := dependencies.Runner.LookPath(binary); lookErr == nil {
				resolved = looked
			}
		}
		if installer.ClaudeAbsent(home, resolved, versionExit) {
			return HarnessCapture{}, errClaudeAbsent
		}
		if versionErr != nil {
			return HarnessCapture{}, fmt.Errorf("read Claude CLI version: %w", versionErr)
		}
		return HarnessCapture{}, fmt.Errorf(
			"read Claude CLI version: command exited %d: %s",
			versionExit,
			strings.TrimSpace(string(versionResult.Stderr)),
		)
	}
	version := strings.TrimSpace(string(versionResult.Stdout))
	devNull, stdinErr := os.Open(os.DevNull)
	if stdinErr != nil {
		return HarnessCapture{}, fmt.Errorf("open %s for the capture run's stdin: %w", os.DevNull, stdinErr)
	}
	defer func() { _ = devNull.Close() }()
	result, runErr := headlessrun.Run(ctx, headlessrun.Request{
		Config: config.Config{Claude: config.Claude{Binary: binary}},
		Engine: pfmengine.Claude, Model: model, Native: true, WithoutAccount: true,
		Timeout: 20 * time.Second,
		// "x" travels as the CLI's own positional prompt argument, never on
		// stdin — matching the documented `claude -p x ...` invocation
		// exactly. Stdin is pinned to /dev/null so the CLI never waits on a
		// stream that carries nothing (issue #24 finding 6 observed a "no
		// stdin data received in 3s" warning when stdin was left ambiguous).
		Args: []string{
			"x", "--output-format", "json", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
			"--max-turns", "1", "--exclude-dynamic-system-prompt-sections",
		},
		Env:    harnessCaptureEnv(os.Environ(), "http://"+listener.Addr().String(), configDir),
		Stdin:  devNull,
		Runner: dependencies.Runner,
	})
	if verboseDir != "" {
		if writeErr := deps.WriteVerboseFile(
			verboseDir,
			"harness-prompt-"+model+".stdout",
			[]byte(result.Stdout),
		); writeErr != nil {
			return HarnessCapture{}, fmt.Errorf("write harness capture stdout evidence: %w", writeErr)
		}
		if writeErr := deps.WriteVerboseFile(
			verboseDir,
			"harness-prompt-"+model+".stderr",
			[]byte(result.Stderr),
		); writeErr != nil {
			return HarnessCapture{}, fmt.Errorf("write harness capture stderr evidence: %w", writeErr)
		}
	}
	// The CLI exits nonzero by design — the sink refused its request; the
	// capture, not the exit code, is the result. The grace window covers the
	// handler goroutine still finishing its send after Run returns; a ctx case
	// is deliberately absent — a ready body racing an expired ctx in one
	// select would drop real captures at random.
	select {
	case body := <-bodies:
		captured, err := decodeHarnessCapture(body)
		captured.CLIVersion = version
		if verboseDir != "" {
			if hitsErr := writeHarnessSinkHits(verboseDir, model, hits); hitsErr != nil {
				return captured, errors.Join(err, fmt.Errorf("write harness sink hit evidence: %w", hitsErr))
			}
		}
		return captured, err
	case <-dependencies.Clock.After(harnessCaptureSinkGrace):
		if verboseDir != "" {
			if hitsErr := writeHarnessSinkHits(verboseDir, model, hits); hitsErr != nil {
				return HarnessCapture{}, fmt.Errorf("write harness sink hit evidence: %w", hitsErr)
			}
		}
		if hits.count() == 0 && runErr == nil && claudeAnsweredWithoutSink(result.Stdout) {
			return HarnessCapture{CLIVersion: version}, fmt.Errorf(
				"%w (OAuth-only routing on cli=%s) — one minimal request may have been billed",
				errHarnessBypassedSink,
				version,
			)
		}
		message := errors.New("no API request reached the capture sink")
		if verboseDir != "" {
			message = fmt.Errorf(
				"%w — see %s (--verbose)",
				message,
				filepath.Join(verboseDir, "harness-prompt-"+model+".stderr"),
			)
		}
		return HarnessCapture{CLIVersion: version}, errors.Join(message, runErr)
	}
}

// claudeAnsweredWithoutSink reports whether the CLI's own stdout is a
// complete, priced Claude Code envelope — stop_reason and usage both present
// — the shape a real /v1/messages exchange produces. A blocked or empty run
// never satisfies this, so it can never masquerade as a spend.
func claudeAnsweredWithoutSink(stdout string) bool {
	var envelope struct {
		StopReason string          `json:"stop_reason"`
		Usage      json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		return false
	}
	return strings.TrimSpace(envelope.StopReason) != "" && len(envelope.Usage) > 0 && string(envelope.Usage) != "null"
}

// harnessSinkHits records every request the sink handler ever saw — not just
// the first /messages body harnessSinkHandler forwards — so a failed capture
// can still report what, if anything, reached the sink (change A).
type harnessSinkHits struct {
	mu    sync.Mutex
	paths []string
}

func (hits *harnessSinkHits) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		hits.mu.Lock()
		hits.paths = append(hits.paths, request.Method+" "+request.URL.Path)
		hits.mu.Unlock()
		next(writer, request)
	}
}

func (hits *harnessSinkHits) count() int {
	hits.mu.Lock()
	defer hits.mu.Unlock()
	return len(hits.paths)
}

func writeHarnessSinkHits(verboseDir, model string, hits *harnessSinkHits) error {
	hits.mu.Lock()
	lines := append([]string(nil), hits.paths...)
	hits.mu.Unlock()
	content := fmt.Sprintf("TOTAL_HITS %d\n", len(lines))
	for _, line := range lines {
		content += line + "\n"
	}
	return deps.WriteVerboseFile(verboseDir, "sink-hits-"+model+".txt", []byte(content))
}

// harnessSinkHandler refuses every request with the non-retryable 400 and
// forwards the FIRST messages-call body only — the CLI can post telemetry or
// preflight calls to the base URL before the real /v1/messages request, and
// an empty or non-messages body must not win the capture.
func harnessSinkHandler(bodies chan<- []byte) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if strings.HasSuffix(request.URL.Path, "/messages") {
			select {
			case bodies <- body:
			default:
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write(
			[]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"captured by pfm doctor"}}`),
		)
	}
}

// harnessCaptureEnv is the fleet hygiene strip applied in-process: every name
// in action's one list (session identity, endpoint, cache and traffic
// overrides) plus ANTHROPIC_API_KEY is dropped, then the sink
// endpoint, dummy credentials, the throwaway config dir, and the full-prompt
// arm are pinned. configDir is created fresh per capture by the caller
// (change B) — CLAUDE_CONFIG_DIR is stripped first so the inherited value
// never leaks through even if this pin were ever omitted.
func harnessCaptureEnv(environ []string, sinkURL, configDir string) []string {
	stripped := map[string]bool{"ANTHROPIC_API_KEY": true}
	for _, name := range action.HygieneNames() {
		stripped[name] = true
	}
	result := make([]string, 0, len(environ)+6)
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if stripped[name] {
			continue
		}
		result = append(result, entry)
	}
	return append(result,
		"ANTHROPIC_BASE_URL="+sinkURL,
		"ANTHROPIC_API_KEY=pfm-doctor-sink",
		"ANTHROPIC_AUTH_TOKEN=pfm-doctor-sink",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=0",
		"FORCE_PROMPT_CACHING_5M=1",
		"CLAUDE_CONFIG_DIR="+configDir,
	)
}

// joinSystemBlocks renders a captured request's system prompt exactly the way
// the baseline file was produced (jq: `.system | map(.text) |
// join("\n\n=== SYSTEM BLOCK ===\n\n")` with jq -r's trailing newline) — the
// hashes only ever match if this stays byte-compatible.
func joinSystemBlocks(body []byte) (string, error) {
	var payload struct {
		System json.RawMessage `json:"system"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parse captured request: %w", err)
	}
	if len(payload.System) == 0 {
		return "", errors.New("captured request carries no system prompt")
	}
	var plain string
	if err := json.Unmarshal(payload.System, &plain); err == nil {
		if strings.TrimSpace(plain) == "" {
			return "", errors.New("captured system prompt is empty")
		}
		return plain + "\n", nil
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload.System, &blocks); err != nil {
		return "", fmt.Errorf("parse system blocks: %w", err)
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		texts = append(texts, block.Text)
	}
	if strings.TrimSpace(strings.Join(texts, "")) == "" {
		return "", errors.New("captured system prompt is empty")
	}
	return strings.Join(texts, "\n\n=== SYSTEM BLOCK ===\n\n") + "\n", nil
}

func decodeHarnessCapture(body []byte) (HarnessCapture, error) {
	var request struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return HarnessCapture{}, fmt.Errorf("parse captured request: %w", err)
	}
	if strings.TrimSpace(request.Model) == "" {
		return HarnessCapture{}, errors.New("captured request carries no resolved model")
	}
	prompt, err := joinSystemBlocks(body)
	return HarnessCapture{Prompt: prompt, ResolvedModel: request.Model}, err
}
