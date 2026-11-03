package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

func TestClaudeStatuslineRateWindowsFollowResponses(t *testing.T) {
	fix := newFixture(t)
	account := filepath.Join(fix.home, ".cc", "2")
	t.Setenv("CLAUDE_CONFIG_DIR", account)
	accounts := filepath.Join(fix.recordDir, "statusline-accounts")
	command := `cat >> "` + fix.recordDir + `/statusline.jsonl"; printf '\n' >> "` + fix.recordDir +
		`/statusline.jsonl"; printf '%s\n' "$CLAUDE_CONFIG_DIR" >> "` + accounts + `"; printf 'QUOTA-FIXTURE\n'`
	settings := inlineHookSettings(t, nil, command)
	quota := json.RawMessage(readFile(t, filepath.Join("testdata", "statusline", "quota89.json")))
	weekly := json.RawMessage(readFile(t, filepath.Join("testdata", "statusline", "weekly-only.json")))
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, RateLimits: quota, Steps: []Step{
		{Type: StepTurn, Reply: "first quota"},
		{Type: StepTurn, Reply: "weekly only", RateLimits: weekly},
		{Type: StepTurn, Reply: "cleared quota", RateLimits: json.RawMessage(`{}`)},
		{Type: StepTurn, Reply: "unchanged quota"},
	}})
	session := fix.startTUI(engineClaude, []string{"--settings", settings, "--effort", "high"}, nil)
	session.waitFrame("configured statusline", func(frame string) bool {
		return strings.Contains(frame, "QUOTA-FIXTURE")
	})
	assertStatuslineQuota(t, fix, 1, nil)
	for index, row := range []struct {
		prompt, reply string
		want          json.RawMessage
	}{
		{"first", "first quota", quota},
		{"second", "weekly only", weekly},
		{"third", "cleared quota", nil},
		{"fourth", "unchanged quota", nil},
	} {
		session.typeLine(row.prompt)
		session.waitFrame(row.reply, func(frame string) bool { return strings.Contains(frame, "⏺ "+row.reply) })
		assertStatuslineQuota(t, fix, index+2, row.want)
	}
	if got, want := readFile(t, accounts), strings.Repeat(account+"\n", 5); got != want {
		t.Fatalf("%s: callback account environment=%q, want %q", accounts, got, want)
	}
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%q, want 0", code, session.stderr.String())
	}
}

func TestClaudeStatuslineStopGateRetainsLiveActor(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.recordDir, "callback-gate")
	stop := filepath.Join(fix.recordDir, "stop-refresh")
	started := filepath.Join(fix.recordDir, "callback-started")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := os.OpenFile(gate, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release.Close() })
	command := `cat >/dev/null; if [ ! -e "` + started + `" ]; then printf 'started' > "` + started + `"; read release < "` + gate + `"; fi; printf 'LIVE-STATUS\n'`
	fix.write(Scenario{SessionID: fixtureSession})
	setScenarioField(t, fix, "idle_statusline_refresh", map[string]any{
		"count": 1800, "interval_ms": 10, "stop_when_exists": stop,
	})
	session := fix.startTUI(engineClaude, []string{"--settings", inlineHookSettings(t, nil, command)}, nil)
	waitFile(t, started, func(content string) bool { return content == "started" })
	if err := os.WriteFile(stop, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := release.WriteString("go\n"); err != nil {
		t.Fatal(err)
	}
	lines := fix.waitRecorded("statusline-idle", 1)
	var receipt map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &receipt); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(receipt, map[string]string{"event": "stopped", "path": stop}) {
		t.Fatalf("stop receipt=%v, want stopped path=%s", receipt, stop)
	}
	if got := fix.waitRecorded("statusline-callbacks", 1); len(got) != 1 {
		t.Fatalf("callbacks after stop=%q, want initial callback only", got)
	}
	session.typeLine("/rename still alive")
	waitFile(t, fix.claudeTranscript(fixtureSession), func(content string) bool {
		return strings.Contains(content, `"customTitle":"still alive"`)
	})
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s, want 0", code, session.stderr.String())
	}
}

func assertStatuslineQuota(t *testing.T, fix *fixture, count int, want json.RawMessage) {
	t.Helper()
	lines := fix.waitRecorded("statusline", count)
	if len(lines) != count {
		t.Fatalf("statusline callback count=%d, want %d; payloads=%q", len(lines), count, lines)
	}
	var payload struct {
		SessionID string          `json:"session_id"`
		Rate      json.RawMessage `json:"rate_limits"`
		Effort    struct {
			Level string `json:"level"`
		} `json:"effort"`
	}
	if err := json.Unmarshal([]byte(lines[count-1]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != fixtureSession || payload.Effort.Level != "high" {
		t.Fatalf("native statusline identity/effort=%+v, want session=%s effort=high", payload, fixtureSession)
	}
	var gotValue, wantValue any
	if len(payload.Rate) > 0 {
		if err := json.Unmarshal(payload.Rate, &gotValue); err != nil {
			t.Fatal(err)
		}
	}
	if len(want) > 0 {
		if err := json.Unmarshal(want, &wantValue); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf(
			"%s: callback %d rate_limits=%s, want %s",
			filepath.Join(fix.recordDir, "statusline.jsonl"),
			count,
			payload.Rate,
			want,
		)
	}
}

func TestClaudeStatuslineRefreshesReapTheirCommands(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	children := filepath.Join(fix.recordDir, "statusline-children")
	command := `cat >/dev/null; printf '%s\n' "$$" >> "` + children + `"; printf 'REFRESH-FIXTURE\nsecond line\n'; printf 'diagnostic\n' >&2`
	fix.write(Scenario{SessionID: fixtureSession})
	setScenarioField(t, fix, "idle_statusline_refresh", map[string]int{"count": 9, "interval_ms": 20})
	session := fix.startTUI(engineClaude, []string{"--settings", inlineHookSettings(t, nil, command)}, nil)
	session.waitFrame("initial refresh", func(frame string) bool { return strings.Contains(frame, "REFRESH-FIXTURE") })
	receipts := fix.waitRecorded("statusline-callbacks", 10)
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	if len(receipts) != 10 {
		t.Fatalf("callback receipts=%q want ten", receipts)
	}
	for _, line := range receipts {
		var receipt struct {
			Input     statusInput `json:"input"`
			Stdout    string      `json:"stdout"`
			Stderr    string      `json:"stderr"`
			PID       int         `json:"pid"`
			ExitCode  int         `json:"exit_code"`
			ConfigDir string      `json:"config_dir"`
			Argv      []string    `json:"argv"`
		}
		if err := json.Unmarshal([]byte(line), &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.Input.SessionID != fixtureSession || receipt.Stdout != "REFRESH-FIXTURE\nsecond line\n" ||
			receipt.Stderr != "diagnostic\n" || receipt.ExitCode != 0 || receipt.ConfigDir != fix.configDir ||
			len(
				receipt.Argv,
			) != 3 || filepath.Base(receipt.Argv[0]) != "sh" || receipt.Argv[1] != "-c" || receipt.Argv[2] != command {
			t.Fatalf("callback=%+v want native input, full output, completed shell argv and account", receipt)
		}
		if _, err := gather.NewProcFS("").Stat(receipt.PID); !os.IsNotExist(err) {
			t.Fatalf("callback pid=%d stat=%v want reaped", receipt.PID, err)
		}
	}
	if got := readMeta(t, fix.claudeTranscript(fixtureSession)); got.HumanPrompts != 0 {
		t.Fatalf("idle refresh sent prompts: %+v", got)
	}
	lines := strings.Split(strings.TrimSpace(readFile(t, children)), "\n")
	if len(lines) != 10 {
		t.Fatalf("%s: statusline child receipts=%q, want 10 completed refreshes", children, lines)
	}
	processes, err := gather.NewProcFS("").PIDs()
	if err != nil {
		t.Fatal(err)
	}
	live := make(map[int]bool, len(processes))
	for _, pid := range processes {
		live[pid] = true
	}
	for _, raw := range lines {
		pid, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("%s: child pid=%q: %v", children, raw, err)
		}
		if live[pid] {
			t.Fatalf("statusline child pid=%d after completed callback, want exited and reaped", pid)
		}
	}
}
