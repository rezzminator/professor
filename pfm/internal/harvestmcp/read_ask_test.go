package harvestmcp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// askFixturePage is the cached page every ask case reads.
const askFixturePage = "https://example.test/ask"

var askSizeLine = regexp.MustCompile(`size: \d+ chars, ~\d+ tokens`)

// TestReadAskAnswersOverTheItems: harvester_read{ask} asks one question over
// the call's items through a fake Claude binary — defaults claude + haiku,
// the answer after the item blocks, which carry their size unless
// include_content is true. engine or model without ask, an unknown engine, an
// engine with no ask runner, a model its engine cannot run, an engine with no
// account, a server with no pfm config and the remote gateway are refused by
// name before any engine starts; a failed engine and a call whose every item
// failed read as an error under the answer header.
func TestReadAskAnswersOverTheItems(t *testing.T) {
	content := "# Ask Page\n\n" + strings.TrimRight(strings.Repeat("Words about Ask Page. ", 40), " ")
	reply := "printf '%s\\n' '{\"result\":\"fixture answer\"}'\n"
	failing := "printf 'fixture failure\\n' >&2\nexit 7\n"
	for _, testCase := range []struct {
		name      string
		args      map[string]any
		engine    string // the fake engine's reply script
		noAccount bool
		noMachine bool
		remote    bool
		want      string // {path} and {size} stand for the artifact path and its size line
		prefix    bool   // want is the answer's opening only
		isError   bool
		wantArgs  string   // "" = the engine must not run
		receipts  []string // what the engine reads of the failed items' receipts
	}{
		{
			name: "defaults claude haiku", args: map[string]any{"urls": []string{askFixturePage}, "ask": "What does the page say?"},
			engine:   reply,
			want:     "=== [1/1] " + askFixturePage + "\n{path}\n{size}\n\n=== answer (claude haiku)\nfixture answer",
			wantArgs: "--model|haiku|",
		},
		{
			name:     "explicit model",
			args:     map[string]any{"urls": []string{askFixturePage}, "ask": "What does the page say?", "engine": "claude", "model": " Sonnet"},
			engine:   reply,
			want:     "=== [1/1] " + askFixturePage + "\n{path}\n{size}\n\n=== answer (claude sonnet)\nfixture answer",
			wantArgs: "--model|sonnet|",
		},
		{
			name:     "include content",
			args:     map[string]any{"urls": []string{askFixturePage}, "ask": "What does the page say?", "include_content": true},
			engine:   reply,
			want:     "=== [1/1] " + askFixturePage + "\n{path}\n" + content + "\n\n=== answer (claude haiku)\nfixture answer",
			wantArgs: "--model|haiku|",
		},
		{
			name: "every item failed", args: map[string]any{"urls": []string{"10.1234/ask"}, "ask": "What?"},
			engine:  reply,
			want:    "=== [1/1] 10.1234/ask\nerror: this is a DOI; put it in publications.\n\n=== answer (claude haiku)\nerror: not asked: no item was read",
			isError: true,
		},
		{
			name:   "one item read, one failed",
			args:   map[string]any{"urls": []string{askFixturePage, "10.1234/ask"}, "ask": "What does the page say?"},
			engine: reply,
			want: "=== [1/2] " + askFixturePage + "\n{path}\n{size}\n\n=== [2/2] 10.1234/ask\n" +
				"error: this is a DOI; put it in publications.\n\n=== answer (claude haiku)\nfixture answer",
			wantArgs: "--model|haiku|",
			receipts: []string{"/source-002.json — source: 10.1234/ask", `"input": "10.1234/ask"`, `"error_kind": "invalid"`},
		},
		{
			name: "engine failed", args: map[string]any{"urls": []string{askFixturePage}, "ask": "What?"},
			engine:   failing,
			want:     "=== [1/1] " + askFixturePage + "\n{path}\n{size}\n\n=== answer (claude haiku)\nerror: claude ask failed: ",
			prefix:   true,
			isError:  true,
			wantArgs: "--model|haiku|",
		},
		{
			name: "engine without ask", args: map[string]any{"urls": []string{askFixturePage}, "engine": "claude"},
			engine: reply, want: "error: engine and model require ask: they choose what answers it", isError: true,
		},
		{
			name: "model without ask", args: map[string]any{"urls": []string{askFixturePage}, "model": "haiku"},
			engine: reply, want: "error: engine and model require ask: they choose what answers it", isError: true,
		},
		{
			name: "unknown engine", args: map[string]any{"urls": []string{askFixturePage}, "ask": "What?", "engine": "gemini"},
			engine: reply, want: `error: engine: unknown engine "gemini" (want cc/claude, cx/codex, ox/opencode)`, isError: true,
		},
		{
			name: "engine with no ask runner", args: map[string]any{"urls": []string{askFixturePage}, "ask": "What?", "engine": "opencode"},
			engine: reply, want: "error: engine opencode: OpenCode does not support ask", isError: true,
		},
		{
			name: "unknown model", args: map[string]any{"urls": []string{askFixturePage}, "ask": "What?", "model": "gpt-5"},
			engine:  reply,
			want:    `error: model "gpt-5" is not a claude model: name an alias (fable, opus, sonnet, haiku) or a model id the price table lists`,
			isError: true,
		},
		{
			name: "engine with no account", args: map[string]any{"urls": []string{askFixturePage}, "ask": "What?"},
			engine: reply, noAccount: true, want: "error: engine claude: no Claude accounts configured", isError: true,
		},
		{
			name: "server without pfm config", args: map[string]any{"urls": []string{askFixturePage}, "ask": "What?"},
			engine: reply, noMachine: true,
			want:    "error: ask is not available on this harvester: it was started without the pfm config an engine runs under",
			isError: true,
		},
		{
			name: "remote gateway", args: map[string]any{"urls": []string{askFixturePage}, "ask": "What?"},
			engine: reply, remote: true,
			want:    "error: ask is not available on the remote server: read the items here and ask your own model",
			isError: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			cacheDir := filepath.Join(home, "cache")
			seedPage(t, cacheDir, askFixturePage, "Ask Page")
			accountHome := filepath.Join(home, "account")
			if err := os.MkdirAll(accountHome, 0o700); err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(home, "engine-capture.txt")
			binary := filepath.Join(home, "claude-fixture")
			script := "#!/bin/sh\nprintf '%s|' \"$@\" > \"$PFM_ASK_CAPTURE\"\nprintf '\\n' >> \"$PFM_ASK_CAPTURE\"\n" +
				"cat >> \"$PFM_ASK_CAPTURE\"\n" +
				"cat " + filepath.Join(home, ".local", "state", "pfm", "harvest-ask") + "/run-*/source-*.json " +
				">> \"$PFM_ASK_CAPTURE\" 2>/dev/null\n" + testCase.engine
			if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PFM_ASK_CAPTURE", capture)
			machine := &config.Config{Claude: config.Claude{Binary: binary}}
			if !testCase.noAccount {
				machine.Accounts = []config.Account{{ID: 1, ConfigDir: accountHome}}
			}
			if testCase.noMachine {
				machine = nil
			}
			runtime := Runtime{Home: home, CacheDir: cacheDir, Machine: machine, Remote: testCase.remote}
			session := connectHarvesterInProcess(t, newTestService(t, runtime))
			result := callRaw(t, session, toolRead, testCase.args)
			text := allText(result)
			artifact := ""
			if lines := strings.SplitN(text, "\n", 3); len(lines) > 1 && strings.HasPrefix(lines[1], "/") {
				artifact = lines[1]
				text = strings.Replace(text, artifact, "{path}", 1)
			}
			text = askSizeLine.ReplaceAllString(text, "{size}")
			if matched := text == testCase.want ||
				testCase.prefix && strings.HasPrefix(text, testCase.want); !matched ||
				result.IsError != testCase.isError {
				t.Fatalf(
					"read = isError %v\n%s\nwant isError %v\n%s",
					result.IsError,
					text,
					testCase.isError,
					testCase.want,
				)
			}
			captured, err := os.ReadFile(capture)
			if testCase.wantArgs == "" {
				if err == nil {
					t.Fatalf("the engine ran, want no engine start: %s", captured)
				}
				return
			}
			if err != nil {
				t.Fatalf("the engine never ran: %v", err)
			}
			wants := append([]string{
				testCase.wantArgs, "1. " + artifact + " — source: " + askFixturePage,
				"TASK: " + testCase.args["ask"].(string),
			}, testCase.receipts...)
			for _, want := range wants {
				if !strings.Contains(string(captured), want) {
					t.Errorf("engine capture lacks %q:\n%s", want, captured)
				}
			}
		})
	}
}

// TestAskReceiptDoesNotExposePrivateHarvestDetails: a failed item's receipt is
// its public failure — no private path, provider URL, method or rungs.
func TestAskReceiptDoesNotExposePrivateHarvestDetails(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	home := t.TempDir()
	receiptDir := filepath.Join(home, "receipts")
	if err := os.MkdirAll(receiptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path, _, err := writeAskReceipt(home, receiptDir, 0, "10.1234/public.boundary", harvest.Result{
		Source:    "10.1234/public.boundary",
		Path:      "/private/cache/html/document.md",
		Method:    "doi-mirror",
		Rungs:     []string{"direct", "mirror:https://mirror.secret.example"},
		Error:     "GET https://mirror.secret.example/private: provider internals",
		ErrorKind: "connect",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"input": "10.1234/public.boundary"`) ||
		strings.Contains(text, "mirror.secret.example") ||
		strings.Contains(text, "/private/cache/") ||
		strings.Contains(text, `"method"`) ||
		strings.Contains(text, `"rungs"`) {
		t.Fatalf("ask receipt leaked private harvest details: %s", text)
	}
}
