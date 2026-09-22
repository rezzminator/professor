package doctor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestHarnessPromptVerdictThreeOutcomes(t *testing.T) {
	captured := "block one\n\n=== SYSTEM BLOCK ===\n\nblock two\n"
	sum := sha256.Sum256([]byte(captured))
	matching := hex.EncodeToString(sum[:])

	line, warn := harnessPromptVerdict(matching, "harness-original-v2.1.280.md", captured, nil)
	if warn || !strings.Contains(line, "matches baseline harness-original-v2.1.280.md") {
		t.Fatalf("match outcome = (%q, %v), want an ok line", line, warn)
	}

	line, warn = harnessPromptVerdict(strings.Repeat("0", 64), "harness-original-v2.1.280.md", captured, nil)
	if !warn || !strings.Contains(line, "DRIFT") {
		t.Fatalf("drift outcome = (%q, %v), want a DRIFT warning", line, warn)
	}

	line, warn = harnessPromptVerdict(
		matching,
		"harness-original-v2.1.280.md",
		"",
		errors.New("no API request reached the capture sink"),
	)
	if !warn || !strings.Contains(line, "CHECK FAILED") || strings.Contains(line, "DRIFT") ||
		strings.Contains(line, "matches") {
		t.Fatalf("capture-failure outcome = (%q, %v), want a distinct CHECK FAILED warning", line, warn)
	}

	line, warn = harnessPromptVerdict(matching, "harness-original-v2.1.280.md", "", errClaudeAbsent)
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

const (
	harnessCatalogLineA = " - The most recent Claude models are the Claude 5 family and Haiku 4.5. Model IDs — Fable 5.1: 'claude-fable-5-1', Opus 5: 'claude-opus-5', Sonnet 5: 'claude-sonnet-5', Haiku 4.5: 'claude-haiku-4-5-20251001'. When building AI applications, default to the latest and most capable Claude models."
	harnessCatalogLineB = " - The most recent Claude models are the Claude 5 family and Haiku 4.5. Model IDs — Fable 5.1: 'claude-fable-5-1', Opus 5.5: 'claude-opus-5-5', Sonnet 5: 'claude-sonnet-5', Haiku 4.5: 'claude-haiku-4-5-20251001'. When building AI applications, default to the latest and most capable Claude models."
	harnessDetailPrefix = "doctor: harness-prompt:   "
)

// harnessDetailFixture is a small opus-shaped prompt: the heading order ends
// `# Environment`, `# Context management`, `# Delivering work`, `# Corrections`,
// as the 2.1.278 opus baseline does.
const harnessDetailFixture = "x-anthropic-billing-header: cc_version=2.1.278.a1b; cc_entrypoint=sdk-cli;\n\n" +
	"=== SYSTEM BLOCK ===\n\nYou are Claude Code, Anthropic's official CLI for Claude.\n\n" +
	"# Doing tasks\n - Read the code before you change it.\n - Keep changes surgical.\n" +
	"```text\nexample: run the tests\n# not a heading inside a fence\n```\n" +
	"# Environment\n - This build is Claude Code version 2.1.278.\n" + harnessCatalogLineA + "\n" +
	"# Context management\n - Summarize when the context runs long.\n" +
	"# Delivering work\n - Report what landed and what did not.\n" +
	"# Corrections\nDo not use the Agent tool, workflows, or deep-research unless the user, a CLAUDE.md file, or a skill asks for it.\n"

func TestHarnessPromptDriftDetail(t *testing.T) {
	sectionsRemoved, _, _ := strings.Cut(harnessDetailFixture, "# Delivering work\n")
	var manyBaseline, manyCaptured strings.Builder
	var manyDetail []string
	for index := 1; index <= 25; index++ {
		fmt.Fprintf(&manyBaseline, "# S%02d\nKeep rule %d.\n", index, index)
		fmt.Fprintf(&manyCaptured, "# S%02d\nDrop rule %d.\n", index, index)
		if index <= 20 {
			manyDetail = append(manyDetail, fmt.Sprintf("%ssection changed: # S%02d", harnessDetailPrefix, index))
		}
	}
	manyDetail = append(manyDetail, harnessDetailPrefix+"… and 5 more")
	modelLine := harnessDetailPrefix + "model changed opus: claude-opus-5 → claude-opus-5-5"

	for _, testCase := range []struct {
		name       string
		baseline   string
		captured   HarnessCapture
		captureErr error
		wantWarn   bool
		wantLine   string
		wantDetail []string
	}{
		{
			name:     "catalog-only change",
			captured: HarnessCapture{Prompt: strings.Replace(harnessDetailFixture, harnessCatalogLineA, harnessCatalogLineB, 1)},
			wantLine: "doctor: harness-prompt: matches baseline fixture.md",
		},
		{
			name: "catalog entry added",
			captured: HarnessCapture{
				Prompt: strings.Replace(harnessDetailFixture, "Sonnet 5: 'claude-sonnet-5',", "Sonnet 5: 'claude-sonnet-5', Haiku 5: 'claude-haiku-5',", 1),
			},
			wantLine: "doctor: harness-prompt: matches baseline fixture.md",
		},
		{
			name: "catalog entry dropped",
			captured: HarnessCapture{
				Prompt: strings.Replace(harnessDetailFixture, " Fable 5.1: 'claude-fable-5-1',", "", 1),
			},
			wantLine: "doctor: harness-prompt: matches baseline fixture.md",
		},
		{
			name:       "catalog line removed",
			captured:   HarnessCapture{Prompt: strings.Replace(harnessDetailFixture, harnessCatalogLineA+"\n", "", 1)},
			wantWarn:   true,
			wantLine:   "doctor: harness-prompt: DRIFT",
			wantDetail: []string{harnessDetailPrefix + "section changed: # Environment"},
		},
		{
			name:     "model display name swapped in prose",
			captured: HarnessCapture{Prompt: strings.Replace(harnessDetailFixture, "Opus 5:", "Opus 6:", 1)},
			wantLine: "doctor: harness-prompt: matches baseline fixture.md",
		},
		{
			name: "model ID swapped in prose",
			captured: HarnessCapture{
				Prompt: strings.Replace(harnessDetailFixture, "'claude-opus-5'", "'claude-opus-6-1-20260101'", 1),
			},
			wantLine: "doctor: harness-prompt: matches baseline fixture.md",
		},
		{
			name:     "CLI version string changed",
			captured: HarnessCapture{Prompt: strings.ReplaceAll(harnessDetailFixture, "2.1.278", "2.1.280")},
			wantLine: "doctor: harness-prompt: matches baseline fixture.md",
		},
		{
			name:     "sections removed plus model change",
			captured: HarnessCapture{Prompt: sectionsRemoved, ResolvedModel: "claude-opus-5-5"},
			wantWarn: true,
			wantLine: "doctor: harness-prompt: DRIFT",
			wantDetail: []string{
				modelLine,
				harnessDetailPrefix + "section removed: # Delivering work",
				harnessDetailPrefix + "section removed: # Corrections",
			},
		},
		{
			name:       "one instruction sentence edited",
			captured:   HarnessCapture{Prompt: strings.Replace(harnessDetailFixture, "Keep changes surgical.", "Keep changes broad.", 1)},
			wantWarn:   true,
			wantLine:   "doctor: harness-prompt: DRIFT",
			wantDetail: []string{harnessDetailPrefix + "section changed: # Doing tasks"},
		},
		{
			name:       "section added",
			captured:   HarnessCapture{Prompt: harnessDetailFixture + "# X\nA new instruction.\n"},
			wantWarn:   true,
			wantLine:   "doctor: harness-prompt: DRIFT",
			wantDetail: []string{harnessDetailPrefix + "section added: # X"},
		},
		{
			name: "fenced example changed",
			captured: HarnessCapture{
				Prompt: strings.Replace(harnessDetailFixture, "example: run the tests", "example: skip the tests", 1),
			},
			wantWarn:   true,
			wantLine:   "doctor: harness-prompt: DRIFT",
			wantDetail: []string{harnessDetailPrefix + "section changed: # Doing tasks"},
		},
		{
			name:       "model differs, prompt same",
			captured:   HarnessCapture{Prompt: harnessDetailFixture, ResolvedModel: "claude-opus-5-5"},
			wantLine:   "doctor: harness-prompt: matches baseline fixture.md",
			wantDetail: []string{modelLine},
		},
		{
			name:       "capture fails",
			captured:   HarnessCapture{ResolvedModel: "claude-opus-5-5", CLIVersion: "2.1.280"},
			captureErr: errors.New("no API request reached the capture sink"),
			wantWarn:   true,
			wantLine:   "doctor: harness-prompt: CHECK FAILED to run (no API request reached the capture sink) — drift unknown",
		},
		{
			name:       "claude absent",
			captured:   HarnessCapture{ResolvedModel: "claude-opus-5-5"},
			captureErr: errClaudeAbsent,
			wantLine:   "doctor: harness-prompt: skipped (no Claude Code binary installed) — nothing to compare",
		},
		{
			name: "sections reordered",
			captured: HarnessCapture{Prompt: strings.Replace(
				harnessDetailFixture,
				"# Context management\n - Summarize when the context runs long.\n# Delivering work\n - Report what landed and what did not.\n",
				"# Delivering work\n - Report what landed and what did not.\n# Context management\n - Summarize when the context runs long.\n",
				1,
			)},
			wantWarn:   true,
			wantLine:   "doctor: harness-prompt: DRIFT",
			wantDetail: []string{harnessDetailPrefix + "section order changed"},
		},
		{
			name: "blank line added at a section end",
			captured: HarnessCapture{Prompt: strings.Replace(
				harnessDetailFixture,
				" - Summarize when the context runs long.\n",
				" - Summarize when the context runs long.\n\n",
				1,
			)},
			wantWarn:   true,
			wantLine:   "doctor: harness-prompt: DRIFT",
			wantDetail: []string{harnessDetailPrefix + "blank lines changed (no section text differs)"},
		},
		{
			name:       "many sections differ",
			baseline:   manyBaseline.String(),
			captured:   HarnessCapture{Prompt: manyCaptured.String()},
			wantWarn:   true,
			wantLine:   "doctor: harness-prompt: DRIFT",
			wantDetail: manyDetail,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			baseline := testCase.baseline
			if baseline == "" {
				baseline = harnessDetailFixture
			}
			sum := sha256.Sum256([]byte(normalizeHarnessPrompt(baseline)))
			line, warn := harnessPromptVerdict(
				hex.EncodeToString(sum[:]),
				"fixture.md",
				testCase.captured.Prompt,
				testCase.captureErr,
			)
			detail := harnessPromptDetail(
				"opus",
				"claude-opus-5",
				baseline,
				testCase.captured,
				testCase.captureErr,
				warn && strings.Contains(line, "DRIFT"),
			)
			if warn != testCase.wantWarn || !strings.HasPrefix(line, testCase.wantLine) {
				t.Fatalf("verdict = (%q, %v), want prefix %q warn=%v", line, warn, testCase.wantLine, testCase.wantWarn)
			}
			if strings.Join(detail, "\n") != strings.Join(testCase.wantDetail, "\n") {
				t.Fatalf("detail =\n%s\nwant\n%s", strings.Join(detail, "\n"), strings.Join(testCase.wantDetail, "\n"))
			}
		})
	}
}

func TestHarnessPromptSectionsKeyHeadings(t *testing.T) {
	canonical := "intro\n# A\none\n```\n# fenced\n```\n=== SYSTEM BLOCK ===\ntwo\n# A\nthree\n=== SYSTEM BLOCK ===\n"
	var keys []string
	for _, section := range harnessPromptSections(canonical) {
		keys = append(keys, section.key)
	}
	want := "(preamble)|# A|=== SYSTEM BLOCK ===|# A (2)|=== SYSTEM BLOCK === (2)"
	if strings.Join(keys, "|") != want {
		t.Fatalf("section keys = %q, want %q", strings.Join(keys, "|"), want)
	}
}

func TestPrintModelHarnessPromptDoctorPrintsDriftDetail(t *testing.T) {
	saved := HarnessCaptureOverride
	t.Cleanup(func() { HarnessCaptureOverride = saved })
	home := t.TempDir()
	opus := harnessPromptModels[1]
	stageModelHarnessPromptBaseline(t, home, opus, harnessDetailFixture, "opus-fixture.md")
	sectionsRemoved, _, _ := strings.Cut(harnessDetailFixture, "# Delivering work\n")
	HarnessCaptureOverride = func(context.Context, string, config.Config, string, string) (HarnessCapture, error) {
		return HarnessCapture{
			Prompt:        sectionsRemoved,
			ResolvedModel: "claude-opus-5-5",
			CLIVersion:    "2.1.280",
		}, nil
	}
	var stdout bytes.Buffer
	if code := printModelHarnessPromptDoctor(
		context.Background(),
		&stdout,
		home,
		config.Config{},
		opus,
		"",
	); code != 1 {
		t.Fatalf("code=%d, want 1\n%s", code, &stdout)
	}
	want := "\n" + harnessDetailPrefix + "model changed opus: claude-opus-5 → claude-opus-5-5\n" +
		harnessDetailPrefix + "section removed: # Delivering work\n" +
		harnessDetailPrefix + "section removed: # Corrections\n"
	output := stdout.String()
	drift := strings.Index(output, "doctor: harness-prompt: DRIFT")
	if drift < 0 || !strings.HasSuffix(output, want) || strings.Index(output, want) < drift {
		t.Fatalf("output=%s\nwant the DRIFT line followed by%s", output, want)
	}
}

// TestHarnessDoctorSonnetBaselineCatalogOnlyChangeIsSilent reads the shipped
// sonnet baseline and pins its catalog line to A on the baseline side and to B
// on the capture side, so the case survives a re-pin of the baseline file.
func TestHarnessDoctorSonnetBaselineCatalogOnlyChangeIsSilent(t *testing.T) {
	sonnet := harnessPromptModels[0]
	pin, err := harnessprompts.ReadPart("claude/baselines/" + sonnet.Stem + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(pin))
	if len(fields) != 2 {
		t.Fatalf("malformed shipped pin %q", pin)
	}
	shipped, err := harnessprompts.ReadPart("claude/baselines/" + fields[1])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(shipped), "\n")
	catalog := -1
	for index, line := range lines {
		if strings.Contains(line, "Model IDs — ") {
			catalog = index
		}
	}
	if catalog < 0 {
		t.Fatalf("shipped sonnet baseline %s carries no catalog line", fields[1])
	}
	lines[catalog] = harnessCatalogLineA
	baseline := strings.Join(lines, "\n")
	lines[catalog] = harnessCatalogLineB
	captured := strings.Join(lines, "\n")

	saved := HarnessCaptureOverride
	t.Cleanup(func() { HarnessCaptureOverride = saved })
	home := t.TempDir()
	stageModelHarnessPromptBaseline(t, home, sonnet, baseline, fields[1])
	HarnessCaptureOverride = func(context.Context, string, config.Config, string, string) (HarnessCapture, error) {
		return HarnessCapture{Prompt: captured, ResolvedModel: "claude-sonnet-5", CLIVersion: "2.1.280"}, nil
	}
	var stdout bytes.Buffer
	code := printModelHarnessPromptDoctor(context.Background(), &stdout, home, config.Config{}, sonnet, "")
	if code != 0 || !strings.Contains(stdout.String(), "doctor: harness-prompt: matches baseline "+fields[1]) ||
		strings.Contains(stdout.String(), "DRIFT") {
		t.Fatalf("catalog-only change warned: code=%d\n%s", code, &stdout)
	}
}
