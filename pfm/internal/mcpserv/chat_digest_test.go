package mcpserv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// digestSourceRepo points chat_digest at the checkout this test runs in, so
// the script under test is the worktree's own, never the host's ~/.claude.
func digestSourceRepo(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(
			filepath.Join(dir, "templates", "global", "skills", "transcript", "transcript.py"),
		); err == nil {
			t.Setenv("PFM_SOURCE_REPO", dir)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no checkout above the test's directory carries templates/global/skills/transcript/transcript.py")
		}
		dir = parent
	}
}

// digestFixture is a jailed home, the source repo pointed at this checkout and
// one transcript passed by path; it returns the session and that path.
func digestFixture(t *testing.T, service *Service) (*mcp.ClientSession, string) {
	t.Helper()
	root := setupBackendFixture(t)
	digestSourceRepo(t)
	path := filepath.Join(root, "digest-fixture.jsonl")
	writeJSONL(t, path, []any{
		map[string]any{
			"type": "user", "cwd": "/work/digest", "sessionId": "digest-fixture",
			"timestamp": "2026-01-01T10:00:00Z",
			"message":   map[string]any{"content": "digest unique prompt"},
		},
		map[string]any{
			"type": "assistant", "timestamp": "2026-01-01T10:00:05Z",
			"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "digest answer"}}},
		},
	})
	return connectInMemory(t, service.Server()).clientSession, path
}

func callDigestError(t *testing.T, session *mcp.ClientSession, input DigestInput) string {
	t.Helper()
	return callToolErrorWithMeta(t, session, "chat_digest", nil, input)
}

func TestChatDigestReturnsTheEventDigestOfAPath(t *testing.T) {
	service := newService("test", &backend{})
	session, path := digestFixture(t, service)
	got := callTool[DigestOutput](t, session, "chat_digest", DigestInput{Source: path})
	if !strings.HasPrefix(got.Text, "TRANSCRIPT claude · digest-fixture") ||
		!strings.Contains(got.Text, "PROMPT digest unique prompt") ||
		got.Bytes != len(got.Text) || got.TotalBytes != got.Bytes || got.Truncated {
		t.Fatalf("chat_digest = %+v, want the header and the prompt event, whole", got)
	}
}

func TestChatDigestFilterMatchingNothingIsAnAnswerNotAnError(t *testing.T) {
	service := newService("test", &backend{})
	session, path := digestFixture(t, service)
	got := callTool[DigestOutput](t, session, "chat_digest", DigestInput{
		Source: path, Grep: "no-such-word-anywhere", Since: "-15m",
	})
	if !strings.Contains(got.Text, "shown 0 of 2 events") {
		t.Fatalf("chat_digest = %+v, want a FILTER line saying shown 0 of 2 events", got)
	}
}

func TestChatDigestUnknownIdIsAToolErrorNamingNotFound(t *testing.T) {
	service := newService("test", &backend{})
	session, _ := digestFixture(t, service)
	message := callDigestError(t, session, DigestInput{Source: "no-such-session-id"})
	if !strings.Contains(message, "chat_digest: ") || !strings.Contains(message, "NOT FOUND") {
		t.Fatalf("chat_digest unknown id error = %s, want chat_digest: and NOT FOUND", message)
	}
}

func TestChatDigestMaxBytesCutsAtALineEnd(t *testing.T) {
	service := newService("test", &backend{})
	session, path := digestFixture(t, service)
	whole := callTool[DigestOutput](t, session, "chat_digest", DigestInput{Source: path})
	limit := len(whole.Text) - 10
	got := callTool[DigestOutput](t, session, "chat_digest", DigestInput{Source: path, MaxBytes: limit})
	if !got.Truncated || got.Bytes > limit || got.Bytes != len(got.Text) ||
		got.TotalBytes != len(whole.Text) || !strings.HasSuffix(got.Text, "\n") ||
		!strings.HasPrefix(whole.Text, got.Text) {
		t.Fatalf(
			"chat_digest max_bytes %d = %+v of %d bytes, want a line-end prefix within the limit",
			limit,
			got,
			len(whole.Text),
		)
	}
}

func TestChatDigestRejectsBadInputNamingTheField(t *testing.T) {
	service := newService("test", &backend{})
	session, path := digestFixture(t, service)
	for _, test := range []struct {
		field string
		input DigestInput
	}{
		{"source", DigestInput{Source: "-x"}},
		{"source", DigestInput{}},
		{"results", DigestInput{Source: path, Results: "bogus"}},
		{"results", DigestInput{Source: path, Results: "tail:0"}},
		{"grep", DigestInput{Source: path, Grep: "--out=/tmp/x"}},
		{"lines", DigestInput{Source: path, Lines: "-5"}},
		{"only", DigestInput{Source: path, Only: "-x"}},
		{"tool", DigestInput{Source: path, Tool: "-x"}},
		{"last", DigestInput{Source: path, Last: -1}},
		{"first", DigestInput{Source: path, First: -1}},
		{"text", DigestInput{Source: path, Text: -1}},
		{"max_bytes", DigestInput{Source: path, MaxBytes: -1}},
		{"max_bytes", DigestInput{Source: path, MaxBytes: 1<<20 + 1}},
	} {
		message := callDigestError(t, session, test.input)
		if !strings.Contains(message, test.field) {
			t.Fatalf("chat_digest %+v error = %s, want it to name %q", test.input, message, test.field)
		}
	}
}

// A since or until that starts with a dash is a value (`-15m`), passed as one
// --flag=value argv word.
func TestChatDigestAcceptsADashedSinceAndUntil(t *testing.T) {
	service := newService("test", &backend{})
	session, path := digestFixture(t, service)
	got := callTool[DigestOutput](t, session, "chat_digest", DigestInput{Source: path, Since: "-15m", Until: "+1m"})
	if !strings.Contains(got.Text, "since -15m") {
		t.Fatalf("chat_digest = %+v, want the FILTER line to carry since -15m", got)
	}
}

func TestChatDigestNamesBothScriptPathsWhenNeitherExists(t *testing.T) {
	root := setupBackendFixture(t)
	t.Setenv("PFM_SOURCE_REPO", filepath.Join(root, "no-source-repo"))
	service := newService("test", &backend{})
	session := connectInMemory(t, service.Server()).clientSession
	message := callDigestError(t, session, DigestInput{Source: "x"})
	for _, want := range []string{
		filepath.Join(root, "no-source-repo", "templates", "global", "skills", "transcript", "transcript.py"),
		filepath.Join(root, "home", ".claude", "skills", "transcript", "transcript.py"),
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("chat_digest error = %s, want it to list %s", message, want)
		}
	}
}

// blockingRunner never answers until its context ends, as a hung python3 does.
type blockingRunner struct{ deps.Runner }

func (blockingRunner) LookPath(name string) (string, error) { return "/fake/" + name, nil }

func (blockingRunner) Run(ctx context.Context, _ []string, _ deps.RunOptions) (deps.RunResult, error) {
	<-ctx.Done()
	return deps.RunResult{ExitCode: -1}, nil
}

func TestChatDigestTimeoutIsAToolErrorSayingSo(t *testing.T) {
	setupBackendFixture(t)
	digestSourceRepo(t)
	service := newService("test", &backend{runner: blockingRunner{}, digestTimeout: 50 * time.Millisecond})
	session := connectInMemory(t, service.Server()).clientSession
	message := callDigestError(t, session, DigestInput{Source: "x"})
	if !strings.Contains(message, "timed out after") || !strings.Contains(message, "lines") {
		t.Fatalf("chat_digest timeout error = %s, want it to say it timed out and to narrow", message)
	}
}

func TestChatDigestPython3MissingIsAToolErrorSayingSo(t *testing.T) {
	setupBackendFixture(t)
	digestSourceRepo(t)
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath("python3", "", os.ErrNotExist)
	service := newService("test", &backend{runner: runner})
	session := connectInMemory(t, service.Server()).clientSession
	message := callDigestError(t, session, DigestInput{Source: "x"})
	if !strings.Contains(message, "python3") || !strings.Contains(message, "not found") {
		t.Fatalf("chat_digest error = %s, want it to say python3 was not found", message)
	}
}

func TestChatDigestDescriptionsStayWithinTheToolBudget(t *testing.T) {
	service := newService("test", &backend{})
	session := connectInMemory(t, service.Server()).clientSession
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
		// chat_status predates the budget and is over it; this change owns the
		// three descriptions it writes.
		switch tool.Name {
		case "chat_digest", "chat_find", "chat_read":
			if runes := utf8.RuneCountInString(tool.Description); runes > 600 {
				t.Errorf("%s description is %d runes, over 600", tool.Name, runes)
			}
		}
	}
	if !seen["chat_digest"] {
		t.Fatalf("tools/list = %v, want chat_digest listed", sortedRoster(ToolNames()))
	}
	for _, tool := range tools.Tools {
		switch tool.Name {
		case "chat_digest":
			for _, phrase := range []string{`chat_digest{source:`, "tool error", "shown 0 of N", "chat_last"} {
				if !strings.Contains(tool.Description, phrase) {
					t.Errorf("chat_digest description lacks %q: %q", phrase, tool.Description)
				}
			}
			encoded, _ := json.Marshal(tool.InputSchema)
			if !strings.Contains(string(encoded), `"max_bytes"`) || !strings.Contains(string(encoded), `"source"`) {
				t.Errorf("chat_digest schema = %s, want source and max_bytes", encoded)
			}
		case "chat_find":
			if !strings.Contains(tool.Description, "pass an id to chat_digest") {
				t.Errorf("chat_find description = %q, want the hand-off to chat_digest", tool.Description)
			}
		case "chat_read":
			if !strings.HasSuffix(tool.Description, "For a window, a filter or tool results, chat_digest.") {
				t.Errorf("chat_read description = %q, want the chat_digest pointer", tool.Description)
			}
		}
	}
}
