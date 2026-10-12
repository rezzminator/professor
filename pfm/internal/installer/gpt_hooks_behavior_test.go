package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

type gptHookResult struct {
	decision string
	stdout   string
	stderr   string
}

func runGptHookProcess(t *testing.T, name, role, root, raw string, env ...string) gptHookResult {
	t.Helper()
	content, err := readAsset(name)
	if err != nil {
		t.Fatalf("shipped hook %s missing: %v", name, err)
	}
	script := filepath.Join(root, name)
	writeFixture(t, script, string(content))
	if name == "gpt-agent-stop.py" {
		shared, err := readAsset("gpt-return-watch.py")
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(root, "gpt-return-watch.py"), string(shared))
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("TOOLCHAIN-MISSING python3: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"-I", script}
	if role != "" {
		args = append(args, role)
	}
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Env = append(append(os.Environ(), "HOME="+root, paths.EnvHome+"="+root), env...)
	cmd.Stdin = strings.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s did not fail open: %v stderr=%s", name, err, stderr.String())
	}
	return gptHookResult{stdout: strings.TrimSpace(stdout.String()), stderr: stderr.String()}
}

func runGptHook(t *testing.T, name, role, root, raw string) gptHookResult {
	t.Helper()
	got := runGptHookProcess(t, name, role, root, raw)
	log := "return-watch.jsonl"
	if role != "" {
		log = role + ".jsonl"
	}
	logPath := filepath.Join(root, ".local", "state", "pfm", strings.TrimSuffix(name, ".py"), log)
	lines := strings.Split(strings.TrimSpace(readFixture(t, logPath)), "\n")
	var entry struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatal(err)
	}
	got.decision = entry.Decision
	return got
}

func gptHookJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestGptReturnWatchBehavior(t *testing.T) {
	path := "fixture/flights/orchard/harvest-run/returns/compute.md"
	watch := func(reader, target string) string {
		return fmt.Sprintf("timeout 720 bash -c 'until [ -s \"$1\" ]; do sleep 7; done; %s' _ %s", reader, target)
	}
	for _, tc := range []struct{ name, command, tool, raw, decision string }{
		{"python", watch(`python3 -c "print('ready')"`, path), "Bash", "", "rewrite"},
		{"grep", watch(`grep -n "^" "$1"`, path), "Bash", "", "rewrite"},
		{"printf", watch(`printf "ready"`, path), "Bash", "", "rewrite"},
		{"test", watch(`test -s "$1"`, path), "Bash", "", "rewrite"},
		{"quoted", watch(`head -n 1 "$1"`, `"`+path+`"`), "Bash", "", "rewrite"},
		{"single-quoted", watch(`head -n 1 "$1"`, `'`+path+`'`), "Bash", "", "rewrite"},
		{"semicolon-path", watch(`head "$1"`, path+`;printf${IFS}unapproved`), "Bash", "", "pass"},
		{"substitution-path", watch(`head "$1"`, path+`$(id)`), "Bash", "", "pass"},
		{"quoted-substitution", watch(`head "$1"`, `"`+path+`$(id)"`), "Bash", "", "pass"},
		{"glob-path", watch(`head "$1"`, path+`*`), "Bash", "", "pass"},
		{"traversal", watch(`head "$1"`, path+`/../../../../secret`), "Bash", "", "pass"},
		{"canonical", watch(`cat "$1"`, path), "Bash", "", "canonical"},
		{"multi-file", watch(`head "$1"`, path+" "+path), "Bash", "", "pass"},
		{"non-return", watch(`head "$1"`, "fixture/notes/compute.md"), "Bash", "", "pass"},
		{"non-watch", "printf ready", "Bash", "", "pass"},
		{"other-tool", watch(`head "$1"`, path), "Read", "", "pass"},
		{"malformed-json", "", "", "not json", "error"},
		{"malformed-input", "", "", `{"tool_name":"Bash","tool_input":[]}`, "error"},
		{"malformed-payload", "", "", `[]`, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{
				"command":           tc.command,
				"run_in_background": true,
				"timeout":           42000,
				"description":       "Read a child return",
			}
			raw := tc.raw
			if raw == "" {
				raw = gptHookJSON(
					t,
					map[string]any{"tool_name": tc.tool, "tool_input": input, "agent_id": "fixture-agent"},
				)
			}
			got := runGptHook(t, "gpt-return-watch.py", "", t.TempDir(), raw)
			if got.decision != tc.decision {
				t.Fatalf("decision=%s want=%s", got.decision, tc.decision)
			}
			if tc.decision == "rewrite" {
				var out struct {
					HookSpecificOutput struct {
						HookEventName, PermissionDecision string
						UpdatedInput                      map[string]any
					}
				}
				if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
					t.Fatal(err)
				}
				hook := out.HookSpecificOutput
				target := path
				if tc.name == "quoted" {
					target = `"` + path + `"`
				}
				if tc.name == "single-quoted" {
					target = `'` + path + `'`
				}
				if hook.HookEventName != "PreToolUse" || hook.PermissionDecision != "allow" ||
					hook.UpdatedInput["command"] != watch(`cat "$1"`, target) ||
					hook.UpdatedInput["timeout"] != float64(42000) ||
					hook.UpdatedInput["run_in_background"] != true ||
					hook.UpdatedInput["description"] != input["description"] {
					t.Fatalf("rewritten payload=%s", got.stdout)
				}
			} else if got.stdout != "" {
				t.Fatalf("pass path emitted a rewrite: %s", got.stdout)
			}
			if tc.decision == "error" && !strings.Contains(got.stderr, "gpt-return-watch:") {
				t.Fatal("malformed input was not visible on stderr")
			}
		})
	}
}

func gptTranscriptRow(kind string, content any) map[string]any {
	return map[string]any{"type": kind, "message": map[string]any{"content": content}}
}

func gptStopPayload(t *testing.T, root, message string, extra ...map[string]any) string {
	t.Helper()
	brief := "CHILD harvest-run · unit compute\nFlight directory: fixture/flights/orchard/harvest-run/"
	rows := []map[string]any{gptTranscriptRow("user", brief)}
	rows = append(rows, extra...)
	rows = append(rows, gptTranscriptRow("assistant", []map[string]any{{"type": "text", "text": message}}))
	var lines []string
	for _, row := range rows {
		lines = append(lines, gptHookJSON(t, row))
	}
	transcript := filepath.Join(root, "agent.jsonl")
	writeFixture(t, transcript, strings.Join(lines, "\n")+"\n")
	return gptHookJSON(
		t,
		map[string]any{
			"agent_id":               "fixture-agent",
			"agent_transcript_path":  transcript,
			"last_assistant_message": message,
			"hook_event_name":        "SubagentStop",
			"stop_hook_active":       false,
		},
	)
}

func TestGptAgentStopForemanBehavior(t *testing.T) {
	launch := gptTranscriptRow(
		"user",
		[]map[string]any{
			{
				"type":        "tool_result",
				"tool_use_id": "launch-1",
				"content":     "Async agent launched successfully.\nagentId: fixturechild",
			},
		},
	)
	agentCall := gptTranscriptRow(
		"assistant",
		[]map[string]any{{"type": "tool_use", "id": "launch-1", "name": "Agent", "input": map[string]any{}}},
	)
	commandCall := gptTranscriptRow(
		"assistant",
		[]map[string]any{
			{"type": "tool_use", "id": "command-1", "name": "Bash", "input": map[string]any{"run_in_background": true}},
		},
	)
	command := gptTranscriptRow(
		"user",
		[]map[string]any{
			{
				"type":        "tool_result",
				"tool_use_id": "command-1",
				"content":     "Command running in background with ID: fixturecommand",
			},
		},
	)
	notice := map[string]any{
		"type":       "attachment",
		"attachment": map[string]any{"content": "<task-id>fixturechild</task-id><status>completed</status>"},
	}
	for _, tc := range []struct {
		name, message, decision string
		extra                   []map[string]any
	}{
		{"done", "DONE harvest-run\nGOAL built", "allow-return", nil},
		{"child-unit", "DONE compute", "allow-return", nil},
		{"partial", "PARTIAL harvest-run: checks pending", "allow-return", nil},
		{"failed", "FAILED harvest-run: gate red", "allow-return", nil},
		{"blocked", "BLOCKED harvest-run: missing contract", "allow-return", nil},
		{"bare", "DONE", "block", nil},
		{"glued-prose", "DONE harvest-run. All green.", "block", nil},
		{"prose", "Everything is ready.", "block", nil},
		{"markup", "**DONE harvest-run**", "block", nil},
		{"unknown-name", "DONE other-flight", "block", nil},
		{"brief-keyword", "DONE CHILD", "block", nil},
		{"brief-path-unit", "DONE compute", "allow-return", nil},
		{"brief-path-flight", "DONE harvest-run", "allow-return", nil},
		{"requirements-path", "DONE harvest-run", "allow-return", nil},
		{"brief-label", "DONE unit", "block", nil},
		{"quoted-launch", "All done.", "block", []map[string]any{gptTranscriptRow("assistant", "Example: Async agent launched successfully.\nagentId: examplechild")}},
		{"unmatched-result", "All done.", "block", []map[string]any{launch}},

		{"agent-wait", "Waiting for child", "allow-waiting", []map[string]any{agentCall, launch}},
		{"command-wait", "Waiting for command", "allow-waiting", []map[string]any{commandCall, command}},
		{"completed-child", "All done.", "block", []map[string]any{agentCall, launch, notice}},
		{"compaction", "<compact-now>checkpoint</compact-now>", "allow-compact", nil},
		{"handoff", "<handoff>units a-b done; next: unit c</handoff>", "allow-handoff", nil},
		{"handoff-after-prose", "Next: unit c.\n<handoff>units a-b done; next: unit c</handoff>", "allow-handoff", nil},
		{"handoff-unclosed", "<handoff>units a-b done; next: unit c", "block", nil},
		{"handoff-backticked", "`<handoff>units a-b done; next: unit c</handoff>`", "block", nil},
		{"handoff-empty", "<handoff> \n</handoff>", "block", nil},
		{"handoff-quoted", "All units built; the plugin reads a <handoff> marker only when one is due.", "block", nil},
		{"compaction-unclosed", "<compact-now>checkpoint", "block", nil},
		{"compaction-backticked", "`<compact-now>checkpoint</compact-now>`", "block", nil},
		{"compaction-quoted", "All units built; no <compact-now> marker was needed.", "block", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			raw := gptStopPayload(t, root, tc.message, tc.extra...)
			if strings.HasPrefix(tc.name, "brief-path") || tc.name == "requirements-path" {
				brief := "fixture/flights/orchard/harvest-run/briefs/compute-r1.md"
				if tc.name == "requirements-path" {
					brief = "fixture/flights/orchard/harvest-run/requirements.md"
				}
				writeFixture(
					t,
					filepath.Join(root, "agent.jsonl"),
					gptHookJSON(t, gptTranscriptRow("user", brief))+"\n",
				)
			}
			got := runGptHook(t, "gpt-agent-stop.py", "foreman", root, raw)
			if got.decision != tc.decision {
				t.Fatalf("decision=%s want=%s stdout=%s stderr=%s", got.decision, tc.decision, got.stdout, got.stderr)
			}
			if tc.decision == "block" {
				var out map[string]string
				if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
					t.Fatal(err)
				}
				if out["decision"] != "block" || !strings.Contains(out["reason"], "first line") ||
					!strings.Contains(out["reason"], "DONE harvest-run") {
					t.Fatalf("missing exact repair: %s", got.stdout)
				}
			} else if got.stdout != "" {
				t.Fatalf("allowed stop emitted %s", got.stdout)
			}
		})
	}
}

func TestGptAgentStopCapsAndFailOpen(t *testing.T) {
	t.Run("fleet-home", func(t *testing.T) {
		for _, hook := range []struct{ name, role, log string }{
			{"gpt-agent-stop.py", "foreman", "foreman.jsonl"},
			{"gpt-return-watch.py", "", "return-watch.jsonl"},
		} {
			root, fleet := t.TempDir(), t.TempDir()
			runGptHookProcess(t, hook.name, hook.role, root, `{}`, paths.EnvHome+"="+fleet)
			log := filepath.Join(fleet, ".local", "state", "pfm", strings.TrimSuffix(hook.name, ".py"), hook.log)
			if _, err := os.ReadFile(log); err != nil {
				t.Errorf("%s did not resolve fleet home: %v", hook.name, err)
			}
		}
	})
	t.Run("unwritable-audit", func(t *testing.T) {
		root := t.TempDir()
		for range 3 {
			got := runGptHookProcess(
				t,
				"gpt-agent-stop.py",
				"foreman",
				root,
				gptStopPayload(t, root, "DONE"),
				paths.EnvHome+"=/proc/pfm-gpt-stop-unwritable",
			)
			if got.stdout != "" || !strings.Contains(got.stderr, "gpt-agent-stop:") {
				t.Fatalf("audit failure trapped agent: %+v", got)
			}
		}
	})
	t.Run("bounded-counter", func(t *testing.T) {
		root := t.TempDir()
		logDir := filepath.Join(root, ".local", "state", "pfm", "gpt-agent-stop")
		// A retained audit entry from another process is not the counter's state.
		writeFixture(t, filepath.Join(logDir, "foreman.jsonl"), "not json\n")
		got := runGptHook(t, "gpt-agent-stop.py", "foreman", root, gptStopPayload(t, root, "DONE"))
		if got.decision != "block" || got.stderr != "" {
			t.Fatalf("refusal rescanned unrelated audit history: %+v", got)
		}
	})
	root := t.TempDir()
	for _, want := range []string{"block", "block", "allow-cap"} {
		got := runGptHook(t, "gpt-agent-stop.py", "foreman", root, gptStopPayload(t, root, "DONE"))
		if got.decision != want {
			t.Fatalf("cap decision=%s want=%s", got.decision, want)
		}
		if want == "allow-cap" && !strings.Contains(got.stderr, "stop allowed") {
			t.Fatal("cap was not visible")
		}
	}
	for _, tc := range []struct{ name, role, raw, decision string }{
		{"invalid-json", "foreman", "not json", "allow-error"},
		{"invalid-payload", "foreman", `[]`, "allow-error"},
		{"invalid-role", "unknown", `{}`, "allow-error"},
		{"no-transcript", "foreman", `{}`, "allow-nocard"},
		{"unreadable-transcript", "foreman", `{"agent_transcript_path":"missing-fixture.jsonl"}`, "allow-error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runGptHook(t, "gpt-agent-stop.py", tc.role, t.TempDir(), tc.raw)
			if got.decision != tc.decision || got.stdout != "" {
				t.Fatalf("fail-open result=%+v", got)
			}
			if tc.decision == "allow-error" && !strings.Contains(got.stderr, "gpt-agent-stop:") {
				t.Fatal("error was not visible")
			}
		})
	}
}

func TestGptAgentStopTracerBehavior(t *testing.T) {
	for _, tc := range []struct {
		name            string
		started, stored bool
		decisions       []string
	}{
		{"pending", true, false, []string{"block", "block", "block", "allow-cap"}},
		{"stored", true, true, []string{"allow-stored"}},
		{"empty-stored", true, false, []string{"block"}},
		{"directory-stored", true, false, []string{"block"}},

		{"unstarted", false, false, []string{"allow-unstarted"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			run := filepath.Join(root, "trace")
			if tc.started {
				writeFixture(t, filepath.Join(run, "2.started"), "")
			}
			if tc.stored {
				writeFixture(t, filepath.Join(run, "2.hops"), "stored")
			}
			if tc.name == "empty-stored" {
				writeFixture(t, filepath.Join(run, "2.hops"), "")
			}
			if tc.name == "directory-stored" {
				if err := os.MkdirAll(filepath.Join(run, "2.hops"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			transcript := filepath.Join(root, "agent.jsonl")
			writeFixture(
				t,
				transcript,
				gptHookJSON(t, gptTranscriptRow("user", "RUN: "+run+"\nID: 2\nQUESTION: fixture"))+"\n",
			)
			raw := gptHookJSON(t, map[string]any{"agent_id": "fixture-tracer", "agent_transcript_path": transcript})
			for _, want := range tc.decisions {
				got := runGptHook(t, "gpt-agent-stop.py", "sub-tracer", root, raw)
				if got.decision != want {
					t.Fatalf("decision=%s want=%s", got.decision, want)
				}
				if want == "block" && !strings.Contains(got.stdout, "tracetree.py write") {
					t.Fatalf("missing store repair: %s", got.stdout)
				}
			}
		})
	}
}

func TestGptReturnWatchPreservesSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	returns := filepath.Join(root, "flight", "returns")
	writeFixture(t, filepath.Join(root, "secret"), "private")
	if err := os.MkdirAll(returns, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(returns, "result.md")
	if err := os.Symlink(filepath.Join(root, "secret"), target); err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf(`timeout 5 bash -c 'until [ -s "$1" ]; do sleep 1; done; head "$1"' _ %s`, target)
	got := runGptHook(
		t,
		"gpt-return-watch.py",
		"",
		root,
		gptHookJSON(t, map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}}),
	)
	if got.decision != "pass" || got.stdout != "" {
		t.Fatalf("symlink escape autoallowed: %+v", got)
	}
}

func TestGptAgentStopMissingIDUsesTranscriptIdentity(t *testing.T) {
	root := t.TempDir()
	for agent := range 3 {
		raw := gptStopPayload(t, root, "DONE")
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatal(err)
		}
		delete(payload, "agent_id")
		transcript := filepath.Join(root, fmt.Sprintf("agent-%d.jsonl", agent))
		if err := os.Rename(filepath.Join(root, "agent.jsonl"), transcript); err != nil {
			t.Fatal(err)
		}
		payload["agent_transcript_path"] = transcript
		for _, want := range []string{"block", "block", "allow-cap"} {
			got := runGptHook(t, "gpt-agent-stop.py", "foreman", root, gptHookJSON(t, payload))
			if got.decision != want {
				t.Fatalf("agent %d decision %s, want independent %s", agent, got.decision, want)
			}
		}
	}
}

func TestGptHookStateRetentionIsBounded(t *testing.T) {
	for _, hook := range []struct{ name, role, log string }{
		{"gpt-return-watch.py", "", "return-watch.jsonl"}, {"gpt-agent-stop.py", "foreman", "foreman.jsonl"},
	} {
		t.Run(hook.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, ".local", "state", "pfm", strings.TrimSuffix(hook.name, ".py"))
			log := filepath.Join(dir, hook.log)
			writeFixture(t, log, strings.Repeat("x", 262144))
			raw := `{}`
			if hook.role != "" {
				raw = gptStopPayload(t, root, "DONE")
				for count := range 1025 {
					writeFixture(t, filepath.Join(dir, "counts", fmt.Sprintf("%064x", count)), "1\n")
				}
				old := filepath.Join(dir, "counts", fmt.Sprintf("%064x", 1026))
				writeFixture(t, old, "1\n")
				at := time.Now().Add(-8 * 24 * time.Hour)
				if err := os.Chtimes(old, at, at); err != nil {
					t.Fatal(err)
				}
			}
			runGptHookProcess(t, hook.name, hook.role, root, raw)
			info, err := os.Stat(log)
			if err != nil || info.Size() > 262144 {
				t.Errorf("audit not bounded: info=%v error=%v", info, err)
			}
			if _, err := os.Stat(log + ".1"); err != nil {
				t.Errorf("previous audit not retained: %v", err)
			}
			if hook.role != "" {
				entries, err := os.ReadDir(filepath.Join(dir, "counts"))
				if err != nil || len(entries) > 1024 {
					t.Errorf("counts unbounded: %d, %v", len(entries), err)
				}
			}
		})
	}
}
