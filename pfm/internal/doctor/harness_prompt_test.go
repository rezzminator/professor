package doctor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
)

func TestHarnessPromptVerdictThreeOutcomes(t *testing.T) {
	captured := "block one\n\n=== SYSTEM BLOCK ===\n\nblock two\n"
	sum := sha256.Sum256([]byte(captured))
	matching := hex.EncodeToString(sum[:])

	line, warn := harnessPromptVerdict(matching, "harness-original-v2.1.278.md", captured, nil)
	if warn || !strings.Contains(line, "matches baseline harness-original-v2.1.278.md") {
		t.Fatalf("match outcome = (%q, %v), want an ok line", line, warn)
	}

	line, warn = harnessPromptVerdict(strings.Repeat("0", 64), "harness-original-v2.1.278.md", captured, nil)
	if !warn || !strings.Contains(line, "DRIFT") {
		t.Fatalf("drift outcome = (%q, %v), want a DRIFT warning", line, warn)
	}

	line, warn = harnessPromptVerdict(
		matching,
		"harness-original-v2.1.278.md",
		"",
		errors.New("no API request reached the capture sink"),
	)
	if !warn || !strings.Contains(line, "CHECK FAILED") || strings.Contains(line, "DRIFT") ||
		strings.Contains(line, "matches") {
		t.Fatalf("capture-failure outcome = (%q, %v), want a distinct CHECK FAILED warning", line, warn)
	}

	line, warn = harnessPromptVerdict(matching, "harness-original-v2.1.278.md", "", errClaudeAbsent)
	if warn || line != "doctor: harness-prompt: skipped (no Claude Code binary installed) — nothing to compare" {
		t.Fatalf("absence outcome = (%q, %v), want the named skip with no warning", line, warn)
	}
}

func TestHarnessPromptVerdictMasksBuildStamp(t *testing.T) {
	baseline := "x-anthropic-billing-header: cc_version=*; cc_entrypoint=sdk-cli;\n\n=== SYSTEM BLOCK ===\n\nprose\n"
	sum := sha256.Sum256([]byte(baseline))
	pin := hex.EncodeToString(sum[:])

	released := "x-anthropic-billing-header: cc_version=2.1.257.9c3; cc_entrypoint=sdk-cli;\n\n=== SYSTEM BLOCK ===\n\nprose\n"
	if line, warn := harnessPromptVerdict(
		pin,
		"b.md",
		released,
		nil,
	); warn ||
		!strings.Contains(line, "matches baseline") {
		t.Fatalf("a new build stamp alone = (%q, %v), want a match", line, warn)
	}

	reworded := "x-anthropic-billing-header: cc_version=2.1.257.9c3; cc_entrypoint=sdk-cli;\n\n=== SYSTEM BLOCK ===\n\nreworded prose\n"
	if line, warn := harnessPromptVerdict(pin, "b.md", reworded, nil); !warn || !strings.Contains(line, "DRIFT") {
		t.Fatalf("changed prose behind a new stamp = (%q, %v), want DRIFT", line, warn)
	}
}

func TestHarnessSinkCapturesOnlyMessagesRequests(t *testing.T) {
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(harnessSinkHandler(bodies))
	defer server.Close()

	post := func(path, body string) *http.Response {
		t.Helper()
		response, err := http.Post(server.URL+path, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response
	}

	if response := post("/v1/telemetry", ""); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("telemetry response = %d, want 400", response.StatusCode)
	}
	select {
	case body := <-bodies:
		t.Fatalf("non-messages POST won the capture: %q", body)
	default:
	}

	if response := post("/v1/messages", `{"system":"real"}`); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("messages response = %d, want 400", response.StatusCode)
	}
	post("/v1/messages", `{"system":"late-duplicate"}`)

	select {
	case body := <-bodies:
		if string(body) != `{"system":"real"}` {
			t.Fatalf("captured body = %q, want the FIRST messages body", body)
		}
	default:
		t.Fatal("messages POST did not reach the capture channel")
	}
	select {
	case body := <-bodies:
		t.Fatalf("second messages body was buffered: %q — first-match semantics broken", body)
	default:
	}
}

func TestJoinSystemBlocksMatchesBaselineRendering(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"alpha"},{"type":"text","text":"beta"}]}`)
	got, err := joinSystemBlocks(body)
	if err != nil {
		t.Fatal(err)
	}
	if got != "alpha\n\n=== SYSTEM BLOCK ===\n\nbeta\n" {
		t.Fatalf("joined = %q, want the jq-compatible join with trailing newline", got)
	}

	plain, err := joinSystemBlocks([]byte(`{"system":"solo"}`))
	if err != nil || plain != "solo\n" {
		t.Fatalf("plain system = (%q, %v), want solo with trailing newline", plain, err)
	}

	if _, err := joinSystemBlocks([]byte(`{"model":"x"}`)); err == nil {
		t.Fatal("a request with no system prompt must error, not hash empty content")
	}
}

// TestPrintHarnessPromptDoctorHonorsCaptureOverride pins the seam
// configuredHarnessCapture adds: printHarnessPromptDoctor's baseline read and
// verdict comparison stay real end to end, only the capture step is swapped —
// exactly what makes the six formerly jail-broken doctor tests (main_test.go,
// doctor_jail_test.go, launch_command_test.go, doctor_external_test.go) able
// to reach match, DRIFT, and CHECK FAILED without a real `claude` binary.
func TestPrintHarnessPromptDoctorHonorsCaptureOverride(t *testing.T) {
	saved := HarnessCaptureOverride
	t.Cleanup(func() { HarnessCaptureOverride = saved })

	stageBaseline := func(t *testing.T, home, captured, name string) {
		stageModelHarnessPromptBaseline(t, home, harnessPromptModels[0], captured, name)
	}
	refuseCapture := func(t *testing.T) func(context.Context, string, config.Config, string, string) (HarnessCapture, error) {
		return func(context.Context, string, config.Config, string, string) (HarnessCapture, error) {
			t.Fatal("capture must not run before the baseline is readable and well-formed")
			return HarnessCapture{}, nil
		}
	}

	cases := []struct {
		name     string
		setup    func(t *testing.T, home string)
		wantWarn bool
		want     string
	}{
		{
			name: "missing baseline never reaches the capture step",
			setup: func(t *testing.T, _ string) {
				HarnessCaptureOverride = refuseCapture(t)
			},
			wantWarn: true,
			want:     "doctor: harness-prompt: baseline unreadable",
		},
		{
			name: "malformed baseline is distinct from missing and never captures",
			setup: func(t *testing.T, home string) {
				path := filepath.Join(paths.HarnessBaselineDir(home), "harness-original.sha256")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("not-two-fields\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				HarnessCaptureOverride = refuseCapture(t)
			},
			wantWarn: true,
			want:     "doctor: harness-prompt: baseline malformed",
		},
		{
			name: "override content matching the staged baseline reports clean",
			setup: func(t *testing.T, home string) {
				stageBaseline(t, home, "captured-fixture\n", "fixture-baseline.md")
				HarnessCaptureOverride = func(context.Context, string, config.Config, string, string) (HarnessCapture, error) {
					return HarnessCapture{
						Prompt:        "captured-fixture\n",
						ResolvedModel: "claude-sonnet-5",
						CLIVersion:    "fixture",
					}, nil
				}
			},
			wantWarn: false,
			want:     "doctor: harness-prompt: matches baseline fixture-baseline.md",
		},
		{
			name: "override content diverging from the staged baseline reports DRIFT",
			setup: func(t *testing.T, home string) {
				stageBaseline(t, home, "captured-fixture\n", "fixture-baseline.md")
				HarnessCaptureOverride = func(context.Context, string, config.Config, string, string) (HarnessCapture, error) {
					return HarnessCapture{
						Prompt:        "a different live prompt\n",
						ResolvedModel: "claude-sonnet-5",
						CLIVersion:    "fixture",
					}, nil
				}
			},
			wantWarn: true,
			want:     "doctor: harness-prompt: DRIFT",
		},
		{
			name: "override capture error reports CHECK FAILED, never DRIFT or matches",
			setup: func(t *testing.T, home string) {
				stageBaseline(t, home, "captured-fixture\n", "fixture-baseline.md")
				HarnessCaptureOverride = func(context.Context, string, config.Config, string, string) (HarnessCapture, error) {
					return HarnessCapture{}, errors.New("no API request reached the capture sink")
				}
			},
			wantWarn: true,
			want:     "doctor: harness-prompt: CHECK FAILED to run",
		},
		{
			name: "Claude absence reports skipped, never CHECK FAILED, and no warning",
			setup: func(t *testing.T, home string) {
				stageBaseline(t, home, "captured-fixture\n", "fixture-baseline.md")
				HarnessCaptureOverride = func(context.Context, string, config.Config, string, string) (HarnessCapture, error) {
					return HarnessCapture{}, errClaudeAbsent
				}
			},
			wantWarn: false,
			want:     "doctor: harness-prompt: skipped (no Claude Code binary installed) — nothing to compare",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			testCase.setup(t, home)
			var stdout bytes.Buffer
			code := printModelHarnessPromptDoctor(
				context.Background(),
				&stdout,
				home,
				config.Config{},
				harnessPromptModels[0],
				"",
			)
			if warned := code != 0; warned != testCase.wantWarn {
				t.Fatalf(
					"code=%d warned=%v, want warned=%v\noutput=%s",
					code,
					warned,
					testCase.wantWarn,
					stdout.String(),
				)
			}
			if !strings.Contains(stdout.String(), testCase.want) {
				t.Fatalf("output=%q, want substring %q", stdout.String(), testCase.want)
			}
		})
	}
}

func TestHarnessPromptVerdictIgnoresOptionalEnvironmentMetadata(t *testing.T) {
	baseline := "# Environment\n - Keep this behavioral instruction.\n\n# Other\n - Assistant knowledge cutoff is part of an example here.\n"
	sum := sha256.Sum256([]byte(baseline))
	pin := hex.EncodeToString(sum[:])
	metadata := " - You are powered by the model named Sonnet 5. The exact model ID is claude-sonnet-5.\n - Assistant knowledge cutoff is January 2026.\n"
	for _, optional := range []string{"", metadata} {
		captured := strings.Replace(baseline, "# Environment\n", "# Environment\n"+optional, 1)
		if line, warn := harnessPromptVerdict(pin, "fixture.md", captured, nil); warn {
			t.Fatalf("optional metadata produced a warning: %s", line)
		}
		changed := strings.Replace(
			captured,
			"Keep this behavioral instruction.",
			"Change this behavioral instruction.",
			1,
		)
		if line, warn := harnessPromptVerdict(
			pin,
			"fixture.md",
			changed,
			nil,
		); !warn ||
			!strings.Contains(line, "DRIFT") {
			t.Fatalf("behavioral change was hidden: %s", line)
		}
	}
}

func TestKnownSIDMetadataIncludesNudgeRecords(t *testing.T) {
	for _, name := range []string{"nudge-ctx-11111111-2222-4333-8444-555555555555", "nudge-band-11111111-2222-4333-8444-555555555555", "nudge-ctx-session-a"} {
		if !knownSIDMetadata(name) {
			t.Errorf("valid nudge record %q rejected", name)
		}
	}
	for _, name := range []string{"nudge-ctx-", "nudge-band-", "nudge-band-   "} {
		if knownSIDMetadata(name) {
			t.Errorf("invalid nudge record %q accepted", name)
		}
	}
}
