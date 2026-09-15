package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	headlessrun "hostops/pfm/internal/headless/run"
)

func runHeadlessExec(args []string, stdin io.Reader, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := newFlagSet("headless exec", "usage: pfm headless exec [options] [-- ENGINE_ARGS...]\n"+
		"  --engine claude|codex --model MODEL --effort EFFORT --account ID\n"+
		"  --prompt TEXT | --prompt-file FILE | stdin\n"+
		"  --files FILE... --labels LABEL... --task TEXT | --task-file FILE\n"+
		"  --system TEXT | --system-file FILE --schema FILE | --json-schema JSON\n"+
		"  --sealed --tools LIST --setting-sources SOURCES --strict-mcp-config\n"+
		"  --allow-unsupported (continue with diagnostics when common controls are unsupported)\n"+
		"  --no-session-persistence --cwd DIR --timeout SECONDS (default 600; 0 unlimited)\n"+
		"  --output-format text|json|native --out FILE --receipt FILE\n"+
		"  --env KEY=VALUE --engine-arg ARG (repeatable; native forwards engine output unchanged)", stderr)
	engine := flags.String("engine", "", "engine (default from PFM config)")
	model := flags.String("model", "", "model (default from PFM config)")
	effort := flags.String("effort", "", "reasoning effort")
	account := flags.Int("account", 0, "configured account ID (default first account)")
	configDir := flags.String("config-dir", "", "select a configured account by its directory")
	prompt := flags.String("prompt", "", "prompt text")
	flags.StringVar(prompt, "p", "", "prompt text")
	promptFile := flags.String("prompt-file", "", "read prompt from file")
	task := flags.String("task", "", "task instruction with labeled file framing")
	taskFile := flags.String("task-file", "", "read the task instruction from a file")
	var files, labels repeatString
	flags.Var(&files, "files", "prepared input files, in order (zero or more)")
	flags.Var(&labels, "labels", "one label per file (default basenames)")
	system := flags.String("system", "", "replace the system prompt")
	flags.StringVar(system, "system-prompt", "", "replace the system prompt")
	systemFile := flags.String("system-file", "", "replacement system prompt file")
	flags.StringVar(systemFile, "system-prompt-file", "", "replacement system prompt file")
	schemaFile := flags.String("schema", "", "JSON Schema file")
	inlineSchema := flags.String("json-schema", "", "inline JSON Schema")
	tools := flags.String("tools", "", "available tools (empty disables tools)")
	settings := flags.String("setting-sources", "", "inherited settings sources (empty disables them)")
	strictMCP := flags.Bool("strict-mcp-config", false, "ignore inherited MCP servers")
	noPersistence := flags.Bool("no-session-persistence", false, "discard session persistence")
	sealed := flags.Bool(
		"sealed",
		false,
		"scratch working directory, replacement system, no inherited settings/tools/MCP or session persistence",
	)
	allowUnsupported := flags.Bool(
		"allow-unsupported",
		false,
		"continue without unsupported common controls and report them",
	)
	cwd := flags.String("cwd", "", "working directory")
	timeout := flags.Float64("timeout", 600, "wall-clock timeout in seconds; 0 unlimited")
	format := flags.String("output-format", "text", "text, json (common result envelope), or native (engine stream)")
	out := flags.String("out", "", "write structured output, or the text answer, to this file")
	receipt := flags.String("receipt", "", "append a content-free JSON execution receipt")
	var engineArgs, environment repeatString
	flags.Var(&engineArgs, "engine-arg", "append one native engine argument (repeatable)")
	flags.Var(&environment, "env", "set one engine environment value (repeatable)")
	var tail []string
	for index, arg := range args {
		if arg == "--" {
			tail, args = args[index+1:], args[:index]
			break
		}
	}
	args, hasFiles := expandHeadlessFileArgs(flags, args)
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *account < 0 || math.IsNaN(*timeout) || math.IsInf(*timeout, 0) || *timeout < 0 ||
		*timeout >= float64(math.MaxInt64)/float64(time.Second) {
		flags.Usage()
		return 2
	}
	if *format != "text" && *format != "json" && *format != "native" {
		fmt.Fprintln(stderr, "pfm headless: --output-format must be text, json, or native")
		return 2
	}
	present := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { present[f.Name] = true })
	hasPrompt := present["prompt"] || present["p"]
	hasSystem := present["system"] || present["system-prompt"]
	promptSources := 0
	for _, supplied := range []bool{hasPrompt, present["prompt-file"], present["task"], present["task-file"]} {
		if supplied {
			promptSources++
		}
	}
	if promptSources > 1 || (hasSystem && (present["system-file"] || present["system-prompt-file"])) ||
		(present["schema"] && present["json-schema"]) ||
		(*format == "native" && *out != "") {
		fmt.Fprintln(
			stderr,
			"pfm headless: choose one source per prompt/system/schema; --out requires normalized output",
		)
		return 2
	}
	if len(labels) != 0 && len(labels) != len(files) {
		fmt.Fprintln(stderr, "pfm headless: --labels must match --files")
		return 2
	}
	request := headlessrun.Request{
		Config: runtime.Config, Account: *account, ConfigDir: *configDir,
		Model: *model, Effort: *effort, Prompt: *prompt, CWD: *cwd, TempDir: runtime.Paths.SIDDir,
		Timeout: time.Duration(*timeout * float64(time.Second)), StrictMCP: *strictMCP,
		NoSessionPersistence: *noPersistence, Sealed: *sealed, Native: *format == "native",
		AllowUnsupported: *allowUnsupported,
		Args:             append([]string(engineArgs), tail...),
	}
	if *engine != "" {
		id, err := pfmengine.Parse(*engine)
		if err != nil {
			fmt.Fprintf(stderr, "pfm headless: %v\n", err)
			return 2
		}
		request.Engine = id
	}
	read := func(path string) (string, error) {
		body, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		return string(body), nil
	}
	var err error
	framed := hasFiles || present["task"] || present["task-file"]
	switch {
	case present["task-file"]:
		request.Prompt, err = read(*taskFile)
		request.Prompt = strings.TrimSpace(request.Prompt)
	case present["task"]:
		request.Prompt = *task
	case present["prompt-file"]:
		request.Prompt, err = read(*promptFile)
	case !hasPrompt:
		if request.Native && !framed {
			request.Stdin = stdin
		} else {
			var body []byte
			body, err = io.ReadAll(stdin)
			request.Prompt = string(body)
		}
	}
	if err == nil && framed {
		var parts []string
		for index, path := range files {
			body, readErr := read(path)
			if readErr != nil {
				err = readErr
				break
			}
			label := filepath.Base(path)
			if len(labels) != 0 {
				label = labels[index]
			}
			parts = append(
				parts,
				fmt.Sprintf("===== FILE %d: %s =====\n%s\n===== END FILE %d =====", index+1, label, body, index+1),
			)
		}
		parts = append(parts, "TASK: "+request.Prompt)
		request.Prompt = strings.Join(parts, "\n\n") + "\n"
	}
	if err == nil && (present["system-file"] || present["system-prompt-file"]) {
		*system, err = read(*systemFile)
		hasSystem = true
	}
	if err == nil && present["schema"] {
		*inlineSchema, err = read(*schemaFile)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm headless: prepare input: %v\n", err)
		return 2
	}
	if hasSystem {
		request.SystemPrompt = system
	}
	if present["schema"] || present["json-schema"] {
		request.Schema = json.RawMessage(*inlineSchema)
		if !json.Valid(request.Schema) {
			fmt.Fprintln(stderr, "pfm headless: schema is not valid JSON")
			return 2
		}
	}
	if present["tools"] {
		request.Tools = tools
	}
	if present["setting-sources"] {
		request.SettingsSources = settings
	}
	if len(environment) != 0 {
		request.Env = os.Environ()
		for _, entry := range environment {
			name, _, ok := strings.Cut(entry, "=")
			if !ok || name == "" || strings.ContainsRune(entry, '\x00') {
				fmt.Fprintln(stderr, "pfm headless: --env requires KEY=VALUE")
				return 2
			}
			kept := request.Env[:0]
			for _, old := range request.Env {
				if !strings.HasPrefix(old, name+"=") {
					kept = append(kept, old)
				}
			}
			kept = append(kept, entry)
			request.Env = kept
		}
	}
	if request.Native {
		request.Stdout, request.Stderr = stdout, stderr
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	result, runErr := headlessrun.Run(ctx, request)
	for _, diagnostic := range result.Diagnostics {
		fmt.Fprintf(stderr, "pfm headless: engine diagnostic: %s\n", diagnostic)
	}
	code := 0
	if runErr != nil {
		code = 4
		if errors.Is(runErr, context.DeadlineExceeded) {
			code = 3
		} else if errors.Is(runErr, headlessrun.ErrStructuredOutput) {
			code = 5
		}
		fmt.Fprintf(stderr, "pfm headless: %v\n", runErr)
	}
	if *format == "json" {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "pfm headless: write result: %v\n", err)
			code = 4
		}
	} else if *format == "text" && runErr == nil {
		if _, err := fmt.Fprintln(stdout, result.Answer); err != nil {
			fmt.Fprintf(stderr, "pfm headless: write answer: %v\n", err)
			code = 4
		}
	}
	if *out != "" && runErr == nil {
		body := []byte(result.Answer)
		if len(result.StructuredOutput) != 0 {
			body = result.StructuredOutput
		}
		if err := os.WriteFile(*out, append(body, '\n'), 0o600); err != nil {
			fmt.Fprintf(stderr, "pfm headless: write %s: %v\n", *out, err)
			code = 4
		}
	}
	if *receipt != "" {
		body, err := json.Marshal(struct {
			Engine      pfmengine.ID            `json:"engine"`
			Model       string                  `json:"model"`
			Effort      string                  `json:"effort"`
			Seconds     float64                 `json:"seconds"`
			Cost        *float64                `json:"cost_usd"`
			Usage       *headlessrun.TokenUsage `json:"usage"`
			Exit        int                     `json:"exit"`
			EngineExit  int                     `json:"engine_exit"`
			Timeout     bool                    `json:"timeout"`
			Error       string                  `json:"error,omitempty"`
			Diagnostics []string                `json:"diagnostics,omitempty"`
		}{result.Engine, result.Model, result.Effort, result.Duration.Seconds(), result.TotalCostUSD, result.Usage, code, result.ExitCode, result.TimedOut, errorText(runErr), result.Diagnostics})
		if err == nil {
			var file *os.File
			file, err = os.OpenFile(*receipt, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
			if err == nil {
				_, writeErr := file.Write(append(body, '\n'))
				err = errors.Join(writeErr, file.Close())
			}
		}
		if err != nil {
			fmt.Fprintf(stderr, "pfm headless: write receipt: %v\n", err)
			code = 4
		}
	}
	return code
}

// errorText is the run error as the receipt records it — empty when the run succeeded.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Expand the lab's multi-value file options into stdlib flag's repeatable form.
// Scalar option values are opaque: a system prompt equal to "--files" is text.
// The caller has already separated native arguments after the -- boundary.
func expandHeadlessFileArgs(flags *flag.FlagSet, args []string) ([]string, bool) {
	var expanded []string
	framed := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if strings.HasPrefix(arg, "-") && (name == "files" || name == "labels") {
			framed = true
			if inline {
				expanded = append(expanded, arg)
			} else {
				for index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
					index++
					expanded = append(expanded, "--"+name+"="+args[index])
				}
			}
			continue
		}
		expanded = append(expanded, arg)
		if !inline && strings.HasPrefix(arg, "-") && index+1 < len(args) {
			if option := flags.Lookup(name); option != nil {
				boolean, ok := option.Value.(interface{ IsBoolFlag() bool })
				if !ok || !boolean.IsBoolFlag() {
					index++
					expanded = append(expanded, args[index])
				}
			}
		}
	}
	return expanded, framed
}
