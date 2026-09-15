package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
)

func TestHarnessDoctorDistinguishesModelCoverageAndCaptureFailures(t *testing.T) {
	saved := harnessCaptureOverride
	t.Cleanup(func() { harnessCaptureOverride = saved })
	for _, tc := range []struct {
		name, model, prompt, missing, want string
		err                                error
		warning                            bool
	}{
		{name: "match", model: "claude-sonnet-5", prompt: harnessPromptFixtureCaptured, want: "matches baseline"},
		{name: "behavior change", model: "claude-sonnet-5", prompt: "different instructions\n", want: "DRIFT", warning: true},
		{name: "renamed model same bytes", model: "claude-renamed-6", prompt: harnessPromptFixtureCaptured, want: "matches baseline"},
		{name: "capture failure", want: "CHECK FAILED", err: errors.New("sink unavailable"), warning: true},
		{name: "missing baseline model", missing: "harness-original.model", want: "BASELINE UNAVAILABLE", warning: true},
		{name: "missing baseline body", missing: "harness-prompt-fixture.md", want: "BASELINE UNAVAILABLE", warning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			stageHarnessPromptBaseline(t, home)
			if tc.missing != "" {
				if err := os.Remove(filepath.Join(home, ".local/share/pfm/install/prompts", tc.missing)); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			harnessCaptureOverride = func(context.Context, string, config.Config, string, string) (harnessCapture, error) {
				called = true
				return harnessCapture{Prompt: tc.prompt, ResolvedModel: tc.model, CLIVersion: "2.1.fixture"}, tc.err
			}
			var output bytes.Buffer
			code := printModelHarnessPromptDoctor(
				context.Background(),
				&output,
				home,
				config.Config{},
				harnessPromptModels[0],
				"",
			)
			if (code != 0) != tc.warning || !strings.Contains(output.String(), tc.want) {
				t.Fatalf("code=%d output=%s", code, &output)
			}
			if tc.missing != "" && called {
				t.Fatal("capture ran without a complete baseline")
			}
			if called {
				for _, field := range []string{"runtime=claude-code", "scope=claude-model-baseline", "requested=sonnet", "cli=\"2.1.fixture\"", "baseline_model=claude-sonnet-5", "unchecked=active-chat,fable,codex"} {
					if !strings.Contains(output.String(), field) {
						t.Errorf("missing %s: %s", field, &output)
					}
				}
			}
		})
	}
}

func TestHarnessCaptureRetainsRequestModel(t *testing.T) {
	got, err := decodeHarnessCapture(
		[]byte(`{"model":"claude-fable-5-1","system":[{"text":"model-specific instructions"}]}`),
	)
	if err != nil || got.ResolvedModel != "claude-fable-5-1" || got.Prompt != "model-specific instructions\n" {
		t.Fatalf("capture=%+v err=%v", got, err)
	}
	for _, raw := range []string{`{`, `{"system":"prompt"}`, `{"model":"claude-sonnet-5"}`, `{"model":"claude-sonnet-5","system":[]}`} {
		if _, err := decodeHarnessCapture([]byte(raw)); err == nil {
			t.Fatalf("invalid capture accepted: %s", raw)
		}
	}
}
