package doctor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestHarnessDoctorDistinguishesModelCoverageAndCaptureFailures(t *testing.T) {
	saved := HarnessCaptureOverride
	t.Cleanup(func() { HarnessCaptureOverride = saved })
	for _, tc := range []struct {
		name, model, prompt, missing, want string
		err                                error
		warning                            bool
	}{
		{name: "match", model: "claude-sonnet-5", prompt: harnessPromptFixtureCaptured, want: "doctor: harness-prompt requested=sonnet\ndoctor: harness-prompt scope=claude-model-baseline runtime=claude-code requested=sonnet resolved=claude-sonnet-5 cli=\"2.1.fixture\" baseline=harness-prompt-fixture.md baseline_model=claude-sonnet-5 unchecked=active-chat,fable,codex\ndoctor: harness-prompt: matches baseline harness-prompt-fixture.md\n"},
		{name: "behavior change", model: "claude-sonnet-5", prompt: "different instructions\n", want: "doctor: harness-prompt requested=sonnet\ndoctor: harness-prompt scope=claude-model-baseline runtime=claude-code requested=sonnet resolved=claude-sonnet-5 cli=\"2.1.fixture\" baseline=harness-prompt-fixture.md baseline_model=claude-sonnet-5 unchecked=active-chat,fable,codex\ndoctor: harness-prompt: DRIFT live=25e177dae1b9ed5f baseline=1832797f55feeb8f (harness-prompt-fixture.md) — harness instructions changed; review before re-pinning\ndoctor: harness-prompt:   section changed: (preamble)\n", warning: true},
		{name: "renamed model same bytes", model: "claude-renamed-6", prompt: harnessPromptFixtureCaptured, want: "doctor: harness-prompt requested=sonnet\ndoctor: harness-prompt scope=claude-model-baseline runtime=claude-code requested=sonnet resolved=claude-renamed-6 cli=\"2.1.fixture\" baseline=harness-prompt-fixture.md baseline_model=claude-sonnet-5 unchecked=active-chat,fable,codex\ndoctor: harness-prompt: matches baseline harness-prompt-fixture.md\ndoctor: harness-prompt:   model changed sonnet: claude-sonnet-5 → claude-renamed-6\n"},
		{name: "capture failure", want: "doctor: harness-prompt requested=sonnet\ndoctor: harness-prompt scope=claude-model-baseline runtime=claude-code requested=sonnet resolved=unknown cli=\"2.1.fixture\" baseline=harness-prompt-fixture.md baseline_model=claude-sonnet-5 unchecked=active-chat,fable,codex\ndoctor: harness-prompt: CHECK FAILED to run (sink unavailable) — drift unknown\n", err: errors.New("sink unavailable"), warning: true},
		{
			name: "missing baseline model", model: "claude-sonnet-5", prompt: harnessPromptFixtureCaptured,
			missing: "harness-original.model", warning: true,
			want: "doctor: harness-prompt requested=sonnet\ndoctor: harness-prompt scope=claude-model-baseline runtime=claude-code requested=sonnet resolved=claude-sonnet-5 cli=\"2.1.fixture\" baseline=harness-prompt-fixture.md baseline_model=unknown unchecked=active-chat,fable,codex\ndoctor: harness-prompt: matches baseline harness-prompt-fixture.md\ndoctor: harness-prompt: baseline_model unknown (open {root}/harness-original.model: no such file or directory) — the prompt was still compared; update or restore the clone at {root}\n",
		},
		{name: "missing baseline body", missing: "harness-prompt-fixture.md", want: "doctor: harness-prompt requested=sonnet\ndoctor: harness-prompt: BASELINE UNAVAILABLE identity=harness-original model=\"sonnet\" dir={root} path={root}/harness-original.sha256 error=captured model=\"claude-sonnet-5\" body=\"harness-prompt-fixture.md\": open {root}/harness-prompt-fixture.md: no such file or directory — baseline inconsistent; update or restore the clone at {root}\n", warning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			stageHarnessPromptBaseline(t, home)
			dir, err := paths.HarnessBaselineDir(home)
			if err != nil {
				t.Fatal(err)
			}
			if tc.missing != "" {
				if err := os.Remove(
					filepath.Join(dir, tc.missing),
				); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			HarnessCaptureOverride = func(context.Context, string, config.Config, string, string) (HarnessCapture, error) {
				called = true
				return HarnessCapture{Prompt: tc.prompt, ResolvedModel: tc.model, CLIVersion: "2.1.fixture"}, tc.err
			}
			var output bytes.Buffer
			code := printModelHarnessPromptDoctor(
				context.Background(),
				&output,
				home,
				harnessPromptModels[0],
			)
			if got := rooted(output.String(), dir); (code != 0) != tc.warning || got != tc.want {
				t.Errorf("code=%d output = %q, want %q", code, got, tc.want)
			}
			if tc.missing == "harness-prompt-fixture.md" && called {
				t.Error("capture ran without a complete baseline")
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
	modelless, err := decodeHarnessCapture([]byte(`{"system":"prompt"}`))
	if err != nil || modelless != (HarnessCapture{Prompt: "prompt\n"}) {
		t.Fatalf("a capture naming no model was not kept for comparison: capture=%+v err=%v", modelless, err)
	}
	for _, raw := range []string{`{`, `{"model":"claude-sonnet-5"}`, `{"model":"claude-sonnet-5","system":[]}`} {
		if _, err := decodeHarnessCapture([]byte(raw)); err == nil {
			t.Fatalf("invalid capture accepted: %s", raw)
		}
	}
}
