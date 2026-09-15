package transcript

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeLines and codexLines are the record shapes each engine actually
// writes, taken from live files: a Claude registry jsonl and a Codex rollout.
const claudeLines = `{"type":"user","timestamp":"t1","message":{"role":"user","content":"ship the fix"}}
{"type":"user","timestamp":"t2","message":{"role":"user","content":"<system-reminder>ignore me</system-reminder>"}}
{"type":"user","timestamp":"t3","message":{"role":"user","content":"<command-name>/compact</command-name>"}}
{"type":"assistant","timestamp":"t4","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"assistant","timestamp":"t5","message":{"role":"assistant","content":[{"type":"text","text":"tests are green"}]}}
{"type":"assistant","timestamp":"t6","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent chatter"}]}}
{"type":"summary","timestamp":"t7","summary":"we fixed the picker"}
`

const codexLines = `{"timestamp":"t1","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"read the report"}]}}
{"timestamp":"t2","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions instructions>"}]}}
{"timestamp":"t3","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"const r = await tools.exec_command({\"cmd\":\"ls\"})"}}
{"timestamp":"t4","type":"response_item","payload":{"type":"function_call","name":"spawn_agent","arguments":"{\"agent_type\":\"gitter\"}"}}
{"timestamp":"t5","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the report is clean"}]}}
{"timestamp":"t6","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":23515},"model_context_window":272000}}}
`

func writeTranscript(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestParseKeepsWhatWasSaidAndDropsTheRest pins both engines' record shapes at
// once: what a reader wants, and every category of line that must never reach
// one (sidechains, harness reminders, slash-command records, the permissions
// preamble Codex writes as a "developer" turn).
func TestParseKeepsWhatWasSaidAndDropsTheRest(t *testing.T) {
	for _, testCase := range []struct {
		engine  string
		content string
		want    []string
	}{
		{
			engine:  "cc",
			content: claudeLines,
			want: []string{
				"U ship the fix",
				"T Bash|{\"command\":\"go test ./...\"}",
				"A tests are green",
				"S we fixed the picker",
			},
		},
		{
			engine:  "cx",
			content: codexLines,
			want: []string{
				"U read the report",
				`T exec|const r = await tools.exec_command({"cmd":"ls"})`,
				`T spawn_agent|{"agent_type":"gitter"}`,
				"A the report is clean",
			},
		},
	} {
		t.Run(testCase.engine, func(t *testing.T) {
			path := writeTranscript(t, "chat.jsonl", testCase.content)
			entries, truncated, err := Tail(
				context.Background(),
				path,
				testCase.engine,
				100,
				0,
			)
			if err != nil {
				t.Fatalf("Tail() error = %v", err)
			}
			if truncated {
				t.Fatal("a 100-entry window over a 7-line file reported truncation")
			}
			got := make([]string, 0, len(entries))
			for _, entry := range entries {
				got = append(got, Condensed(entry))
			}
			if strings.Join(got, "\n") != strings.Join(testCase.want, "\n") {
				t.Fatalf("entries\n got: %v\nwant: %v", got, testCase.want)
			}
		})
	}
}

func TestTailKeepsTheNewestAndReportsTruncation(t *testing.T) {
	path := writeTranscript(t, "chat.jsonl", claudeLines)
	entries, truncated, err := Tail(context.Background(), path, "cc", 2, 0)
	if err != nil {
		t.Fatalf("Tail() error = %v", err)
	}
	if !truncated {
		t.Fatal("a 2-entry window over 4 entries must report truncation")
	}
	if len(entries) != 2 ||
		Condensed(entries[0]) != "A tests are green" ||
		Condensed(entries[1]) != "S we fixed the picker" {
		t.Fatalf("tail = %#v", entries)
	}
}

func TestLastFindsTheNewestAssistantTurn(t *testing.T) {
	path := writeTranscript(t, "chat.jsonl", claudeLines)
	entries, _, err := Tail(context.Background(), path, "cc", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	entry, found := Last(entries, RoleAssistant)
	if !found || entry.Text != "tests are green" {
		t.Fatalf("last assistant = %#v found=%t", entry, found)
	}
}

func TestLastExchangeIsEngineAgnosticAndKeepsTools(t *testing.T) {
	tests := []struct {
		engine  string
		content string
	}{
		{
			engine: "cc",
			content: `{"type":"user","message":{"role":"user","content":"older question"}}
{"type":"assistant","message":{"role":"assistant","content":"older answer"}}
{"type":"user","message":{"role":"user","content":"latest question"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"prepared.md"}}]}}
{"type":"assistant","message":{"role":"assistant","content":"latest answer"}}
`,
		},
		{
			engine: "cx",
			content: `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"older question"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"older answer"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"latest question"}]}}
{"type":"response_item","payload":{"type":"function_call","name":"read_file","arguments":"{\"path\":\"prepared.md\"}"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"latest answer"}]}}
`,
		},
	}
	for _, test := range tests {
		t.Run(test.engine, func(t *testing.T) {
			entries, err := All(context.Background(), writeTranscript(t, "exchange.jsonl", test.content), test.engine)
			if err != nil {
				t.Fatal(err)
			}
			prompt, response, ok := LastExchange(entries)
			if !ok || len(prompt) != 1 || Condensed(prompt[0]) != "U latest question" {
				t.Fatalf("prompt=%#v ok=%t", prompt, ok)
			}
			if len(response) != 2 || !strings.HasPrefix(Condensed(response[0]), "T ") ||
				Condensed(response[1]) != "A latest answer" {
				t.Fatalf("response=%#v", response)
			}
		})
	}
}

func TestLastExchangeNamesPartialAndMissingShapesWithoutGuessing(t *testing.T) {
	prompt, response, ok := LastExchange([]Entry{
		{Role: RoleAssistant, Text: "orphan answer"},
		{Role: RoleUser, Text: "new question"},
		{Role: RoleTool, Tool: "Bash", Input: "go test ./..."},
	})
	if !ok || len(prompt) != 1 || len(response) != 1 || response[0].Role != RoleTool {
		t.Fatalf("partial prompt=%#v response=%#v ok=%t", prompt, response, ok)
	}
	if prompt, response, ok := LastExchange(
		[]Entry{{Role: RoleAssistant, Text: "orphan"}},
	); ok || prompt != nil ||
		response != nil {
		t.Fatalf("missing prompt=%#v response=%#v ok=%t", prompt, response, ok)
	}
}

func TestReadMetaTakesTheLiveModelAndContext(t *testing.T) {
	codex := writeTranscript(t, "rollout.jsonl", codexLines)
	meta, err := ReadMeta(codex, "cx")
	if err != nil {
		t.Fatalf("ReadMeta() error = %v", err)
	}
	if meta.ContextTokens != 23515 || meta.ContextWindow != 272000 {
		t.Fatalf("codex meta = %#v", meta)
	}
	if percent := meta.ContextPercent(); percent < 8.6 || percent > 8.7 {
		t.Fatalf("context percent = %f, want ~8.64", percent)
	}

	claude := writeTranscript(
		t,
		"claude.jsonl",
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":2,"cache_read_input_tokens":400,"cache_creation_input_tokens":98,"output_tokens":500}}}
{"type":"assistant","message":{"model":"<synthetic>","usage":{}}}
`,
	)
	meta, err = ReadMeta(claude, "cc")
	if err != nil {
		t.Fatalf("ReadMeta() error = %v", err)
	}
	if meta.Model != "claude-opus-5" {
		t.Fatalf("model = %q — <synthetic> is the harness, not a choice", meta.Model)
	}
	if meta.ContextTokens != 1000 {
		t.Fatalf("context tokens = %d, want 1000 (cache reads ARE context)", meta.ContextTokens)
	}
	// No window is stated anywhere in a Claude transcript, so the percentage
	// must stay unknown rather than be guessed from a model name.
	if percent := meta.ContextPercent(); percent != 0 {
		t.Fatalf("context percent = %f, want 0 (unknown)", percent)
	}
}

func TestCodexContextPercentUsesLastWindowNotLifetimeTotal(t *testing.T) {
	path := writeTranscript(
		t,
		"rollout.jsonl",
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":155000},"total_token_usage":{"total_tokens":155000000},"model_context_window":272000}}}`+"\n",
	)
	meta, err := ReadMeta(path, "cx")
	if err != nil {
		t.Fatalf("ReadMeta() error = %v", err)
	}
	if meta.ContextTokens != 155000 {
		t.Fatalf("context tokens = %d, want current-window 155000", meta.ContextTokens)
	}
	if percent := meta.ContextPercent(); percent < 56.9 || percent > 57.0 {
		t.Fatalf("context percent = %f, want ~56.99", percent)
	}
	if percent := (Meta{ContextTokens: 155000000, ContextWindow: 272000}).ContextPercent(); percent != 100 {
		t.Fatalf("defensive context-percent cap = %f, want 100", percent)
	}
}
