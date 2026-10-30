package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// --plan previews, no flag writes, a stray argument is usage, and a failing
// step exits 1 with the step named on stderr.
func TestGenerateModesAndFailure(t *testing.T) {
	previous := runGenerator
	t.Cleanup(func() { runGenerator = previous })
	cases := []struct {
		args []string
		err  error
		mode installer.Mode
		code int
	}{
		{args: nil, mode: installer.ModeApply},
		{args: []string{"--plan"}, mode: installer.ModeDryRun},
		{args: []string{"extra"}, code: 2},
		{args: nil, err: errors.New("engine-sync policy: boom"), mode: installer.ModeApply, code: 1},
	}
	for _, testCase := range cases {
		called := false
		runGenerator = func(_ context.Context, options installer.Options) (installer.Report, error) {
			called = true
			if options.Mode != testCase.mode {
				t.Fatalf("generate %v mode=%v, want %v", testCase.args, options.Mode, testCase.mode)
			}
			return installer.Report{}, testCase.err
		}
		var out, errOut bytes.Buffer
		runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
		code := runGenerate(testCase.args, &out, &errOut, runtime)
		if code != testCase.code || called != (testCase.code != 2) {
			t.Fatalf("generate %v code=%d called=%v stderr=%s", testCase.args, code, called, errOut.String())
		}
		if testCase.err != nil && !strings.Contains(errOut.String(), "pfm generate: engine-sync policy: boom") {
			t.Fatalf("generate failure stderr does not name the step: %s", errOut.String())
		}
	}
}
