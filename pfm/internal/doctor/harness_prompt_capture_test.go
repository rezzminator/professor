package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// writeFakeHarnessClaude stages a shell `claude` stand-in used ONLY by the
// three regression tests below: real REAL-SESSION coverage stays in
// harness_prompt_native_test.go (PFM_TEST_CLAUDE_NATIVE), this fixture never
// touches a network endpoint that is not pfm's own loopback sink.
func writeFakeHarnessClaude(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --version ]; then printf '2.1.270 (Claude Code)\\n'; exit 0; fi\n" +
		body
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestHarnessCaptureReportsBypassWhenTheCLIAnswersWithoutHittingTheSink pins
// change C: a CLI that answers with a real, priced result and never contacts
// pfm's capture sink must be named CANNOT CAPTURE, never a generic
// CHECK FAILED — the doctor row that reports this never gets to guess whether
// the OAuth-only routing spent real money.
func TestHarnessCaptureReportsBypassWhenTheCLIAnswersWithoutHittingTheSink(t *testing.T) {
	shortenHarnessCaptureSinkGrace(t)
	binary := writeFakeHarnessClaude(
		t,
		"printf '{\"stop_reason\":\"end_turn\",\"usage\":{\"input_tokens\":12,\"output_tokens\":4},\"result\":\"hi\"}\\n'\nexit 0\n",
	)
	machine := config.Config{}
	machine.Claude.Binary = binary

	captured, err := captureHarnessPrompt(t.Context(), t.TempDir(), machine, "sonnet", "")
	if !errors.Is(err, errHarnessBypassedSink) {
		t.Fatalf("captureHarnessPrompt error = %v, want errHarnessBypassedSink", err)
	}
	if captured.Prompt != "" {
		t.Fatalf("bypass capture carries a prompt body: %+v", captured)
	}

	line, warn := harnessPromptVerdict(strings.Repeat("0", 64), "fixture-baseline.md", captured.Prompt, err)
	if !warn || !strings.Contains(line, "CANNOT CAPTURE") {
		t.Fatalf("verdict = (%q, %v), want a CANNOT CAPTURE warning", line, warn)
	}
}

// TestHarnessCaptureRunsTheCLIInAThrowawayConfigDir pins change B: the
// capture launches with a fresh, per-capture CLAUDE_CONFIG_DIR — never the
// caller's real one — and removes it once the capture returns.
func TestHarnessCaptureRunsTheCLIInAThrowawayConfigDir(t *testing.T) {
	shortenHarnessCaptureSinkGrace(t)
	record := filepath.Join(t.TempDir(), "config-dir-seen.txt")
	t.Setenv("PFM_TEST_HARNESS_CONFIGDIR_RECORD", record)
	binary := writeFakeHarnessClaude(
		t,
		"printf '%s' \"$CLAUDE_CONFIG_DIR\" > \"$PFM_TEST_HARNESS_CONFIGDIR_RECORD\"\nexit 1\n",
	)
	machine := config.Config{}
	machine.Claude.Binary = binary

	home := t.TempDir()
	if _, err := captureHarnessPrompt(t.Context(), home, machine, "sonnet", ""); err == nil {
		t.Fatal("fake CLI never reaches the sink, want a capture error")
	}

	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("fake CLI did not record CLAUDE_CONFIG_DIR: %v", err)
	}
	seen := strings.TrimSpace(string(raw))
	if seen == "" {
		t.Fatal("CLAUDE_CONFIG_DIR was empty — capture did not pin a throwaway config dir")
	}
	if seen == filepath.Join(home, ".claude") {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want a fresh temp dir, not the real home config", seen)
	}
	if info, statErr := os.Stat(seen); statErr == nil {
		t.Fatalf(
			"throwaway CLAUDE_CONFIG_DIR %q still exists after capture (mode %s) — it must be removed",
			seen,
			info.Mode(),
		)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("stat throwaway CLAUDE_CONFIG_DIR %q: %v", seen, statErr)
	}
}

// TestDoctorVerboseKeepsTheHarnessRunOutput pins change A: --verbose keeps
// the harness run's own stdout/stderr under tmp/pfm-doctor/ — today that
// evidence is discarded and a timeout renders as bare prose no one can act on.
func TestDoctorVerboseKeepsTheHarnessRunOutput(t *testing.T) {
	shortenHarnessCaptureSinkGrace(t)
	binary := writeFakeHarnessClaude(t, "printf 'diagnostic-stderr-line\\n' 1>&2\nexit 1\n")
	machine := config.Config{}
	machine.Claude.Binary = binary

	verboseDir := t.TempDir()
	if _, err := captureHarnessPrompt(t.Context(), t.TempDir(), machine, "sonnet", verboseDir); err == nil {
		t.Fatal("fake CLI never reaches the sink, want a capture error")
	}

	stderrPath := filepath.Join(verboseDir, "harness-prompt.stderr")
	raw, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("%s not written: %v", stderrPath, err)
	}
	if !strings.Contains(string(raw), "diagnostic-stderr-line") {
		t.Fatalf("%s = %q, want the fake CLI's stderr", stderrPath, raw)
	}

	hitsPath := filepath.Join(verboseDir, "sink-hits.txt")
	if _, err := os.Stat(hitsPath); err != nil {
		t.Fatalf("%s not written: %v", hitsPath, err)
	}
}

func shortenHarnessCaptureSinkGrace(t *testing.T) {
	t.Helper()
	previous := harnessCaptureSinkGrace
	harnessCaptureSinkGrace = 20 * time.Millisecond
	t.Cleanup(func() { harnessCaptureSinkGrace = previous })
}

func TestHarnessCaptureVersionUsesWaitDelay(t *testing.T) {
	shortenHarnessCaptureSinkGrace(t)
	binary := deps.Executable("sh")
	fake := &deps.FakeRunner{}
	fake.Script([]string{binary, "--version"}, deps.RunResult{Stdout: []byte("2.1.fixture\n")}, nil)
	machine := config.Config{}
	machine.Claude.Binary = binary
	_, _ = captureHarnessPromptWithDeps(
		t.Context(), t.TempDir(), machine, "sonnet", "", Dependencies{Runner: fake},
	)
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("capture made no version call")
	}
	if got := calls[0].Opts.WaitDelay; got != 500*time.Millisecond {
		t.Fatalf("version RunOptions.WaitDelay = %s, want 500ms", got)
	}
}

func TestHarnessCaptureReturnsDecodedResultWithEvidenceWriteError(t *testing.T) {
	shortenHarnessCaptureSinkGrace(t)
	verboseDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(verboseDir, "sink-hits.txt"), 0o700); err != nil {
		t.Fatalf("make sink evidence directory: %v", err)
	}
	binary := deps.Executable("sh")
	runner := harnessCaptureHTTPRunner{binary: binary}
	machine := config.Config{}
	machine.Claude.Binary = binary
	captured, err := captureHarnessPromptWithDeps(
		t.Context(), t.TempDir(), machine, "sonnet", verboseDir, Dependencies{Runner: runner},
	)
	if err == nil || !strings.Contains(err.Error(), "sink hit evidence") {
		t.Fatalf("capture error = %v, want sink evidence write error", err)
	}
	if captured.ResolvedModel != "fixture-model" || captured.Prompt == "" {
		t.Fatalf("capture = %+v, want decoded result preserved with evidence error", captured)
	}
}

type harnessCaptureHTTPRunner struct {
	binary string
}

func (runner harnessCaptureHTTPRunner) Run(
	_ context.Context,
	argv []string,
	_ deps.RunOptions,
) (deps.RunResult, error) {
	if len(argv) == 2 && argv[0] == runner.binary && argv[1] == "--version" {
		return deps.RunResult{Stdout: []byte("2.1.fixture\n")}, nil
	}
	return deps.RunResult{ExitCode: -1}, fmt.Errorf("unexpected Run argv %q", argv)
}

func (runner harnessCaptureHTTPRunner) LookPath(name string) (string, error) {
	return name, nil
}

func (runner harnessCaptureHTTPRunner) Start(
	_ context.Context,
	_ []string,
	options deps.StartOptions,
) (deps.Process, error) {
	for _, entry := range options.Env {
		if strings.HasPrefix(entry, "ANTHROPIC_BASE_URL=") {
			response, err := http.Post(
				strings.TrimPrefix(entry, "ANTHROPIC_BASE_URL=")+"/v1/messages",
				"application/json",
				strings.NewReader(
					`{"model":"fixture-model","system":[{"text":"captured fixture prompt"}]}`,
				),
			)
			if err != nil {
				return nil, err
			}
			_ = response.Body.Close()
			return harnessCaptureHTTPProcess{}, nil
		}
	}
	return nil, errors.New("capture runner missing ANTHROPIC_BASE_URL")
}

type harnessCaptureHTTPProcess struct{}

func (harnessCaptureHTTPProcess) Pid() int       { return 1 }
func (harnessCaptureHTTPProcess) Wait() error    { return nil }
func (harnessCaptureHTTPProcess) Release() error { return nil }
func (harnessCaptureHTTPProcess) StdinPipe() (io.WriteCloser, error) {
	return nil, errors.New("stdin pipe unavailable")
}

func (harnessCaptureHTTPProcess) StdoutPipe() (io.ReadCloser, error) {
	return nil, errors.New("stdout pipe unavailable")
}
func (harnessCaptureHTTPProcess) Kill() error      { return nil }
func (harnessCaptureHTTPProcess) KillGroup() error { return nil }
