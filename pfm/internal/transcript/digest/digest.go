// Package digest is `pfm transcript`, the mechanical reader of one Claude Code,
// Codex or OpenCode session transcript. One pass parses a transcript into events
// (prompts, replies, tool calls with their results, notes); filters select; a
// renderer prints one compact line per event, its transcript line number
// first. Every record not rendered is counted by type and named in the
// SKIPPED line: an unparsed record never disappears. Verbs: show (the
// digest), counts (per-tool table), types (every record type and its
// disposition), locate (the resolved path), agents (a session's sub-agents).
//
// Its bytes are the Python reader's it replaced (testdata/cases holds that
// reader's captured runs); each deliberate difference is a {label}.intended golden
// beside the case it changes. It reads through the shared transcript record,
// block, title, timestamp and lookup types, never a decoder of its own, and an
// OpenCode session's store rows through internal/index (opencode.go maps them).
package digest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	failedPrefix  = "TRANSCRIPT FAILED — "
	warningPrefix = "TRANSCRIPT WARNING — "
)

// Env is where a run looks: pfm's resolved Claude roots, Codex homes and
// OpenCode roots, and the home directory a digest shortens to "~/".
type Env struct {
	ClaudeRoots   []string
	CodexHomes    []string
	OpenCodeRoots []string
	Home          string
}

// EnvOf is the Env pfm's resolved paths name: every Claude projects root,
// every Codex home, every OpenCode root and the home directory.
func EnvOf(values paths.Values) Env {
	return Env{
		ClaudeRoots:   values.Roots[pfmengine.Claude],
		CodexHomes:    values.Roots[pfmengine.Codex],
		OpenCodeRoots: values.Roots[pfmengine.OpenCode],
		Home:          values.Home,
	}
}

// Output is one run's stdout text and its warnings, each one stderr line
// without its newline. Warnings are kept on a failed run too.
type Output struct {
	Text     string
	Warnings []string
}

// Failure is a refusal the run reports as `TRANSCRIPT FAILED — {Message}`:
// Usage marks a request that cannot be served as written (exit 2), any other
// a transcript that could not be found or read (exit 1).
type Failure struct {
	Message string
	Usage   bool
}

func (failure *Failure) Error() string { return failure.Message }

func usageFailure(format string, args ...any) error {
	return &Failure{Message: fmt.Sprintf(format, args...), Usage: true}
}

func runtimeFailure(format string, args ...any) error {
	return &Failure{Message: fmt.Sprintf(format, args...)}
}

// DigestTranscript runs one `pfm transcript` argument list.
func DigestTranscript(ctx context.Context, args []string, env Env) (Output, error) {
	var out Output
	req, err := parseDigestArgs(args)
	if err != nil {
		return out, err
	}
	if req.help {
		out.Text = usageText
		return out, nil
	}
	finder := &resolver{ctx: ctx, env: env, extra: req.roots, out: &out}
	path, err := finder.resolve(req.target)
	if err != nil {
		return out, err
	}
	switch req.verb {
	case "locate":
		if storePath, id, ok := splitOpenCodeLocator(path); ok {
			if err := openCodeLocate(ctx, storePath, id); err != nil {
				return out, err
			}
		}
		out.Text = path + "\n"
		return out, nil
	case "agents":
		if storePath, id, ok := splitOpenCodeLocator(path); ok {
			out.Text, err = openCodeAgents(ctx, storePath, id)
		} else {
			out.Text, err = sessionAgents(path)
		}
		return out, err
	}
	var parsed *parsed
	if storePath, id, ok := splitOpenCodeLocator(path); ok {
		parsed, err = parseOpenCode(ctx, storePath, id)
	} else {
		parsed, err = parseTranscript(ctx, path)
	}
	if err != nil {
		return out, err
	}
	var text string
	switch req.verb {
	case "show":
		text, err = showDigest(parsed, &req, env.Home)
	case "counts":
		text, err = countsDigest(parsed, &req)
	default:
		text, err = typesDigest(parsed, &req)
	}
	if err != nil {
		return out, err
	}
	if req.out == "" {
		out.Text = text
		return out, nil
	}
	if err := writeDigest(req.out, text); err != nil {
		return out, err
	}
	out.Text = fmt.Sprintf("WROTE %s · %d bytes · %d lines\n", req.out, len(text), strings.Count(text, "\n"))
	return out, nil
}

// writeDigest writes the digest to out, creating its directory.
func writeDigest(out, text string) error {
	absolute, err := filepath.Abs(out)
	if err != nil {
		return runtimeFailure("--out %s: %v", out, err)
	}
	if info, err := os.Stat(absolute); err == nil && info.IsDir() {
		return runtimeFailure("--out %s: is a directory", out)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return runtimeFailure("--out %s: %v", out, err)
	}
	if err := atomicfile.Write(absolute, []byte(text), 0o644); err != nil {
		return runtimeFailure("--out %s: %v", out, err)
	}
	return nil
}

// Invoke runs args and writes the run to the streams: each warning, then the
// text, or the failure line. It returns the exit code: 0, 2 for a usage
// failure, 1 for any other.
func Invoke(ctx context.Context, args []string, stdout, stderr io.Writer, env Env) int {
	out, err := DigestTranscript(ctx, args, env)
	for _, warning := range out.Warnings {
		fmt.Fprintln(stderr, warning)
	}
	if err != nil {
		fmt.Fprintln(stderr, failedPrefix+err.Error())
		if failure := (*Failure)(nil); errors.As(err, &failure) && failure.Usage {
			return 2
		}
		return 1
	}
	if _, err := io.WriteString(stdout, out.Text); err != nil {
		fmt.Fprintln(stderr, failedPrefix+"write the digest: "+err.Error())
		return 1
	}
	return 0
}
