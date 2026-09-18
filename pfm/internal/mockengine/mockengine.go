// Package mockengine is the scripted stand-in for the three engines pfm drives
// — Claude Code, Codex and OpenCode — selected by argv[0]'s basename. It speaks
// only the protocol doors pfm consumes: the TUI pane shapes internal/inject
// matches, the hooks and statusline settings.json registers, the transcript
// and rollout files internal/transcript and internal/index read, the OpenCode
// session store, and the MCP-over-HTTP handshake. Behaviour comes from a JSON
// scenario (MOCK_ENGINE_SCENARIO); a shape pfm has not pinned is refused by
// name (ExitUnpinned), never invented.
package mockengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	pfmengine "hostops/pfm/internal/engine"
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
	scenario, err := LoadScenario(env(EnvScenario))
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
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, proc.script.Version)
		return 0
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
// (internal/index readCompleteLines, internal/transcript.From) expects.
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

// newUUID is a random version-4 UUID in the 8-4-4-4-12 spelling
// gather.isUUID accepts.
func newUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand never fails on the platforms pfm builds for; a fixed
		// fixture id is still a valid UUID rather than a crash.
		warn(os.Stderr, "crypto/rand.Read failed, falling back to a fixed fixture UUID: %v", err)
		return "00000000-0000-4000-8000-000000000000"
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	text := hex.EncodeToString(raw[:])
	return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:32]
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
