package mcpserv

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// digestFixture is a jailed home and one transcript passed by path; it returns
// the session and that path.
func digestFixture(t *testing.T, service *Service) (*mcp.ClientSession, string) {
	t.Helper()
	root := setupBackendFixture(t)
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
		{"source", DigestInput{}},
		{"results", DigestInput{Source: path, Results: "bogus"}},
		{"results", DigestInput{Source: path, Results: "tail:0"}},
		{"lines", DigestInput{Source: path, Lines: "-5"}},
		{"only", DigestInput{Source: path, Only: "-x"}},
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

// A digest that outlives its deadline stops reading and answers the timeout.
func TestChatDigestTimeoutIsAToolErrorSayingSo(t *testing.T) {
	service := newService("test", &backend{digestTimeout: time.Nanosecond})
	session, path := digestFixture(t, service)
	message := callDigestError(t, session, DigestInput{Source: path})
	if !strings.Contains(message, "timed out after") || !strings.Contains(message, "lines") {
		t.Fatalf("chat_digest timeout error = %s, want it to say it timed out and to narrow", message)
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

func TestChatDigestMaxBytesBeforeFirstLine(t *testing.T) {
	service := newService("test", &backend{})
	session, path := digestFixture(t, service)
	whole := callTool[DigestOutput](t, session, "chat_digest", DigestInput{Source: path})
	firstEnd := strings.IndexByte(whole.Text, '\n') + 1
	for _, limit := range []int{1, firstEnd - 1, firstEnd} {
		got := callTool[DigestOutput](t, session, "chat_digest", DigestInput{Source: path, MaxBytes: limit})
		want := ""
		if limit == firstEnd {
			want = whole.Text[:firstEnd]
		}
		if got.Text != want || got.Bytes != len(want) || !got.Truncated || got.TotalBytes != len(whole.Text) {
			t.Errorf("max_bytes=%d: got %+v, want complete lines %q and total=%d", limit, got, want, len(whole.Text))
		}
	}
}

// An id resolves under the backend's own Claude roots, in-process.
func TestChatDigestResolvesAnIdUnderTheBackendRoots(t *testing.T) {
	root := setupBackendFixture(t)
	writeJSONL(t, filepath.Join(root, "claude", "project-alpha", "digest-resolve-0001.jsonl"), []any{
		map[string]any{
			"type": "user", "cwd": "/work/alpha", "sessionId": "digest-resolve-0001",
			"timestamp": "2026-01-01T10:00:00Z", "message": map[string]any{"content": "resolved by id"},
		},
	})
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	service := newService("test", &backend{paths: resolved})
	session := connectInMemory(t, service.Server()).clientSession
	got := callTool[DigestOutput](t, session, "chat_digest", DigestInput{Source: "digest-resolve"})
	if !strings.Contains(got.Text, "PROMPT resolved by id") {
		t.Fatalf("chat_digest by id prefix = %+v, want the transcript under the backend's Claude root", got)
	}
}
