package deps

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestFakeRunnerScriptStartOutputPlaysTheChildAtStart(t *testing.T) {
	t.Parallel()
	fake := &FakeRunner{}
	var atStart []string
	fake.ScriptStartOutput([]string{"engine"}, StartOutput{
		AtStart: func(argv []string, opts StartOptions) { atStart = append(append([]string(nil), argv...), opts.Dir) },
		Stdout:  "out\n", Stderr: "err\n", ExitCode: 9,
	})
	var stdout, stderr strings.Builder
	process, err := fake.Start(context.Background(), []string{"engine", "run"}, StartOptions{
		Dir: "/work", Stdin: strings.NewReader("prompt"), Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got := strings.Join(atStart, "|"); got != "engine|run|/work" {
		t.Fatalf("AtStart saw %q, want engine|run|/work", got)
	}
	if stdout.String() != "out\n" || stderr.String() != "err\n" {
		t.Fatalf("child wrote stdout=%q stderr=%q, want out/err lines", stdout.String(), stderr.String())
	}
	waitErr := process.Wait()
	if waitErr == nil || waitErr.Error() != "exit status 9" || ExitCode(waitErr) != 9 {
		t.Fatalf("Wait() = %v (code %d), want exit status 9", waitErr, ExitCode(waitErr))
	}
	left, _ := io.ReadAll(fake.Starts()[0].Opts.Stdin)
	if string(left) != "prompt" {
		t.Fatalf("Starts()[0] stdin = %q, want the unread prompt", left)
	}
	fake.ScriptStartOutput([]string{"quiet"}, StartOutput{})
	other, err := fake.Start(context.Background(), []string{"quiet"}, StartOptions{})
	if err != nil {
		t.Fatalf("quiet Start() error = %v", err)
	}
	if waitErr := other.Wait(); waitErr != nil {
		t.Fatalf("quiet Wait() = %v, want a clean exit", waitErr)
	}
	atStart = nil
	_, err = fake.Start(context.Background(), []string{"engine"}, StartOptions{StdoutPipe: true})
	if want := `deps: FakeRunner: interactive script for ["engine"] did not provide requested pipes`; err == nil ||
		err.Error() != want {
		t.Fatalf("a StdoutPipe Start on an output script = %v, want %q", err, want)
	}
	if atStart != nil {
		t.Fatalf("a refused Start played the child: AtStart saw %q, want nothing", atStart)
	}
}
