package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	config "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	headlessrun "hostops/pfm/internal/headless/run"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/paths"
)

var harnessCaptureSinkGrace = 2 * time.Second

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

func configuredHarnessCapture(
	ctx context.Context,
	home string,
	machine config.Config,
	model, verboseDir string,
) (HarnessCapture, error) {
	if HarnessCaptureOverride != nil {
		return HarnessCaptureOverride(ctx, home, machine, model, verboseDir)
	}
	return captureHarnessPrompt(ctx, home, machine, model, verboseDir)
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
)

// normalizeHarnessPrompt excludes only CLI identity metadata. Claude may omit
// these two Environment lines even with dynamic sections excluded. Instructions
// in that section, and matching text elsewhere, remain part of the drift hash.
func normalizeHarnessPrompt(prompt string) string {
	lines := strings.Split(prompt, "\n")
	kept := lines[:0]
	inEnvironment := false
	var fence byte
	fenceWidth := 0
	for index, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		width := 0
		if len(line)-len(trimmed) <= 3 && trimmed != "" && (trimmed[0] == '`' || trimmed[0] == '~') {
			for width < len(trimmed) && trimmed[width] == trimmed[0] {
				width++
			}
		}
		if fenceWidth > 0 {
			kept = append(kept, line)
			if width >= fenceWidth && trimmed[0] == fence && strings.TrimSpace(trimmed[width:]) == "" {
				fenceWidth = 0
			}
			continue
		}
		if width >= 3 {
			fence = trimmed[0]
			fenceWidth = width
			kept = append(kept, line)
			continue
		}
		if index == 0 && len(lines) > 2 && lines[1] == "" && lines[2] == "=== SYSTEM BLOCK ===" &&
			strings.HasPrefix(line, "x-anthropic-billing-header: ") {
			line = harnessBuildStamp.ReplaceAllLiteralString(line, "cc_version=*;")
		}
		if strings.HasPrefix(line, "#") || line == "=== SYSTEM BLOCK ===" {
			inEnvironment = line == "# Environment"
		}
		if inEnvironment && (harnessModelIdentity.MatchString(line) || harnessKnowledgeCutoff.MatchString(line)) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// harnessPromptVerdict is the pure comparator: baseline hash + name, the
// captured prompt, and the capture error map to exactly one doctor line.
func harnessPromptVerdict(baselineSHA, baselineName, captured string, captureErr error) (string, bool) {
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
	configDir, err := os.MkdirTemp(resolvedPaths.SIDDir, "pfm-harness-configdir-")
	if err != nil {
		return HarnessCapture{}, fmt.Errorf("create throwaway CLAUDE_CONFIG_DIR: %w", err)
	}
	defer func() { _ = os.RemoveAll(configDir) }()

	binary := machine.Claude.Binary
	if binary == "" {
		binary = pfmengine.MustLookup(pfmengine.Claude).Binary
	}
	versionCtx, versionCancel := context.WithTimeout(ctx, 5*time.Second)
	versionCmd := exec.CommandContext(versionCtx, binary, "--version")
	versionCmd.WaitDelay = 500 * time.Millisecond
	versionCmd.Env = harnessCaptureEnv(os.Environ(), "http://"+listener.Addr().String(), configDir)
	versionRaw, versionErr := versionCmd.Output()
	versionCancel()
	if versionErr != nil {
		resolved := binary
		if !filepath.IsAbs(resolved) {
			if looked, lookErr := exec.LookPath(binary); lookErr == nil {
				resolved = looked
			}
		}
		if installer.ClaudeAbsent(home, resolved, deps.ExitCode(versionErr)) {
			return HarnessCapture{}, errClaudeAbsent
		}
		return HarnessCapture{}, fmt.Errorf("read Claude CLI version: %w", versionErr)
	}
	version := strings.TrimSpace(string(versionRaw))
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
		Env:   harnessCaptureEnv(os.Environ(), "http://"+listener.Addr().String(), configDir),
		Stdin: devNull,
	})
	if verboseDir != "" {
		if writeErr := deps.WriteVerboseFile(
			verboseDir,
			"harness-prompt.stdout",
			[]byte(result.Stdout),
		); writeErr != nil {
			return HarnessCapture{}, fmt.Errorf("write harness capture stdout evidence: %w", writeErr)
		}
		if writeErr := deps.WriteVerboseFile(
			verboseDir,
			"harness-prompt.stderr",
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
			_ = writeHarnessSinkHits(verboseDir, hits)
		}
		return captured, err
	case <-time.After(harnessCaptureSinkGrace):
		if verboseDir != "" {
			_ = writeHarnessSinkHits(verboseDir, hits)
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
			message = fmt.Errorf("%w — see %s (--verbose)", message, filepath.Join(verboseDir, "harness-prompt.stderr"))
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

func writeHarnessSinkHits(verboseDir string, hits *harnessSinkHits) error {
	hits.mu.Lock()
	lines := append([]string(nil), hits.paths...)
	hits.mu.Unlock()
	content := fmt.Sprintf("TOTAL_HITS %d\n", len(lines))
	for _, line := range lines {
		content += line + "\n"
	}
	return deps.WriteVerboseFile(verboseDir, "sink-hits.txt", []byte(content))
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

// harnessCaptureEnv is the fleet hygiene strip applied in-process: inherited
// session identity, endpoint and cache overrides are dropped, then the sink
// endpoint, dummy credentials, the throwaway config dir, and the full-prompt
// arm are pinned. configDir is created fresh per capture by the caller
// (change B) — CLAUDE_CONFIG_DIR is stripped first so the inherited value
// never leaks through even if this pin were ever omitted.
func harnessCaptureEnv(environ []string, sinkURL, configDir string) []string {
	stripped := map[string]bool{
		"CLAUDE_CODE_SESSION_ID": true, "CLAUDECODE": true, "CLAUDE_CODE_CHILD_SESSION": true,
		"CLAUDE_CONFIG_DIR": true, "ENABLE_PROMPT_CACHING_1H": true, "FORCE_PROMPT_CACHING_5M": true,
		"ANTHROPIC_BASE_URL": true, "ANTHROPIC_AUTH_TOKEN": true, "ANTHROPIC_API_KEY": true,
		"ANTHROPIC_MODEL": true, "ANTHROPIC_SMALL_FAST_MODEL": true,
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": true, "CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT": true,
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
