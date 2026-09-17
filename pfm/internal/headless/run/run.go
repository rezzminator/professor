// Package run owns the one-shot process boundary for every supported headless
// engine. Callers may choose the normalized contract or ask the native client
// to pass through its output, but process lifetime and account identity always
// cross this package.
package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"hostops/pfm/internal/atomicfile"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

const (
	maxCapturedOutput = 8 << 20
	jsonNull          = "null"
)

// ErrStructuredOutput means that a requested schema was not satisfied by the
// engine envelope. It is deliberately separate from a process failure so CLI
// callers can give this validation failure its own exit status.
var ErrStructuredOutput = errors.New("structured output missing or invalid")

// BinaryMissingError distinguishes a configured engine that is absent from an
// engine binary that resolved but failed after launch.
type BinaryMissingError struct {
	Engine string
	Binary string
	Err    error
}

func (err *BinaryMissingError) Error() string {
	return fmt.Sprintf("%s binary MISSING (%s)", err.Engine, err.Binary)
}

func (err *BinaryMissingError) Unwrap() error { return err.Err }

// Request is the engine-neutral headless contract. Args are appended losslessly
// after translated common options, allowing a caller to use any native option
// the selected engine supports without teaching pfm every engine feature.
type Request struct {
	Config               pfmconfig.Config
	Engine               pfmengine.ID
	Account              int
	ConfigDir            string
	Model                string
	Effort               string
	Prompt               string
	CWD                  string
	TempDir              string
	Timeout              time.Duration
	SystemPrompt         *string
	Schema               json.RawMessage
	Tools                *string
	SettingsSources      *string
	StrictMCP            bool
	NoSessionPersistence bool
	Sealed               bool
	AllowUnsupported     bool
	Args                 []string
	Env                  []string
	// WithoutAccount is reserved for controlled diagnostic/native captures.
	// It requires a complete explicit Env and never derives identity from a
	// configured roster; ordinary callers must resolve a roster account.
	WithoutAccount     bool
	Stdin              io.Reader
	Stdout             io.Writer
	Stderr             io.Writer
	Native             bool
	systemPromptFile   string
	schemaFilePath     string
	binaryPath         string
	unsupportedOptions []string
}

// TokenUsage is every way an engine can bill one call. Claude's usage block splits the input
// three ways and CacheCreation is by far the largest of them the first time a big attachment is
// sent: dropping it reported ~2 input tokens for a 60 KB prompt, which is not a small error but a
// fiction. A receipt that cannot be trusted for size cannot be trusted for cost either.
type TokenUsage struct {
	Input         int `json:"input_tokens"`
	CachedInput   int `json:"cached_input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	Output        int `json:"output_tokens"`
}

type Result struct {
	Engine           pfmengine.ID    `json:"engine"`
	Model            string          `json:"model"`
	Effort           string          `json:"effort"`
	Answer           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
	Usage            *TokenUsage     `json:"usage,omitempty"`
	TotalCostUSD     *float64        `json:"total_cost_usd"`
	Duration         time.Duration   `json:"-"`
	ExitCode         int             `json:"exit_code"`
	IsError          bool            `json:"is_error"`
	TimedOut         bool            `json:"timeout"`
	Diagnostics      []string        `json:"diagnostics,omitempty"`
	Stdout           string          `json:"-"`
	Stderr           string          `json:"-"`
}

// Resolve applies engine, account, binary, model, effort, and filesystem
// defaults without launching a process. The returned private fields are kept
// out of the public API; Run resolves again so callers may use either seam.
func Resolve(request Request) (Request, error) {
	if request.Timeout < 0 {
		return Request{}, fmt.Errorf("timeout must be zero or positive")
	}
	if request.WithoutAccount {
		if request.Account != 0 || request.ConfigDir != "" || request.Env == nil {
			return Request{}, fmt.Errorf(
				"without-account headless runs require Account=0, no ConfigDir, and a complete Env",
			)
		}
	}
	if request.Engine == "" {
		var err error
		request.Engine, err = request.Config.DefaultEngine()
		if err != nil {
			return Request{}, err
		}
	}
	if _, err := pfmengine.Lookup(request.Engine); err != nil {
		return Request{}, err
	}
	if request.Engine == pfmengine.OpenCode {
		return Request{}, fmt.Errorf("OpenCode does not support headless runs")
	}

	binary, accounts := configuredEngineAccounts(request)
	rosterPresent := !request.WithoutAccount && (request.Account != 0 || len(accounts) > 0)
	if !request.WithoutAccount && request.Account == 0 && len(accounts) > 0 {
		request.Account = accountIDForConfigDir(accounts, request.ConfigDir)
		if request.Account == 0 {
			request.Account = accounts[0].id
		}
	}
	var rosterDir string
	if !request.WithoutAccount && rosterPresent {
		account, ok := configuredAccountByID(accounts, request.Account)
		if !ok {
			return Request{}, fmt.Errorf(
				"requested %s account %d is not in the configured roster",
				pfmengine.MustLookup(request.Engine).Short,
				request.Account,
			)
		}
		rosterDir = account.configDir
		if account.binary != "" {
			binary = account.binary
		}
	}
	if request.ConfigDir != "" && rosterDir != "" && filepath.Clean(request.ConfigDir) != filepath.Clean(rosterDir) {
		return Request{}, fmt.Errorf(
			"%s config dir %q does not match account %d roster dir %q",
			request.Engine,
			request.ConfigDir,
			request.Account,
			rosterDir,
		)
	}
	if request.ConfigDir == "" {
		request.ConfigDir = rosterDir
	}
	if request.WithoutAccount {
		name := pfmengine.MustLookup(request.Engine).HomeEnv
		for _, entry := range request.Env {
			if value, ok := strings.CutPrefix(entry, name+"="); ok {
				request.ConfigDir = value
			}
		}
	} else if !rosterPresent {
		return Request{}, fmt.Errorf("%s account roster is empty; configure an account", request.Engine)
	}
	binaryPath, err := deps.Resolve(binary)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Request{}, &BinaryMissingError{
				Engine: pfmengine.MustLookup(request.Engine).LongName,
				Binary: binary,
				Err:    err,
			}
		}
		return Request{}, fmt.Errorf("resolve %s binary %q: %w", request.Engine, binary, err)
	}
	request.binaryPath = binaryPath

	if !request.Native {
		prefs := request.Config.Ask.PrefsFor(request.Engine)
		if request.Model == "" {
			request.Model = prefs.Model
		}
		if request.Effort == "" {
			request.Effort = prefs.Effort
		}
	}
	if request.Sealed {
		if request.SystemPrompt == nil {
			return Request{}, fmt.Errorf("sealed headless run requires a replacement system prompt")
		}
		if request.TempDir == "" {
			resolved, err := paths.Resolve()
			if err != nil {
				return Request{}, fmt.Errorf("resolve headless scratch directory: %w", err)
			}
			request.TempDir = resolved.SIDDir
		}
		if request.CWD != "" {
			// The caller may provide a scratch base, but Sealed always owns the
			// actual cwd. It is created immediately before launch in Run.
			request.CWD = ""
		}
		empty := ""
		request.Tools = &empty
		request.SettingsSources = &empty
		request.StrictMCP = true
		request.NoSessionPersistence = true
		if len(request.Args) != 0 && (request.Engine != pfmengine.Codex || !request.AllowUnsupported) {
			return Request{}, fmt.Errorf("sealed headless run does not accept native args")
		}
	}
	if request.Engine == pfmengine.Codex {
		var unsupported []string
		if request.Sealed {
			unsupported = append(unsupported, "--sealed (full isolation)")
		}
		if request.Tools != nil {
			unsupported = append(unsupported, "--tools")
		}
		if request.SettingsSources != nil {
			unsupported = append(unsupported, "--setting-sources")
		}
		if request.StrictMCP {
			unsupported = append(unsupported, "--strict-mcp-config")
		}
		if len(unsupported) != 0 {
			if !request.AllowUnsupported {
				return Request{}, fmt.Errorf(
					"headless runs with Codex cannot guarantee tools or settings isolation: unsupported %s; use --allow-unsupported to continue without these controls",
					strings.Join(unsupported, ", "),
				)
			}
			request.Tools, request.SettingsSources, request.StrictMCP = nil, nil, false
			request.unsupportedOptions = unsupported
		}
	}
	if request.Schema != nil {
		if err := validateSchema(request.Schema); err != nil {
			return Request{}, err
		}
	}
	return request, nil
}

type configuredEngineAccount struct {
	id        int
	configDir string
	binary    string
}

func configuredEngineAccounts(request Request) (string, []configuredEngineAccount) {
	var binary string
	var accounts []configuredEngineAccount
	switch request.Engine {
	case pfmengine.Claude:
		binary = strings.TrimSpace(request.Config.Claude.Binary)
		accounts = make([]configuredEngineAccount, 0, len(request.Config.Accounts))
		for _, account := range request.Config.Accounts {
			accounts = append(accounts, configuredEngineAccount{
				id:        account.ID,
				configDir: account.ConfigDir,
				binary:    strings.TrimSpace(request.Config.EffectiveClaude(account.ID).Binary),
			})
		}
	case pfmengine.Codex:
		binary = strings.TrimSpace(request.Config.Codex.Binary)
		accounts = make([]configuredEngineAccount, 0, len(request.Config.CodexAccounts))
		for _, account := range request.Config.CodexAccounts {
			accounts = append(accounts, configuredEngineAccount{
				id:        account.ID,
				configDir: account.Home,
				binary:    strings.TrimSpace(request.Config.EffectiveCodex(account.ID).Binary),
			})
		}
	}
	if binary == "" {
		binary = pfmengine.MustLookup(request.Engine).Binary
	}
	return binary, accounts
}

func accountIDForConfigDir(accounts []configuredEngineAccount, configDir string) int {
	if configDir == "" {
		return 0
	}
	want := filepath.Clean(configDir)
	for _, account := range accounts {
		if filepath.Clean(account.configDir) == want {
			return account.id
		}
	}
	return 0
}

func configuredAccountByID(accounts []configuredEngineAccount, id int) (configuredEngineAccount, bool) {
	for _, account := range accounts {
		if account.id == id {
			return account, true
		}
	}
	return configuredEngineAccount{}, false
}

func ensureScratchBase(base string) (string, error) {
	if base == "" {
		resolved, resolveErr := paths.Resolve()
		if resolveErr != nil {
			return "", fmt.Errorf("resolve headless scratch directory: %w", resolveErr)
		}
		base = resolved.SIDDir
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("create headless scratch base %s: %w", base, err)
	}
	return base, nil
}

func writeHeadlessScratch(base, pattern string, data []byte) (path string, cleanup func(), err error) {
	base, err = ensureScratchBase(base)
	if err != nil {
		return "", nil, err
	}
	return atomicfile.WriteScratch(base, pattern, data)
}

func Run(parent context.Context, request Request) (result Result, runErr error) {
	defer func() {
		if runErr != nil {
			result.IsError = true
		}
	}()
	resolved, err := Resolve(request)
	if err != nil {
		return Result{Engine: request.Engine, ExitCode: -1}, err
	}
	request = resolved
	result.Engine, result.Model, result.Effort, result.ExitCode = request.Engine, request.Model, request.Effort, -1
	for _, option := range request.unsupportedOptions {
		result.Diagnostics = append(result.Diagnostics, "unsupported control not applied for Codex: "+option)
	}
	started := time.Now()
	ctx := parent
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, request.Timeout)
		defer cancel()
	}

	cwd := request.CWD
	cleanup := func() error { return nil }
	if request.Sealed {
		base, baseErr := ensureScratchBase(request.TempDir)
		if baseErr != nil {
			return result, baseErr
		}
		cwd, err = os.MkdirTemp(base, "pfm-headless-")
		if err != nil {
			return result, fmt.Errorf("create headless scratch directory in %s: %w", base, err)
		}
		cleanup = func() error { return os.RemoveAll(cwd) }
	}
	if cwd != "" {
		if info, statErr := os.Stat(cwd); statErr != nil || !info.IsDir() {
			if statErr != nil {
				return result, fmt.Errorf("headless cwd %s: %w", cwd, statErr)
			}
			return result, fmt.Errorf("headless cwd %s is not a directory", cwd)
		}
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			if runErr == nil {
				runErr = fmt.Errorf("cleanup headless scratch directory: %w", cleanupErr)
			} else {
				runErr = fmt.Errorf("%v; cleanup headless scratch directory: %w", runErr, cleanupErr)
			}
		}
	}()
	if request.SystemPrompt != nil && (request.Engine == pfmengine.Claude || request.Engine == pfmengine.Codex) {
		name, removePrompt, fileErr := writeHeadlessScratch(
			request.TempDir,
			"pfm-headless-system-*.txt",
			[]byte(*request.SystemPrompt),
		)
		if fileErr != nil {
			return result, fmt.Errorf("materialize headless system prompt: %w", fileErr)
		}
		request.systemPromptFile = name
		previousCleanup := cleanup
		cleanup = func() error {
			removePrompt()
			return previousCleanup()
		}
	}
	if request.Schema != nil && request.Engine == pfmengine.Codex {
		name, removeSchema, fileErr := writeHeadlessScratch(
			request.TempDir,
			"pfm-headless-schema-*.json",
			request.Schema,
		)
		if fileErr != nil {
			return result, fmt.Errorf("materialize headless output schema: %w", fileErr)
		}
		request.schemaFilePath = name
		previousCleanup := cleanup
		cleanup = func() error {
			removeSchema()
			return previousCleanup()
		}
	}

	argv, err := arguments(request)
	if err != nil {
		return result, err
	}
	command := exec.CommandContext(ctx, request.binaryPath, argv...)
	configureBoundedCommand(command)
	if cwd != "" {
		command.Dir = cwd
	}
	if request.Env == nil {
		command.Env = os.Environ()
	} else {
		command.Env = append([]string(nil), request.Env...)
	}
	setEnvironment(command.Env, request.Engine, request.ConfigDir, request.Env != nil, &command.Env)
	if request.Stdin != nil {
		command.Stdin = request.Stdin
	} else {
		command.Stdin = strings.NewReader(request.Prompt)
	}

	stdout := boundedBuffer{limited: !request.Native}
	stderr := boundedBuffer{limited: !request.Native}
	command.Stdout = writerFor(request.Stdout, &stdout)
	command.Stderr = writerFor(request.Stderr, &stderr)
	runErr = command.Run()
	result.Duration = time.Since(started)
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	result.ExitCode = processExitCode(runErr)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		result.IsError = true
		return result, fmt.Errorf("%s headless run timed out: %w", request.Engine, context.DeadlineExceeded)
	}
	if ctx.Err() != nil {
		result.IsError = true
		return result, fmt.Errorf("%s headless run canceled: %w", request.Engine, ctx.Err())
	}
	if request.Native {
		result.Answer = result.Stdout
		if runErr != nil {
			result.IsError = true
			return result, fmt.Errorf(
				"%s headless run failed: %w; stderr tail %q",
				request.Engine,
				runErr,
				boundedTail(result.Stderr, 1024),
			)
		}
		return result, nil
	}
	if stdout.truncated || stderr.truncated {
		return result, fmt.Errorf("normalized headless output exceeded the %d-byte capture limit", maxCapturedOutput)
	}
	if runErr != nil {
		result.IsError = true
		// An engine that exits non-zero with an empty stderr said what went wrong on stdout
		// (Claude's JSON envelope carries `is_error` + a result line); without both tails the
		// failure reads as absence — 60 s, exit 1, nothing.
		result.Diagnostics = append(result.Diagnostics, failureDiagnostics(result)...)
		return result, fmt.Errorf(
			"%s headless run failed: %w; stderr tail %q; stdout tail %q",
			request.Engine,
			runErr,
			boundedTail(result.Stderr, 1024),
			boundedTail(result.Stdout, 1024),
		)
	}
	if err := parseOutput(&result, request); err != nil {
		result.IsError = true
		return result, err
	}
	return result, nil
}

func arguments(request Request) ([]string, error) {
	args := []string{}
	switch request.Engine {
	case pfmengine.Claude:
		args = append(args, "-p")
		if request.Sealed {
			args = append(args, "--safe-mode")
		}
		if request.Model != "" {
			args = append(args, "--model", request.Model)
		}
		if request.Effort != "" {
			args = append(args, "--effort", request.Effort)
		}
		if !request.Native {
			args = append(args, "--output-format", "json")
		}
		if request.SystemPrompt != nil {
			if request.systemPromptFile != "" {
				args = append(args, "--system-prompt-file", request.systemPromptFile)
			} else {
				args = append(args, "--system-prompt", *request.SystemPrompt)
			}
		}
		if request.Schema != nil {
			args = append(args, "--json-schema", string(request.Schema))
		}
		if request.Tools != nil {
			args = append(args, "--tools", *request.Tools)
		}
		if request.SettingsSources != nil {
			args = append(args, "--setting-sources", *request.SettingsSources)
		}
		if request.StrictMCP {
			args = append(args, "--strict-mcp-config")
		}
		if request.NoSessionPersistence {
			args = append(args, "--no-session-persistence")
		}
	case pfmengine.Codex:
		args = append(args, "exec")
		if request.Model != "" {
			args = append(args, "--model", request.Model)
		}
		if request.Effort != "" {
			args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(request.Effort))
		}
		if request.systemPromptFile != "" {
			args = append(args, "-c", "model_instructions_file="+strconv.Quote(request.systemPromptFile))
		}
		if !request.Native {
			args = append(args, "--json")
		}
		if request.Schema != nil {
			if request.schemaFilePath == "" {
				return nil, fmt.Errorf("output schema file was not materialized")
			}
			args = append(args, "--output-schema", request.schemaFilePath)
		}
		if request.NoSessionPersistence || request.Sealed {
			args = append(args, "--ephemeral")
		}
		if request.Sealed {
			args = append(args, "--ignore-user-config", "--ignore-rules")
		}
		if !request.Native {
			args = append(args, "--skip-git-repo-check", "--color", "never")
		}
	default:
		return nil, fmt.Errorf("engine %s does not support headless runs", request.Engine)
	}
	args = append(args, request.Args...)
	// LaunchArgs carries the argv words every launch of this engine must
	// carry — for Claude, --settings {"outputStyle":"default"}. A headless run
	// is a door of its own (it never renders through action.ClaudeSpawn), so
	// it disables Claude Code's own output style too, the same as every other
	// Claude launch. Read off the descriptor the way setEnvironment reads
	// LaunchEnv, never gated on one engine id: an engine that gains LaunchArgs
	// later must reach this door without editing it. The switch above has
	// already refused every unregistered engine. These trail the caller's own
	// Args so a caller-supplied --output-format (harvest's ask adapter passes
	// its own) stays adjacent to the flags that came with it.
	args = append(args, pfmengine.LaunchArgsFor(request.Engine, request.Args)...)
	return args, nil
}

func setEnvironment(environment []string, id pfmengine.ID, configDir string, explicit bool, target *[]string) {
	dropped := map[string]struct{}{
		"CLAUDE_CODE_SESSION_ID": {}, "CLAUDECODE": {}, "CLAUDE_CODE_CHILD_SESSION": {},
		"CLAUDE_CONFIG_DIR": {}, "CODEX_THREAD_ID": {}, "TMUX": {}, "TMUX_PANE": {},
	}
	if !explicit {
		for _, name := range []string{
			"ENABLE_PROMPT_CACHING_1H", "FORCE_PROMPT_CACHING_5M",
			"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL",
			"OPENAI_API_KEY", "OPENAI_BASE_URL", "CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT",
			"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
			"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY",
		} {
			dropped[name] = struct{}{}
		}
	}
	name := pfmengine.MustLookup(id).HomeEnv
	if name != "" {
		dropped[name] = struct{}{}
	}
	filtered := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		if _, remove := dropped[key]; remove {
			continue
		}
		filtered = append(filtered, value)
	}
	if configDir != "" && name != "" {
		filtered = append(filtered, name+"="+configDir)
	}
	filtered = append(filtered, pfmengine.MustLookup(id).LaunchEnv...)
	*target = filtered
}

type boundedBuffer struct {
	bytes.Buffer
	truncated bool
	limited   bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	if !buffer.limited {
		return buffer.Buffer.Write(value)
	}
	remaining := maxCapturedOutput - buffer.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return len(value), nil
	}
	if len(value) > remaining {
		_, _ = buffer.Buffer.Write(value[:remaining])
		buffer.truncated = true
		return len(value), nil
	}
	return buffer.Buffer.Write(value)
}

func writerFor(stream, capture io.Writer) io.Writer {
	if stream == nil {
		return capture
	}
	return io.MultiWriter(stream, capture)
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type claudeEnvelope struct {
	Result     json.RawMessage `json:"result"`
	Structured json.RawMessage `json:"structured_output"`
	Usage      json.RawMessage `json:"usage"`
	ModelUsage json.RawMessage `json:"modelUsage"`
	TotalCost  json.RawMessage `json:"total_cost_usd"`
	IsError    bool            `json:"is_error"`
}

// parseModelUsage sums Claude's per-model `modelUsage` totals — the whole session, every API
// turn. The envelope's `usage` block is the LAST turn only, while `total_cost_usd` is the sum: a
// seat that took a second turn (a structured-output retry, a tool call) reported ~2k input tokens
// against a cost that says 60k. Nil when the block is absent or empty.
func parseModelUsage(raw json.RawMessage) (*TokenUsage, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil
	}
	var models map[string]struct {
		Input         int `json:"inputTokens"`
		Output        int `json:"outputTokens"`
		CacheRead     int `json:"cacheReadInputTokens"`
		CacheCreation int `json:"cacheCreationInputTokens"`
	}
	if err := json.Unmarshal(raw, &models); err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, nil
	}
	usage := &TokenUsage{}
	for name, m := range models {
		if m.Input < 0 || m.Output < 0 || m.CacheRead < 0 || m.CacheCreation < 0 {
			return nil, fmt.Errorf("modelUsage %s: negative token count", name)
		}
		usage.Input += m.Input
		usage.Output += m.Output
		usage.CachedInput += m.CacheRead
		usage.CacheCreation += m.CacheCreation
	}
	return usage, nil
}

func parseOutput(result *Result, request Request) error {
	switch request.Engine {
	case pfmengine.Claude:
		var envelope claudeEnvelope
		if err := json.Unmarshal([]byte(result.Stdout), &envelope); err != nil {
			return fmt.Errorf("parse Claude JSON envelope: %w", err)
		}
		result.IsError = envelope.IsError
		if len(envelope.Result) > 0 && string(envelope.Result) != jsonNull {
			if err := json.Unmarshal(envelope.Result, &result.Answer); err != nil {
				return fmt.Errorf("parse Claude result: %w", err)
			}
		}
		result.StructuredOutput = append(json.RawMessage(nil), envelope.Structured...)
		var err error
		result.Usage, err = parseTokenUsage(envelope.Usage)
		if err != nil {
			return fmt.Errorf("parse Claude usage: %w", err)
		}
		if whole, err := parseModelUsage(envelope.ModelUsage); err != nil {
			return fmt.Errorf("parse Claude modelUsage: %w", err)
		} else if whole != nil {
			result.Usage = whole // every turn, not the last one
		}
		result.TotalCostUSD, err = parseCost(envelope.TotalCost)
		if err != nil {
			return fmt.Errorf("parse Claude cost: %w", err)
		}
		if result.IsError {
			return fmt.Errorf("headless envelope reported an error for Claude")
		}
	case pfmengine.Codex:
		if err := parseCodexJSONL(result, request); err != nil {
			return err
		}
	default:
		return fmt.Errorf("engine %s does not support normalized output", request.Engine)
	}
	if request.Schema != nil {
		if len(result.StructuredOutput) == 0 {
			return fmt.Errorf("%w: engine did not return structured_output", ErrStructuredOutput)
		}
		if err := validateInstance(request.Schema, result.StructuredOutput); err != nil {
			return fmt.Errorf("%w: %v", ErrStructuredOutput, err)
		}
		if result.Answer == "" {
			result.Answer = string(result.StructuredOutput)
		}
	} else if strings.TrimSpace(result.Answer) == "" {
		return fmt.Errorf("%s headless output contains no answer", request.Engine)
	}
	return nil
}

func parseCodexJSONL(result *Result, request Request) error {
	var terminal bool
	for lineNo, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Type    string          `json:"type"`
			Message string          `json:"message"`
			Item    json.RawMessage `json:"item"`
			Usage   json.RawMessage `json:"usage"`
			Error   json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return fmt.Errorf("parse Codex JSONL event %d: %w", lineNo+1, err)
		}
		if event.Type == "" {
			return fmt.Errorf("event %d from Codex has no type", lineNo+1)
		}
		switch event.Type {
		case "error":
			// Codex uses error events for retries as well as failures. Only a
			// subsequent terminal success proves that the engine recovered.
			if terminal {
				result.Answer = ""
			}
			terminal = false
			message := event.Message
			if message == "" {
				message = "Codex reported an error"
			}
			result.Diagnostics = append(result.Diagnostics, message)
		case "turn.failed", "thread.failed":
			return fmt.Errorf("headless event %s from Codex: %s", event.Type, event.Error)
		case "turn.started":
			terminal = false
			result.Answer = ""
		case "turn.completed":
			if len(event.Error) != 0 && string(event.Error) != jsonNull {
				return fmt.Errorf("turn.completed event from Codex contains an error")
			}
			terminal = true
			var err error
			result.Usage, err = parseTokenUsage(event.Usage)
			if err != nil {
				return fmt.Errorf("parse Codex usage: %w", err)
			}
		case "item.completed":
			var item struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(event.Item, &item); err != nil {
				return fmt.Errorf("parse Codex completed item: %w", err)
			}
			if item.Type == "agent_message" {
				result.Answer = item.Text
			} else if item.Type == "error" && item.Message != "" {
				result.Diagnostics = append(result.Diagnostics, item.Message)
			}
		}
	}
	if !terminal {
		return fmt.Errorf("missing terminal success event in headless output from Codex")
	}
	if strings.TrimSpace(result.Answer) == "" {
		return fmt.Errorf("headless output from Codex contains no completed answer")
	}
	if request.Schema != nil {
		result.StructuredOutput = json.RawMessage(result.Answer)
	}
	return nil
}

func parseTokenUsage(raw json.RawMessage) (*TokenUsage, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	usage := &TokenUsage{}
	known := false
	for _, field := range []struct {
		names  []string
		target *int
	}{
		{[]string{"input_tokens", "prompt_tokens"}, &usage.Input},
		{[]string{"cached_input_tokens", "cache_read_input_tokens", "cached_tokens"}, &usage.CachedInput},
		{[]string{"cache_creation_input_tokens", "cache_write_input_tokens"}, &usage.CacheCreation},
		{[]string{"output_tokens", "completion_tokens"}, &usage.Output},
	} {
		for _, name := range field.names {
			value, present := values[name]
			if !present || string(value) == jsonNull {
				continue
			}
			if err := json.Unmarshal(value, field.target); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if *field.target < 0 {
				return nil, fmt.Errorf("%s must be nonnegative", name)
			}
			known = true
			break
		}
	}
	if !known {
		return nil, nil
	}
	return usage, nil
}

func parseCost(raw json.RawMessage) (*float64, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil
	}
	var cost float64
	if err := json.Unmarshal(raw, &cost); err != nil {
		return nil, err
	}
	if cost < 0 {
		return nil, fmt.Errorf("cost must be nonnegative")
	}
	return &cost, nil
}

func validateSchema(raw json.RawMessage) error {
	if strings.TrimSpace(string(raw)) == jsonNull {
		return fmt.Errorf("invalid output schema: null is not a JSON Schema")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("invalid output schema: %w", err)
	}
	if _, err := schema.Resolve(nil); err != nil {
		return fmt.Errorf("invalid output schema: %w", err)
	}
	return nil
}

func validateInstance(schemaRaw, instanceRaw json.RawMessage) error {
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return err
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(instanceRaw, &instance); err != nil {
		return err
	}
	return resolved.Validate(instance)
}

// failureDiagnostics is what a failed engine run leaves in the receipt: the stderr and stdout
// tails, and — for a Claude envelope on stdout — the error line the envelope carried.
func failureDiagnostics(result Result) []string {
	var lines []string
	if tail := boundedTail(result.Stderr, 1024); tail != "" {
		lines = append(lines, "stderr: "+tail)
	}
	if tail := boundedTail(result.Stdout, 1024); tail != "" {
		lines = append(lines, "stdout: "+tail)
	}
	var envelope claudeEnvelope
	if err := json.Unmarshal([]byte(result.Stdout), &envelope); err == nil && envelope.IsError {
		var text string
		if json.Unmarshal(envelope.Result, &text) == nil && text != "" {
			lines = append(lines, "engine error: "+boundedTail(text, 512))
		}
	}
	return lines
}

func boundedTail(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}
