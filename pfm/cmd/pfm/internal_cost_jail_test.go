//go:build jail

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

	"github.com/rezzminator/professor/pfm/internal/costledger"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

const costSession = "00000000-0000-4000-8000-0000000000a1"

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
	stdin, release, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	launch.cmd.Stdin, launch.release = stdin, release
	if err := launch.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	launch.exited = make(chan struct{})
	go func() {
		launch.exitErr = launch.cmd.Wait()
		close(launch.exited)
	}()
	t.Cleanup(func() {
		_ = release.Close()
		_ = launch.cmd.Process.Kill()
		testwait.Receive(t, "the launched chat to be reaped", launch.exited)
	})
	ready := filepath.Join(out, "ready")
	testwait.Until(t, "the launched chat to start", func() (bool, string) {
		_, err := os.Stat(ready)
		return err == nil, fmt.Sprintf("stat %s: %v", ready, err)
	})
	return launch
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
	testwait.Until(t, "the sink to log that it serves this pid", func() (bool, string) {
		log := launch.sinkLog()
		serving := fmt.Sprintf("for pid %d (start time %d)", pid, start.StartTime)
		return strings.Contains(log, serving), "sink log:\n" + log
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
	testwait.Until(t, "the joined row in the ledger", func() (bool, string) {
		ledger, err := costledger.ForSession(ledgerDir, costSession)
		return len(ledger.Rows) == 1, fmt.Sprintf("%s rows %+v, %v", ledgerDir, ledger.Rows, err)
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
	testwait.Until(t, "the sink to see the chat gone and stop", func() (bool, string) {
		log := launch.sinkLog()
		gone := strings.Contains(log, fmt.Sprintf("pid %d is gone", pid)) && strings.Contains(log, "stopped")
		return gone, "sink log:\n" + log
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

// Jail: the subject is the exit status of the prebuilt pfm binary, a real child.
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
