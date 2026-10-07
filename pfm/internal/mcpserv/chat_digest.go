package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

const (
	// digestRunTimeout bounds one transcript.py run; digestErrorBytes bounds the
	// script output an error message carries.
	digestRunTimeout        = 60 * time.Second
	digestErrorBytes        = 4 << 10
	digestDefaultBytes      = 64 << 10
	digestMaxBytes          = 1 << 20
	digestScriptUnderSource = "templates/global/skills/transcript/transcript.py"
	digestScriptUnderHome   = ".claude/skills/transcript/transcript.py"
)

var digestResultsForm = regexp.MustCompile(`^(brief|none|full|tail:[1-9]\d*)$`)

// chatDigest is chat_digest: `transcript.py show` over any Claude or Codex
// transcript, its digest bounded to max_bytes at a line end.
func (service *Service) chatDigest(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input DigestInput,
) (*mcp.CallToolResult, DigestOutput, error) {
	flags, maxBytes, err := digestArgs(input)
	if err != nil {
		return nil, DigestOutput{}, fmt.Errorf("chat_digest: %w", err)
	}
	script, err := digestScript()
	if err != nil {
		return nil, DigestOutput{}, fmt.Errorf("chat_digest: %w", err)
	}
	runner := service.backend.runner
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	if _, err := runner.LookPath("python3"); err != nil {
		return nil, DigestOutput{}, fmt.Errorf(
			"chat_digest: python3 not found on PATH, the transcript script cannot run: %w",
			err,
		)
	}
	timeout := service.backend.digestTimeout
	if timeout <= 0 {
		timeout = digestRunTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := append([]string{"python3", "-I", script, "show"}, flags...)
	argv = append(argv, "--", input.Source)
	result, runErr := runner.Run(runCtx, argv, deps.RunOptions{})
	switch {
	case ctx.Err() != nil:
		return nil, DigestOutput{}, fmt.Errorf(
			"chat_digest: cancelled before the digest of %q finished: %w",
			input.Source,
			ctx.Err(),
		)
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return nil, DigestOutput{}, fmt.Errorf(
			"chat_digest: timed out after %s digesting %q; narrow it with lines, since or last",
			timeout.Round(time.Second), input.Source)
	case runErr != nil:
		return nil, DigestOutput{}, fmt.Errorf("chat_digest: run %s for %q: %w", script, input.Source, runErr)
	case result.ExitCode != 0:
		output := strings.TrimSpace(string(result.Stderr) + string(result.Stdout))
		if output == "" {
			output = "no output"
		}
		return nil, DigestOutput{}, fmt.Errorf(
			"chat_digest: %s (exit %d)", cutBytes(output, digestErrorBytes), result.ExitCode)
	}
	text := string(result.Stdout)
	output := DigestOutput{TotalBytes: len(text)}
	if len(text) > maxBytes {
		text = cutAtLine(text, maxBytes)
		output.Truncated = true
	}
	output.Text, output.Bytes = text, len(text)
	return nil, output, nil
}

// digestArgs validates the input at entry and returns the `show` flags, each
// one argv word `--flag=value` so a value that starts with a dash (since
// "-15m") is never read as an option, and the byte bound.
func digestArgs(input DigestInput) ([]string, int, error) {
	if strings.TrimSpace(input.Source) == "" {
		return nil, 0, fmt.Errorf("source is required")
	}
	if strings.HasPrefix(input.Source, "-") {
		return nil, 0, fmt.Errorf("source %q must not start with a dash", input.Source)
	}
	var flags []string
	for _, field := range []struct{ name, flag, value string }{
		{"lines", "--lines", input.Lines},
		{"grep", "--grep", input.Grep},
		{"only", "--only", input.Only},
		{"tool", "--tool", input.Tool},
		{"results", "--results", input.Results},
	} {
		if strings.HasPrefix(field.value, "-") {
			return nil, 0, fmt.Errorf("%s %q must not start with a dash", field.name, field.value)
		}
		if field.value != "" {
			flags = append(flags, field.flag+"="+field.value)
		}
	}
	if input.Results != "" && !digestResultsForm.MatchString(input.Results) {
		return nil, 0, fmt.Errorf(
			"results %q must be brief, none, full or tail:N with N a positive integer",
			input.Results,
		)
	}
	for _, field := range []struct{ name, flag, value string }{
		{"since", "--since", input.Since},
		{"until", "--until", input.Until},
	} {
		if field.value != "" {
			flags = append(flags, field.flag+"="+field.value)
		}
	}
	if input.IgnoreCase {
		flags = append(flags, "--ignore-case")
	}
	for _, field := range []struct {
		name, flag string
		value      int
	}{
		{"first", "--first", input.First},
		{"last", "--last", input.Last},
		{"text", "--text", input.Text},
	} {
		if field.value < 0 {
			return nil, 0, fmt.Errorf("%s must not be negative", field.name)
		}
		if field.value > 0 {
			flags = append(flags, field.flag+"="+strconv.Itoa(field.value))
		}
	}
	maxBytes := input.MaxBytes
	if maxBytes == 0 {
		maxBytes = digestDefaultBytes
	}
	if maxBytes < 1 || maxBytes > digestMaxBytes {
		return nil, 0, fmt.Errorf("max_bytes must be between 1 and %d", digestMaxBytes)
	}
	return flags, maxBytes, nil
}

// digestScript finds transcript.py: the source clone's original first, then
// the installed skill under the home. Neither is an error naming both paths.
func digestScript() (string, error) {
	var tried []string
	if repo := professor.DiscoverSourceRepo(); repo != "" {
		candidate := filepath.Join(repo, filepath.FromSlash(digestScriptUnderSource))
		found, note := digestScriptAt(candidate)
		if found {
			return candidate, nil
		}
		tried = append(tried, note)
	} else {
		tried = append(tried, "the source clone was not found")
	}
	home, err := paths.Home()
	if err != nil {
		tried = append(tried, fmt.Sprintf("the home directory did not resolve: %v", err))
	} else {
		candidate := filepath.Join(home, filepath.FromSlash(digestScriptUnderHome))
		found, note := digestScriptAt(candidate)
		if found {
			return candidate, nil
		}
		tried = append(tried, note)
	}
	return "", fmt.Errorf("transcript.py not found; tried %s", strings.Join(tried, "; "))
}

func digestScriptAt(candidate string) (bool, string) {
	info, err := os.Stat(candidate)
	switch {
	case err != nil:
		return false, fmt.Sprintf("%s (%v)", candidate, err)
	case !info.Mode().IsRegular():
		return false, candidate + " (not a regular file)"
	}
	return true, candidate
}

// cutAtLine keeps only complete lines within limit bytes. When the first
// line exceeds the cap, the bounded digest is empty.
func cutAtLine(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if cut := strings.LastIndexByte(text[:limit], '\n'); cut >= 0 {
		return text[:cut+1]
	}
	return ""
}

// cutBytes keeps the first limit bytes of text without splitting a rune.
func cutBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
