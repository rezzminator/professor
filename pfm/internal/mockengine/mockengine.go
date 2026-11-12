// Package mockengine is the scripted stand-in for the three engines pfm drives
// — Claude Code, Codex and OpenCode — selected by argv[0]'s basename. It speaks
// only the protocol doors pfm consumes: the TUI pane shapes internal/call
// matches, the hooks and statusline from settings.json and --settings, the transcript
// and rollout files internal/transcript and internal/index read, the OpenCode
// session store, and the MCP-over-HTTP handshake. Behaviour comes from a JSON
// scenario (MOCK_ENGINE_SCENARIO); a shape pfm has not pinned is refused by
// name (ExitUnpinned), never invented.
//
// Entry point: Main. The other files:
//
//   - scenario.go: the scenario, its steps, the inline `mock-engine: ` directive and the step cursor.
//   - pane.go: the TUI every engine shares — the idle loop, a turn and its steps, /exit.
//   - pane_screen.go: the screen the TUI repaints — history, footer or menu, composer, statusline.
//   - agents.go: the background agents behind an idle composer — their rows, Claude's wait row, a
//     gated agent's end and the turn that reports it.
//   - tty.go: the raw terminal and the key decoder.
//   - claude.go: Claude Code — its pane, hooks fired per beat, transcript, statusline, headless -p.
//   - claude_resume.go: a resumed Claude transcript's title and a fork's seeded copy.
//   - claude_tooluse.go: Claude's PostToolUse beat and the tool response it carries.
//   - hooks.go: the settings hook documents, running a hook command and reading its answer.
//   - statusline.go: a settings layer's statusLine and one run of its command.
//   - session_end.go: SessionEnd hooks that outlive a hangup on every engine, and Claude's session end.
//   - codex.go: Codex — its pane, rollout, app-server and MCP handshake.
//   - codex_hooks.go: Codex's hook trust and its hooks.json state.
//   - codex_fire.go: the trusted hooks.json handlers a Codex session fires, at each beat it plays.
//   - opencode.go: OpenCode — `opencode run`, its TUI and session store.
//   - opencode_serve.go: OpenCode's `serve` HTTP API.
//   - opencode_plugin.go: pfm's Herdr reporter plugin as OpenCode loads it — start, working, title, idle and end.
//   - cli.go: the engines' non-chat verbs (plugin marketplace and install records).
//   - jail.go: the mock's presence on a jailed host — its /proc entry and sid crumb.
package mockengine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// The engine names are argv[0] spellings: each descriptor's Binary
// (internal/engine/builtin.go), never restated here.
var (
	engineClaude   = pfmengine.MustLookup(pfmengine.Claude).Binary
	engineCodex    = pfmengine.MustLookup(pfmengine.Codex).Binary
	engineOpenCode = pfmengine.MustLookup(pfmengine.OpenCode).Binary
)

// Spellings shared by the engines' records and command lines.
const (
	roleUser        = "user"
	roleAssistant   = "assistant"
	roleDeveloper   = "developer"
	sourceStartup   = "startup"
	sourceClear     = "clear"
	sourceResume    = "resume"
	flagModel       = "--model"
	blockText       = "text"
	blockInputText  = "input_text"
	blockOutputText = "output_text"
	keyType         = "type"
	keyCWD          = "cwd"
)

// process is one mock engine run: the engine, its argv, its streams, the
// resolved scenario and the host facts every mode shares.
type process struct {
	ctx      context.Context
	engine   string
	args     []string
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	env      func(string) string
	script   *script
	cwd      string
	pid      int
	recorder recorder
}

// Main is the whole mock: argv0 selects the engine, args are its command line,
// env is the process environment. It returns the process exit code.
func Main(
	ctx context.Context,
	argv0 string,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	env func(string) string,
) int {
	engine := filepath.Base(argv0)
	switch engine {
	case engineClaude, engineCodex, engineOpenCode:
	default:
		override := env(EnvEngine)
		switch override {
		case engineClaude, engineCodex, engineOpenCode:
			engine = override
		default:
			fmt.Fprintf(
				stderr,
				"mock-engine: argv[0] %q names no engine (claude|codex|opencode) and %s=%q selects none\n",
				engine, EnvEngine, override,
			)
			return ExitUsage
		}
	}
	scenario, err := LoadScenario(env(EnvScenario), engine)
	if err != nil {
		fmt.Fprintf(stderr, "mock-engine: %v\n", err)
		return ExitUsage
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "mock-engine: resolve cwd: %v\n", err)
		return ExitUsage
	}
	proc := &process{
		ctx: ctx, engine: engine, args: args, stdin: stdin, stdout: stdout, stderr: stderr, env: env,
		script: newScript(scenario, engine), cwd: cwd, pid: os.Getpid(),
		recorder: recorder{dir: scenario.RecordDir},
	}
	if err := proc.script.resume(env(EnvScenario)); err != nil {
		fmt.Fprintf(stderr, "mock-engine: %v\n", err)
		return ExitUsage
	}
	if err := proc.recorder.write("argv", strings.Join(append([]string{engine}, args...), "\n")+"\n"); err != nil {
		fmt.Fprintf(stderr, "mock-engine: %v\n", err)
		return ExitUsage
	}
	for _, arg := range args {
		if arg == "--version" {
			fmt.Fprintln(stdout, proc.script.Version)
			return 0
		}
	}
	if code, answered := answerCLI(proc); answered {
		return code
	}
	switch engine {
	case engineClaude:
		return serveClaude(proc)
	case engineCodex:
		return serveCodex(proc)
	default:
		return serveOpenCode(proc)
	}
}

// recorder appends the mock's own telemetry under a directory the scenario
// names. It is evidence for a test, never the judge; an empty dir records
// nothing.
type recorder struct {
	dir string
}

func (rec recorder) write(name, content string) error {
	if rec.dir == "" {
		return nil
	}
	if err := os.MkdirAll(rec.dir, 0o700); err != nil {
		return fmt.Errorf("create record dir %s: %w", rec.dir, err)
	}
	file, err := os.OpenFile(filepath.Join(rec.dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open record %s: %w", name, err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write record %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close record %s: %w", name, err)
	}
	return nil
}

// appendLine adds one line to a file the engine owns, creating it and its
// directory on first use — the append-only shape every transcript reader
// (internal/transcript.ReadLines, internal/transcript.From) expects.
func appendLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("append %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// touchFile creates an empty file (and its directory) when none exists.
func touchFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// stamp is the RFC3339 millisecond timestamp Claude and Codex write.
func stamp(now time.Time) string {
	return now.UTC().Format("2006-01-02T15:04:05.000Z")
}

// sleepOrCancel waits for the duration unless ctx ends first.
func sleepOrCancel(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
