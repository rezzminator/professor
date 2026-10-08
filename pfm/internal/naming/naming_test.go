package naming

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDisplayName(t *testing.T) {
	tests := []struct {
		name        string
		customTitle string
		aiTitle     string
		firstPrompt string
		want        string
	}{
		{
			name:        "manual rename wins after later AI title",
			customTitle: "manual",
			aiTitle:     "later automatic",
			firstPrompt: "raw prompt",
			want:        "manual",
		},
		{
			name:        "AI title is strictly second",
			aiTitle:     "automatic",
			firstPrompt: "raw prompt",
			want:        "automatic",
		},
		{
			name:        "first real prompt is fallback",
			firstPrompt: "raw prompt",
			want:        "raw prompt",
		},
		{
			name: "promptless transcript",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DisplayName(test.customTitle, test.aiTitle, test.firstPrompt)
			if got != test.want {
				t.Fatalf("DisplayName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLiveFallback(t *testing.T) {
	longName := strings.Repeat("x", 100)
	tests := []struct {
		name        string
		indexed     string
		windowName  string
		paneTitle   string
		sessionName string
		lastPrompt  string
		isCCSock    bool
		want        string
	}{
		{
			name:        "indexed transcript name wins",
			indexed:     "indexed",
			paneTitle:   "pane",
			sessionName: "session",
			lastPrompt:  "last",
			isCCSock:    true,
			want:        "indexed",
		},
		{
			name:        "indexed transcript name wins over the launch window name",
			indexed:     "indexed",
			windowName:  "GATEWAY",
			paneTitle:   "✳ GATEWAY",
			sessionName: "cc-1-2-3",
			isCCSock:    true,
			want:        "indexed",
		},
		{
			name:        "Claude socket before its first prompt uses its launch window name",
			windowName:  "GATEWAY",
			paneTitle:   "✳ Claude Code",
			sessionName: "cc-1-2-3",
			isCCSock:    true,
			want:        "GATEWAY",
		},
		{
			name:        "placeholder chat window falls through to the pane title",
			windowName:  "chat",
			paneTitle:   "operator title",
			sessionName: "cc-1-2-3",
			isCCSock:    true,
			want:        "operator title",
		},
		{
			name:        "engine short window falls through to the pane title",
			windowName:  "Claude",
			paneTitle:   "operator title",
			sessionName: "cc-1-2-3",
			isCCSock:    true,
			want:        "operator title",
		},
		{
			name:        "window named after its socket falls through to the last prompt",
			windowName:  "cc-1-2-3",
			sessionName: "cc-1-2-3",
			lastPrompt:  "last real prompt",
			isCCSock:    true,
			want:        "last real prompt",
		},
		{
			name:       "Claude socket uses pane title",
			paneTitle:  "operator title",
			lastPrompt: "last",
			isCCSock:   true,
			want:       "operator title",
		},
		{
			name:        "Claude Code pane title is ignored",
			paneTitle:   "Claude Code",
			sessionName: "cc-123",
			lastPrompt:  "last real prompt",
			isCCSock:    true,
			want:        "last real prompt",
		},
		{
			name:        "empty Claude pane title uses last prompt",
			sessionName: "cc-123",
			lastPrompt:  "last real prompt",
			isCCSock:    true,
			want:        "last real prompt",
		},
		{
			name:        "non-Claude socket uses tmux session name",
			paneTitle:   "incidental pane",
			sessionName: "revived",
			lastPrompt:  "last",
			want:        "revived",
		},
		{
			name:     "unnamed fallback",
			isCCSock: true,
			want:     "(unnamed)",
		},
		{
			name:     "no display clipping",
			indexed:  longName,
			isCCSock: true,
			want:     longName,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := LiveFallback(
				test.indexed,
				test.windowName,
				test.paneTitle,
				test.sessionName,
				test.lastPrompt,
				test.isCCSock,
			)
			if got != test.want {
				t.Fatalf("LiveFallback() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestIsJunkPrompt(t *testing.T) {
	tests := []struct {
		prompt string
		want   bool
	}{
		{prompt: "", want: false},
		{prompt: "<local-command", want: true},
		{prompt: "<system-reminder>", want: true},
		{prompt: "<a", want: true},
		{prompt: "<X", want: false},
		{prompt: "<9", want: false},
		{prompt: "prefix <local-command", want: false},
		{prompt: "Caveat:", want: true},
		{prompt: "Caveat: injected", want: true},
		{prompt: "caveat:", want: false},
		{prompt: " Caveat:", want: false},
		{prompt: "[Request", want: true},
		{prompt: "[Requester", want: true},
		{prompt: "[request", want: false},
		{prompt: "prefix [Request", want: false},
	}

	for _, test := range tests {
		t.Run(test.prompt, func(t *testing.T) {
			if got := IsJunkPrompt(test.prompt); got != test.want {
				t.Fatalf("IsJunkPrompt(%q) = %v, want %v", test.prompt, got, test.want)
			}
		})
	}
}

func TestFlattenPromptText(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "string content",
			content: `"hello"`,
			want:    "hello",
		},
		{
			name:    "string whitespace is flattened",
			content: "\"line one\\n\\tline two\"",
			want:    "line one line two",
		},
		{
			name:    "top-level text blocks join",
			content: `[{"type":"text","text":"one"},{"type":"text","text":"two"}]`,
			want:    "one two",
		},
		{
			name:    "tool results never contribute",
			content: `[{"type":"text","text":"before"},{"type":"tool_result","text":"secret"},{"type":"text","text":"after"}]`,
			want:    "before after",
		},
		{
			name:    "nested text never contributes",
			content: `[{"type":"container","content":[{"type":"text","text":"nested"}]},{"type":"text","text":"top"}]`,
			want:    "top",
		},
		{
			name:    "top-level strings are not text blocks",
			content: `["ignored",{"type":"text","text":"kept"}]`,
			want:    "kept",
		},
		{
			name:    "array whitespace is flattened after joining",
			content: `[{"type":"text","text":"one\n"},{"type":"text","text":"\ttwo"}]`,
			want:    "one   two",
		},
		{
			name:    "empty array",
			content: `[]`,
		},
		{
			name:    "null",
			content: `null`,
		},
		{
			name:    "object is not content",
			content: `{"type":"text","text":"not an array"}`,
		},
		{
			name:    "invalid JSON",
			content: `[{"type":`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := FlattenPromptText(json.RawMessage(test.content))
			if got != test.want {
				t.Fatalf("FlattenPromptText() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCxName(t *testing.T) {
	longASCII := strings.Repeat("a", 61)
	longMultibyte := strings.Repeat("界", 61)
	tests := []struct {
		name             string
		ownID            string
		sessionID        string
		parentThread     string
		names            map[string]string
		firstUserMessage string
		want             string
	}{
		{
			name:             "own thread name wins",
			ownID:            "own",
			sessionID:        "session",
			parentThread:     "parent",
			names:            map[string]string{"own": "own name", "session": "session name", "parent": "parent name"},
			firstUserMessage: "prompt",
			want:             "own name",
		},
		{
			name:             "session lineage fallback",
			ownID:            "own",
			sessionID:        "session",
			parentThread:     "parent",
			names:            map[string]string{"session": "session name", "parent": "parent name"},
			firstUserMessage: "prompt",
			want:             "session name",
		},
		{
			name:             "empty own name falls through",
			ownID:            "own",
			sessionID:        "session",
			names:            map[string]string{"own": "", "session": "session name"},
			firstUserMessage: "prompt",
			want:             "session name",
		},
		{
			name:             "parent lineage fallback",
			ownID:            "own",
			sessionID:        "session",
			parentThread:     "parent",
			names:            map[string]string{"parent": "parent name"},
			firstUserMessage: "prompt",
			want:             "parent name",
		},
		{
			name:             "first user message fallback",
			ownID:            "own",
			sessionID:        "session",
			parentThread:     "parent",
			names:            map[string]string{},
			firstUserMessage: "first\n\tmessage",
			want:             "first message",
		},
		{
			name:  "empty",
			names: nil,
		},
		{
			name:  "ASCII clips to 60 characters",
			ownID: "own",
			names: map[string]string{"own": longASCII},
			want:  strings.Repeat("a", 60),
		},
		{
			name:  "multibyte clips by rune",
			ownID: "own",
			names: map[string]string{"own": longMultibyte},
			want:  strings.Repeat("界", 60),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := CxName(
				test.ownID,
				test.sessionID,
				test.parentThread,
				test.names,
				test.firstUserMessage,
			)
			if got != test.want {
				t.Fatalf("CxName() = %q, want %q", got, test.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("CxName() returned invalid UTF-8: %x", got)
			}
		})
	}
}
