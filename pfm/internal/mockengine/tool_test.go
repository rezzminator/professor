package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellToolRunsUnderEngineIdentity(t *testing.T) {
	for _, row := range []struct{ engine, tool, key string }{{"claude", "Bash", "command"}, {"codex", "exec_command", "cmd"}, {"opencode", "bash", "command"}} {
		t.Run(row.engine, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			t.Setenv("MOCK_SHELL_VALUE", "atlas")
			command := `printf '%s\n%s\n%s\n' "$PPID" "$PWD" "$MOCK_SHELL_VALUE" > shell-receipt; `
			if row.engine == engineCodex {
				command += `printf '%s\n' "$CODEX_TOOL_CALL_ID" "$NO_COLOR" "$PAGER" "$GIT_PAGER" "$GH_PAGER" "$CODEX_CI" "$CODEX_SESSION_ID" "$CODEX_VERSION" "$CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS" > tool-environment; `
			}
			command += `printf output; printf diagnostic >&2; exit 7`
			input, err := json.Marshal(
				map[string]string{
					row.key: command,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			fix.write(
				Scenario{
					SessionID: fixtureSession,
					Pane:      PaneShapes{Busy: "working", Compacted: "compacted", Composer: "❯"},
					Steps: []Step{
						{Type: StepToolCall, Tool: row.tool, Input: input},
						{Type: StepTurn, Reply: "continued"},
					},
				},
			)
			session := fix.startTUI(row.engine, nil, nil)
			session.waitFrame(
				"composer",
				func(frame string) bool { return strings.Contains(frame, "❯") || strings.Contains(frame, "›") },
			)
			session.typeLine("run shell")
			session.waitFrame(
				"reply after failed shell",
				func(frame string) bool { return strings.Contains(frame, "continued") },
			)
			path := filepath.Join(fix.work, "shell-receipt")
			want := fmt.Sprintf("%d\n%s\natlas\n", os.Getpid(), fix.work)
			if got := readFile(t, path); got != want {
				t.Fatalf("%s: receipt=%q want=%q", path, got, want)
			}
			if row.engine == engineCodex {
				var callID string
				for _, line := range strings.Split(strings.TrimSpace(readFile(t, fix.rolloutPath())), "\n") {
					var record struct {
						Payload struct {
							Type   string `json:"type"`
							CallID string `json:"call_id"`
						} `json:"payload"`
					}
					if err := json.Unmarshal([]byte(line), &record); err != nil {
						t.Fatal(err)
					}
					if record.Payload.Type == "function_call" {
						callID = record.Payload.CallID
					}
				}
				if callID == "" {
					t.Fatal("no actual tool call id in rollout")
				}
				wantEnv := strings.Join(
					[]string{callID, "1", "cat", "cat", "cat", "1", fixtureSession, "0.154.0", "1", ""},
					"\n",
				)
				if got := readFile(t, filepath.Join(fix.work, "tool-environment")); got != wantEnv {
					t.Fatalf("tool environment=%q want=%q", got, wantEnv)
				}
			}
			var receipt struct {
				Stdout   string `json:"stdout"`
				Stderr   string `json:"stderr"`
				ExitCode int    `json:"exit_code"`
			}
			if err := json.Unmarshal(
				[]byte(readFile(t, filepath.Join(fix.recordDir, "shell.jsonl"))),
				&receipt,
			); err != nil {
				t.Fatal(err)
			}
			if receipt.Stdout != "output" || receipt.Stderr != "diagnostic" || receipt.ExitCode != 7 {
				t.Fatalf("shell receipt=%+v want output/diagnostic/7", receipt)
			}
		})
	}
}

func TestFileToolEditsExactBytesAndRefusesAmbiguity(t *testing.T) {
	fix := newFixture(t)
	proc := &process{cwd: fix.work}
	for _, row := range []struct {
		tool, input, want string
		failed            bool
	}{
		{"Write", `{"file_path":"nested/report.txt","content":"a a\n"}`, "a a\n", false},
		{"Edit", `{"file_path":"nested/report.txt","old_string":"a","new_string":"b"}`, "a a\n", true},
		{"Edit", `{"file_path":"nested/report.txt","old_string":"a","new_string":"b","replace_all":true}`, "b b\n", false},
		{"Edit", `{"file_path":"nested/report.txt","old_string":"absent","new_string":"c"}`, "b b\n", true},
		{"write", `{"filePath":"nested/report.txt","content":"before\n"}`, "before\n", false},
		{"edit", `{"filePath":"nested/report.txt","oldString":"before","newString":"after"}`, "after\n", false},
	} {
		result, err := proc.executeTool(row.tool, json.RawMessage(row.input))
		if err != nil || (result.Failure != "") != row.failed {
			t.Fatalf("%s: result=%+v err=%v want failed=%v", row.input, result, err, row.failed)
		}
		path := filepath.Join(fix.work, "nested", "report.txt")
		if got := readFile(t, path); got != row.want {
			t.Fatalf("%s: bytes=%q want=%q", path, got, row.want)
		}
	}
}
