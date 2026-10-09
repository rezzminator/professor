package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/transcript/digest"
)

const (
	// digestRunTimeout bounds one digest run.
	digestRunTimeout   = 60 * time.Second
	digestDefaultBytes = 64 << 10
	digestMaxBytes     = 1 << 20
)

var digestResultsForm = regexp.MustCompile(`^(brief|none|full|tail:[1-9]\d*)$`)

// chatDigest is chat_digest: `pfm transcript show` over any Claude, Codex or
// OpenCode transcript, run in-process, its digest bounded to max_bytes at a line end.
func (service *Service) chatDigest(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input DigestInput,
) (*mcp.CallToolResult, DigestOutput, error) {
	flags, maxBytes, err := digestArgs(input)
	if err != nil {
		return nil, DigestOutput{}, fmt.Errorf("chat_digest: %w", err)
	}
	timeout := service.backend.digestTimeout
	if timeout <= 0 {
		timeout = digestRunTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append([]string{"show"}, flags...)
	args = append(args, "--", input.Source)
	var stdout, stderr strings.Builder
	code := digest.Invoke(runCtx, args, &stdout, &stderr, digest.EnvOf(service.backend.paths))
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
	case code != 0:
		return nil, DigestOutput{}, fmt.Errorf("chat_digest: %s", strings.TrimSpace(stderr.String()))
	}
	text := stdout.String()
	output := DigestOutput{TotalBytes: len(text)}
	if len(text) > maxBytes {
		text = cutAtLine(text, maxBytes)
		output.Truncated = true
	}
	output.Text, output.Bytes = text, len(text)
	return nil, output, nil
}

// digestArgs validates the input at entry and returns the `show` flags, each
// one argument `--flag=value` so a value that starts with a dash (since
// "-15m") is never read as an option, and the byte bound.
func digestArgs(input DigestInput) ([]string, int, error) {
	if strings.TrimSpace(input.Source) == "" {
		return nil, 0, fmt.Errorf("source is required")
	}
	var flags []string
	for _, field := range []struct{ flag, value string }{
		{"--lines", input.Lines},
		{"--grep", input.Grep},
		{"--only", input.Only},
		{"--tool", input.Tool},
		{"--results", input.Results},
		{"--since", input.Since},
		{"--until", input.Until},
	} {
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
