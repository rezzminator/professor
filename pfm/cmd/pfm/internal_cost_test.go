package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/costledger"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const costSession = "00000000-0000-4000-8000-0000000000a1"

// fakeClaude records its pid, environment and open descriptors, then waits for a stop file, the
// way a chat lives while its sink serves it.
const fakeClaude = `#!/bin/sh
out="$1"
echo $$ > "$out/pid"
env > "$out/env"
for f in /proc/$$/fd/*; do readlink "$f"; done > "$out/fds"
: > "$out/ready"
while [ ! -e "$out/stop" ]; do sleep 0.05; done
`

type costLaunch struct {
	home, out string
	cmd       *exec.Cmd
	stderr    *bytes.Buffer
}

// startCostLaunch runs `env -u … pfm internal cost-launch -- fake-claude` as a launched chat does.
func startCostLaunch(t *testing.T, extraEnv ...string) *costLaunch {
	t.Helper()
	home := t.TempDir()
	out := filepath.Join(home, "out")
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(home, "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(claude), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(claude, []byte(fakeClaude), 0o700); err != nil {
		t.Fatal(err)
	}
	launch := &costLaunch{home: home, out: out, stderr: &bytes.Buffer{}}
	launch.cmd = exec.Command(
		"env",
		"-u",
		"CLAUDE_CODE_SESSION_ID",
		testPFMBinary,
		"internal",
		"cost-launch",
		"--",
		claude,
		out,
	)
	launch.cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "PFM_HOME=" + home}, extraEnv...)
	launch.cmd.Stderr = launch.stderr
	if err := launch.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(out, "stop"), nil, 0o600)
		_ = launch.cmd.Process.Kill()
		_, _ = launch.cmd.Process.Wait()
	})
	waitFor(t, "the launched chat to start", func() bool { return exists(filepath.Join(out, "ready")) })
	return launch
}

func (l *costLaunch) stop(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(l.out, "stop"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.cmd.Wait(); err != nil {
		t.Fatalf("launched chat exit: %v\n%s", err, l.stderr.String())
	}
}

func (l *costLaunch) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(l.out, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (l *costLaunch) sinkLog() string {
	data, _ := os.ReadFile(filepath.Join(l.home, ".local", "state", "pfm", "log", "cost-sink.log"))
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func envEntry(env, name string) string {
	for _, line := range strings.Split(env, "\n") {
		if value, ok := strings.CutPrefix(line, name+"="); ok {
			return value
		}
	}
	return ""
}

func costBody(kind, request, agent string, cost int64) string {
	attr := func(key, value string) string {
		return fmt.Sprintf(`{"key":%q,"value":{"stringValue":%q}}`, key, value)
	}
	if kind == "logs" {
		return `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"attributes":[` + strings.Join([]string{
			attr("event.name", "api_request"), attr("session.id", costSession), attr("request_id", request),
			attr("event.timestamp", "2026-10-09T18:27:28.025Z"), attr("query_source", "agent:builtin:general-purpose"),
			attr("model", "claude-haiku-5-5"), fmt.Sprintf(`{"key":"cost_usd_micros","value":{"intValue":%d}}`, cost),
		}, ",") + `]}]}]}]}`
	}
	return `{"resourceSpans":[{"scopeSpans":[{"spans":[{"name":"claude_code.llm_request","endTimeUnixNano":"1791570448025178291","attributes":[` +
		strings.Join(
			[]string{attr("session.id", costSession), attr("request_id", request), attr("agent_id", agent)},
			",",
		) + `]}]}]}]}`
}

// The wrapper arms a sink, execs claude with the SAME pid (the sink watches it), hands claude the
// port in its environment and nothing else of the sink, and the sink joins what claude exports and
// exits after the chat does.
func TestCostLaunchArmsASinkKeepsThePidAndHandsClaudeOnlyTheEnvironment(t *testing.T) {
	launch := startCostLaunch(t)
	pid := launch.cmd.Process.Pid
	if got := strings.TrimSpace(launch.read(t, "pid")); got != strconv.Itoa(pid) {
		t.Fatalf("claude ran as pid %s, the launched process was %d: the exec chain forked", got, pid)
	}
	env := launch.read(t, "env")
	endpoint := envEntry(env, "OTEL_EXPORTER_OTLP_ENDPOINT")
	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") || envEntry(env, "PFM_COST_LEDGER") != "on" ||
		envEntry(env, "CLAUDE_CODE_ENABLE_TELEMETRY") != "1" || envEntry(env, "OTEL_METRICS_EXPORTER") != "none" {
		t.Fatalf("claude's environment lacks the exporter knobs:\n%s", env)
	}
	for _, banned := range []string{"OTEL_LOG_USER_PROMPTS", "OTEL_LOG_TOOL_DETAILS", "OTEL_LOG_RAW_API_BODIES"} {
		if _, found := os.LookupEnv(banned); !found && strings.Contains(env, banned+"=") {
			t.Errorf("claude's environment sets %s", banned)
		}
	}
	if fds := launch.read(t, "fds"); strings.Contains(fds, "socket:") {
		t.Fatalf("claude inherited a socket (the sink's listener must close on exec):\n%s", fds)
	}
	start, err := gather.NewProcFS("").Stat(pid)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sink to log that it serves this pid", func() bool {
		return strings.Contains(launch.sinkLog(), fmt.Sprintf("for pid %d (start time %d)", pid, start.StartTime))
	})

	post := func(path, body string) {
		t.Helper()
		command := exec.Command(
			"sh",
			"-c",
			`curl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' --data-binary @- "$0"`,
			endpoint+path,
		)
		command.Stdin = strings.NewReader(body)
		out, err := command.CombinedOutput()
		if err != nil || string(out) != "200" {
			t.Fatalf("POST %s = %q, %v", path, out, err)
		}
	}
	post("/v1/logs", costBody("logs", "req_011CFixture00000099", "", 2118))
	post("/v1/traces", costBody("traces", "req_011CFixture00000099", "a0000000000000001", 0))
	ledgerDir := filepath.Join(launch.home, ".local", "state", "pfm", "cost-ledger")
	waitFor(t, "the joined row in the ledger", func() bool {
		ledger, _ := costledger.ForSession(ledgerDir, costSession)
		return len(ledger.Rows) == 1
	})
	ledger, err := costledger.ForSession(ledgerDir, costSession)
	if err != nil {
		t.Fatal(err)
	}
	if row := ledger.Rows[0]; row.Join != costledger.JoinLogSpan || row.AgentID != "a0000000000000001" ||
		*row.CostUSDMicros != 2118 {
		t.Fatalf("row = %+v", row)
	}
	launch.stop(t)
	waitFor(t, "the sink to see the chat gone and stop", func() bool {
		log := launch.sinkLog()
		return strings.Contains(log, fmt.Sprintf("pid %d is gone", pid)) && strings.Contains(log, "stopped")
	})
}

func TestCostLaunchKeepsTheUsersOwnTelemetryAndRunsWithoutALedger(t *testing.T) {
	launch := startCostLaunch(t, "OTEL_METRICS_EXPORTER=prometheus")
	env := launch.read(t, "env")
	if envEntry(env, "OTEL_METRICS_EXPORTER") != "prometheus" || envEntry(env, "PFM_COST_LEDGER") != "preempted" ||
		envEntry(env, "OTEL_EXPORTER_OTLP_ENDPOINT") != "" || envEntry(env, "CLAUDE_CODE_ENABLE_TELEMETRY") != "" {
		t.Fatalf("a user's OTEL_ setting was not kept as it was:\n%s", env)
	}
	if got := launch.sinkLog(); !strings.Contains(got, "pre-empted") ||
		!strings.Contains(got, "OTEL_METRICS_EXPORTER") ||
		strings.Contains(got, "serving") {
		t.Fatalf("sink log = %q, want the reason and no sink", got)
	}
	launch.stop(t)
}

func TestCostVerbsRefuseWithoutTheirArguments(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"internal", "cost-launch"}, 2},
		{[]string{"internal", "cost-launch", "--"}, 2},
		{[]string{"internal", "cost-sink"}, 2},
		{[]string{"internal", "cost-sink", "--pid", "0", "--start", "1"}, 2},
		{[]string{"internal", "cost-launch", "--", "/nonexistent/claude"}, 127},
	} {
		command := exec.Command(testPFMBinary, tc.args...)
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "PFM_HOME=" + t.TempDir()}
		err := command.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != tc.want {
			t.Errorf("pfm %v = %v, want exit %d", tc.args, err, tc.want)
		}
	}
}
