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

	"hostops/pfm/internal/atomicfile"
	"hostops/pfm/internal/clock"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/paths"
)

const (
	maxCapturedOutput = 8 << 20
	jsonNull          = "null"
	// One spelling per JSON literal the engine event parsers read and write.
	jsonKeyType    = "type"
	jsonEventText  = "text"
	jsonEventError = "error"
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
	WithoutAccount      bool
	Stdin               io.Reader
	Stdout              io.Writer
	Stderr              io.Writer
	Native              bool
	Clock               clock.Clock
	Runner              deps.Runner
	systemPromptFile    string
	schemaFilePath      string
	binaryPath          string
	unsupportedOptions  []string
	openCodePluginReady string
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
		// OpenCode exports no home variable of its own: its data home is
		// XDG_DATA_HOME/opencode, so that is where a without-account run's
		// explicit environment names the account directory.
		if request.Engine == pfmengine.OpenCode && request.ConfigDir == "" {
			for _, entry := range request.Env {
				if value, ok := strings.CutPrefix(entry, "XDG_DATA_HOME="); ok && value != "" {
					request.ConfigDir = filepath.Join(value, openCodeDataDirName())
				}
			}
		}
	} else if !rosterPresent {
		return Request{}, fmt.Errorf("%s account roster is empty; configure an account", request.Engine)
	}
	binaryPath, err := obs.Runner(deps.RealRunner{}).LookPath(binary)
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
	// OpenCode needs a model even for a native pass-through: its `run` has no
	// configured default the way Claude's and Codex's CLIs do.
	if !request.Native || request.Engine == pfmengine.OpenCode {
		prefs := request.Config.Ask.PrefsFor(request.Engine)
		if request.Engine == pfmengine.OpenCode {
			codexPrefs := request.Config.Ask.PrefsFor(pfmengine.Codex)
			if prefs.Model == "" {
				prefs.Model = codexPrefs.Model
			}
			if prefs.Effort == "" {
				prefs.Effort = codexPrefs.Effort
			}
		}
		if request.Model == "" {
			request.Model = prefs.Model
		}
		if request.Effort == "" {
			request.Effort = prefs.Effort
		}
	}
	if request.Engine == pfmengine.OpenCode && request.Effort != "" && !openCodeEffortSupported(request.Effort) {
		return Request{}, fmt.Errorf(
			"OpenCode does not support reasoning effort %q (want none, minimal, low, medium, high, or xhigh)",
			request.Effort,
		)
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
	if request.Clock == nil {
		request.Clock = clock.Real
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
	case pfmengine.OpenCode:
		binary = strings.TrimSpace(request.Config.OpenCode.Binary)
		accounts = make([]configuredEngineAccount, 0, len(request.Config.OpenCodeAccounts))
		for _, account := range request.Config.OpenCodeAccounts {
			accounts = append(accounts, configuredEngineAccount{id: account.ID, configDir: account.Home})
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
	runClock := request.Clock
	started := runClock.Now()
	ctx := parent
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, request.Timeout)
		defer cancel()
	}
	// The engine process is not always the last thing a run waits on: an
	// OpenCode run reads the persisted assistant message after `run --attach`
	// returns. Stamping the duration here, last, keeps the receipt honest
	// instead of reporting only the part that happened to be a subprocess.
	defer func() {
		result.Duration = runClock.Now().Sub(started)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
		}
	}()

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
	if request.SystemPrompt != nil &&
		(request.Engine == pfmengine.Claude || request.Engine == pfmengine.Codex ||
			request.Engine == pfmengine.OpenCode) {
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
	if request.Schema != nil && (request.Engine == pfmengine.Codex || request.Engine == pfmengine.OpenCode) {
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

	args, err := arguments(request)
	if err != nil {
		return result, err
	}
	environment := os.Environ()
	if request.Env != nil {
		environment = append([]string(nil), request.Env...)
	}
	setEnvironment(
		environment, request.Engine, request.ConfigDir, request.Env != nil, &environment, subagentCaps(request)...,
	)
	var openCode openCodeRun
	if request.Engine == pfmengine.OpenCode {
		openCode, err = startOpenCode(ctx, &request, environment, cwd)
		if err != nil {
			return result, err
		}
		previousCleanup := cleanup
		cleanup = func() error { return openCode.stop(previousCleanup) }
		environment = openCode.environment
		args = attachOpenCodeRun(args, openCode.address)
		if request.Schema != nil {
			if err := primeOpenCodeSchema(ctx, openCode.address, cwd, environment, request); err != nil {
				return result, err
			}
		}
	}
	argv := append([]string{request.binaryPath}, args...)
	if request.Stdin == nil {
		request.Stdin = strings.NewReader(request.Prompt)
	}

	stdout := boundedBuffer{limited: !request.Native}
	stderr := boundedBuffer{limited: !request.Native}
	runErr = runProcess(ctx, request.Runner, argv, deps.StartOptions{
		Env: environment, Dir: cwd, Stdin: request.Stdin,
		Stdout: writerFor(request.Stdout, &stdout), Stderr: writerFor(request.Stderr, &stderr),
		ProcessGroup: true, WaitDelay: processWaitAfterCancel,
	})
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	result.ExitCode = processExitCode(runErr)
	if request.Engine == pfmengine.OpenCode && runErr == nil {
		runErr = applyOpenCodeRunOutput(ctx, openCode.address, cwd, environment, &result, request)
	}
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
		if err := verifyOpenCodePluginFor(request); err != nil {
			result.IsError = true
			return result, err
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
	if err := verifyOpenCodePluginFor(request); err != nil {
		result.IsError = true
		return result, err
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
	case pfmengine.OpenCode:
		if len(request.Args) != 0 {
			if err := validateOpenCodeArgs(request.Args, request); err != nil {
				return nil, err
			}
		}
		args = append(args, "run")
		if request.Model != "" {
			args = append(args, "--model", openCodeModel(request.Model))
		}
		if request.Effort != "" {
			args = append(args, "--variant", request.Effort)
		}
		if !request.Native {
			args = append(args, "--format", "json")
		}
	default:
		return nil, fmt.Errorf("engine %s does not support headless runs", request.Engine)
	}
	args = append(args, request.Args...)
	// LaunchArgs carries the argv words every launch of this engine must
	// carry — for Claude, --settings {"outputStyle":"default"}, merged with
	// the account's resolved theme (empty for WithoutAccount, which reads no
	// roster) the same binary-pattern EffectiveClaude call action.ClaudeSpawn
	// makes. A headless run is a door of its own, so it disables Claude
	// Code's own output style too. These trail the caller's own Args so a
	// caller-supplied --output-format stays adjacent to the flags it came with.
	settings := ""
	if request.Engine == pfmengine.Claude && !request.WithoutAccount {
		settings = pfmengine.ClaudeSettingsPayload(request.Config.EffectiveClaude(request.Account).Theme)
	}
	args = append(args, pfmengine.LaunchArgsWithSettings(request.Engine, request.Args, settings)...)
	return args, nil
}

// subagentCaps is the headless door's share of the sub-agent capacity policy.
// A headless run spawns sub-agents like any other Claude chat, so it carries
// the same caps the tmux-borne launches get from action.ClaudeSpawn; Codex and
// OpenCode never read these names and get nothing.
func subagentCaps(request Request) []string {
	if request.Engine != pfmengine.Claude {
		return nil
	}
	return request.Config.EffectiveClaude(request.Account).SubagentEnv()
}

func setEnvironment(
	environment []string,
	id pfmengine.ID,
	configDir string,
	explicit bool,
	target *[]string,
	extra ...string,
) {
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
	filtered = append(filtered, extra...)
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
	return deps.ExitCode(err)
}
