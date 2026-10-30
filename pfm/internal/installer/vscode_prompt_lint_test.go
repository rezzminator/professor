package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// promptLintCall is one answer of the fake pfm the Prompt Lint command runs: stdout,
// stderr, exit code, and sleepSeconds to stay running (cancel, timeout).
type promptLintCall struct {
	stdout       string
	stderr       string
	exit         int
	sleepSeconds int
}

// promptLintScenario is the editor the Prompt Lint command runs in and what its fake
// pfm answers, one promptLintCall per run in order.
type promptLintScenario struct {
	file           string // path under HOME of the file open in the editor
	text           string
	calls          []promptLintCall
	noSystemPrompt bool // leaves the staged extension's prompt-lint.system.md out
	window         chooseWindow
	noPfm          bool
	// driver knobs, passed through PROFESSOR_PROMPT_LINT_SCENARIO
	runs, cancelAfterMs, cancelLensAfterMs, editBeforeFix int
	secondClick, editDuringRun, lensClick                 bool
	globsAfter                                            []string
	settings                                              map[string]any
}

type promptLintDiagnostic struct {
	StartLine    int    `json:"startLine"`
	EndLine      int    `json:"endLine"`
	EndCharacter int    `json:"endCharacter"`
	Severity     int    `json:"severity"`
	Message      string `json:"message"`
	Source       string `json:"source"`
	Code         string `json:"code"`
}

type promptLintLens struct {
	Line    int      `json:"line"`
	Title   string   `json:"title"`
	Command string   `json:"command"`
	Uris    []string `json:"uris"`
}

type promptLintRun struct {
	CollectionName string `json:"collectionName"`
	Messages       []struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"messages"`
	Progresses []struct {
		Title       string `json:"title"`
		Cancellable bool   `json:"cancellable"`
	} `json:"progresses"`
	Contexts       [][]any          `json:"contexts"`
	LensIdle       []promptLintLens `json:"lensIdle"`
	LensRunning    []promptLintLens `json:"lensRunning"`
	LensAfter      []promptLintLens `json:"lensAfter"`
	LensFires      int              `json:"lensFires"`
	GlobsFires     int              `json:"globsFires"`
	LensAfterGlobs []promptLintLens `json:"lensAfterGlobs"`
	LensSelector   struct {
		Scheme string `json:"scheme"`
	} `json:"lensSelector"`
	Diagnostics []promptLintDiagnostic `json:"diagnostics"`
	Actions     []struct {
		Title        string   `json:"title"`
		Kind         string   `json:"kind"`
		Codes        []string `json:"codes"`
		Replacements []struct {
			StartLine    int    `json:"startLine"`
			EndLine      int    `json:"endLine"`
			EndCharacter int    `json:"endCharacter"`
			Text         string `json:"text"`
		} `json:"replacements"`
	} `json:"actions"`
}

// promptLintPfmRun is what the fake pfm saw on one run: its argv and its stdin.
type promptLintPfmRun struct {
	args  []string
	stdin string
}

// runVSCodePromptLint drives professor.promptLint under node over a fake
// ~/.local/bin/pfm, from a copy of the extension at ~/ext beside the
// prompt-lint.system.md pfm install builds, and returns what the extension
// showed and every pfm run.
func runVSCodePromptLint(t *testing.T, scenario promptLintScenario) (promptLintRun, []promptLintPfmRun, string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	data := filepath.Join(home, "fake-pfm")
	for _, dir := range []string{bin, data} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	extension := filepath.Join(home, "ext")
	if err := os.CopyFS(extension, os.DirFS(filepath.Join("assets", "vscode", "professor"))); err != nil {
		t.Fatal(err)
	}
	if !scenario.noSystemPrompt {
		writeFixture(t, filepath.Join(extension, vscodeSystemPromptName), "# Prompt Quality\n")
	}
	for index, call := range scenario.calls {
		n := index + 1
		files := map[string]string{
			fmt.Sprintf("out.%d", n): call.stdout, fmt.Sprintf("err.%d", n): call.stderr,
			fmt.Sprintf("exit.%d", n): fmt.Sprint(call.exit), fmt.Sprintf("sleep.%d", n): fmt.Sprint(call.sleepSeconds),
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(data, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Each run logs its argv one argument per line and its stdin, then answers; a sleeping
	// run execs sleep, so a kill of pfm's pid ends it (no orphan holding stdout open).
	script := `#!/bin/sh
d="$HOME/fake-pfm"
n=$(( $(cat "$d/count" 2>/dev/null || echo 0) + 1 ))
echo "$n" > "$d/count"
for arg in "$@"; do printf '%s\n' "$arg"; done > "$d/args.$n"
cat > "$d/stdin.$n"
cat "$d/out.$n" 2>/dev/null
cat "$d/err.$n" >&2 2>/dev/null
if [ "$(cat "$d/sleep.$n" 2>/dev/null || echo 0)" != 0 ]; then exec sleep "$(cat "$d/sleep.$n")"; fi
exit "$(cat "$d/exit.$n" 2>/dev/null || echo 64)"
`
	if !scenario.noPfm {
		if err := testjail.WriteExecutable(filepath.Join(bin, "pfm"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(map[string]any{
		"remoteName": scenario.window.remoteName, "extensionKind": scenario.window.kind,
		"file": scenario.file, "text": scenario.text, "settings": scenario.settings,
		"runs": scenario.runs, "cancelAfterMs": scenario.cancelAfterMs, "editBeforeFix": scenario.editBeforeFix,
		"secondClick": scenario.secondClick, "editDuringRun": scenario.editDuringRun, "lensClick": scenario.lensClick,
		"cancelLensAfterMs": scenario.cancelLensAfterMs, "globsAfter": scenario.globsAfter,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := runVSCodeExtensionDriverFrom(
		t, filepath.Join(extension, "extension.js"), "promptLint", home,
		[]string{"PROFESSOR_PROMPT_LINT_SCENARIO=" + string(raw)},
	)
	var run promptLintRun
	if err := json.Unmarshal(output, &run); err != nil {
		t.Fatalf("decode promptLint driver output: %v: %s", err, output)
	}
	var runs []promptLintPfmRun
	for n := 1; ; n++ {
		args, err := os.ReadFile(filepath.Join(data, fmt.Sprintf("args.%d", n)))
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		stdin, err := os.ReadFile(filepath.Join(data, fmt.Sprintf("stdin.%d", n)))
		if err != nil {
			t.Fatal(err)
		}
		runs = append(
			runs,
			promptLintPfmRun{strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"), string(stdin)},
		)
	}
	return run, runs, home
}

const promptLintSkill = "---\nname: demo\n---\n\n# Demo\n\nOn 2026-05-19 we changed this.\nNEVER be vague."

// promptLintEnvelope is pfm headless exec --output-format json's answer carrying these findings.
func promptLintEnvelope(findings string) string {
	return `{"engine":"claude","model":"claude-opus-5-5","effort":"medium","result":"","structured_output":{"findings":` +
		findings + `},"total_cost_usd":0.1,"exit_code":0,"is_error":false,"timeout":false}` + "\n"
}

func promptLintMessage(t *testing.T, run promptLintRun, kind string, fragments ...string) {
	t.Helper()
	if len(run.Messages) != 1 || run.Messages[0].Kind != kind {
		t.Fatalf("messages = %+v, want one %s message", run.Messages, kind)
	}
	for _, fragment := range fragments {
		if !strings.Contains(run.Messages[0].Message, fragment) {
			t.Errorf("%s message %q lacks %q", kind, run.Messages[0].Message, fragment)
		}
	}
}

// TestVSCodePromptLintMapsFindingsOntoTheProblemsPanel: a run sends the
// numbered buffer through the one pfm argv, and each finding becomes a
// Prompt Lint diagnostic at its lines and severity; an out-of-range line is
// clamped and says so; an invalid finding is an error diagnostic, never
// dropped; a rewrite is a quick fix replacing exactly its lines.
func TestVSCodePromptLintMapsFindingsOntoTheProblemsPanel(t *testing.T) {
	t.Parallel()
	findings := `[
	 {"line":7,"endLine":7,"severity":"info","rule":"time-narration","message":"Dated narration.","rewrite":"This holds."},
	 {"line":8,"endLine":8,"severity":"warning","rule":"aggressive-emphasis","message":"Shouting."},
	 {"line":2,"endLine":3,"severity":"error","rule":"broken-frontmatter","message":"Frontmatter.","rewrite":"name: demo\ndescription: x\n"},
	 {"line":40,"endLine":44,"severity":"info","rule":"past-the-end","message":"Beyond.","rewrite":"never applied"},
	 {"line":1,"endLine":1,"severity":"fatal","rule":"x","message":"bad severity"},
	 {"line":"2","endLine":2,"severity":"error","rule":"x","message":"string line"}]`
	run, runs, home := runVSCodePromptLint(t, promptLintScenario{
		file:     "project/skills/demo/SKILL.md",
		text:     promptLintSkill,
		calls:    []promptLintCall{{stdout: promptLintEnvelope(findings)}},
		settings: map[string]any{"showInfo": true},
	})
	if len(runs) != 1 {
		t.Fatalf("pfm ran %d times, want 1", len(runs))
	}
	extension := filepath.Join(home, "ext")
	wantArgs := []string{
		"headless",
		"exec",
		"--engine",
		"claude",
		"--model",
		"opus",
		"--effort",
		"medium",
		"--sealed",
		"--system-file",
		filepath.Join(extension, vscodeSystemPromptName),
		"--schema",
		filepath.Join(extension, "prompt-lint.schema.json"),
		"--output-format",
		"json",
		"--timeout",
		"900",
	}
	if !slices.Equal(runs[0].args, wantArgs) {
		t.Errorf("pfm argv =\n%q\nwant\n%q", runs[0].args, wantArgs)
	}
	for _, want := range []string{"===== TARGET: project/skills/demo/SKILL.md =====\n1\t---\n2\tname: demo\n", "\n8\tNEVER be vague.\n===== END TARGET ====="} {
		if !strings.Contains(runs[0].stdin, want) {
			t.Errorf("pfm stdin lacks %q:\n%s", want, runs[0].stdin)
		}
	}
	if run.CollectionName != "professor-prompt-lint" {
		t.Errorf("diagnostic collection = %q, want professor-prompt-lint", run.CollectionName)
	}
	if len(run.Progresses) != 1 || !run.Progresses[0].Cancellable {
		t.Errorf("progress = %+v, want one cancellable notification", run.Progresses)
	}
	promptLintMessage(
		t,
		run,
		"info",
		"6 findings in project/skills/demo/SKILL.md",
		"1 errors",
		"1 warnings",
		"2 info",
		"2 invalid",
	)
	want := []promptLintDiagnostic{
		{6, 6, len("On 2026-05-19 we changed this."), 2, "Dated narration.", "Prompt Lint", "time-narration"},
		{7, 7, len("NEVER be vague."), 1, "Shouting.", "Prompt Lint", "aggressive-emphasis"},
		{1, 2, len("---"), 0, "Frontmatter.", "Prompt Lint", "broken-frontmatter"},
		{
			7,
			7,
			len("NEVER be vague."),
			2,
			"Beyond. (Claude cited lines 40–44; shown at 8–8)",
			"Prompt Lint",
			"past-the-end",
		},
	}
	if len(run.Diagnostics) != 6 || !reflect.DeepEqual(run.Diagnostics[:4], want) {
		t.Fatalf("diagnostics =\n%+v\nwant the first four\n%+v", run.Diagnostics, want)
	}
	invalid := run.Diagnostics[4]
	if invalid.Code != "invalid-finding" || invalid.Severity != 0 || invalid.StartLine != 0 ||
		!strings.Contains(invalid.Message, `severity "fatal" is not error, warning or info`) {
		t.Errorf("invalid finding surfaced as %+v", invalid)
	}
	if len(run.Actions) != 2 {
		t.Fatalf("quick fixes = %+v, want the two in-range rewrites", run.Actions)
	}
	for index, want := range []struct {
		code             string
		start, end, char int
		text             string
	}{
		{"time-narration", 6, 6, len("On 2026-05-19 we changed this."), "This holds."},
		{"broken-frontmatter", 1, 2, len("---"), "name: demo\ndescription: x"},
	} {
		action := run.Actions[index]
		if action.Title != "Apply Prompt Lint rewrite" || action.Kind != "quickfix" ||
			!slices.Equal(action.Codes, []string{want.code}) ||
			len(action.Replacements) != 1 {
			t.Fatalf("quick fix %d = %+v, want Apply Prompt Lint rewrite for %s", index, action, want.code)
		}
		got := action.Replacements[0]
		if got.StartLine != want.start || got.EndLine != want.end || got.EndCharacter != want.char ||
			got.Text != want.text {
			t.Errorf(
				"quick fix %d edit = %+v, want lines %d-%d (to char %d) -> %q",
				index,
				got,
				want.start,
				want.end,
				want.char,
				want.text,
			)
		}
	}
	if wantContexts := [][]any{{"setContext", "professor.pfmHere", true}}; !reflect.DeepEqual(
		run.Contexts,
		wantContexts,
	) {
		t.Errorf("activate set contexts %v, want %v", run.Contexts, wantContexts)
	}
}

// TestVSCodePromptLintHidesInfoByDefault: with professor.promptLint.showInfo
// off (its default) an info finding gets no diagnostic and no quick fix, and
// the summary counts it as hidden; errors, warnings and invalid findings stay.
func TestVSCodePromptLintHidesInfoByDefault(t *testing.T) {
	t.Parallel()
	findings := `[
	 {"line":7,"endLine":7,"severity":"info","rule":"time-narration","message":"Dated narration.","rewrite":"This holds."},
	 {"line":8,"endLine":8,"severity":"warning","rule":"aggressive-emphasis","message":"Shouting.","rewrite":"Be specific."},
	 {"line":1,"endLine":1,"severity":"info","rule":"x","message":42}]`
	run, _, _ := runVSCodePromptLint(t, promptLintScenario{
		file:  "project/skills/demo/SKILL.md",
		text:  promptLintSkill,
		calls: []promptLintCall{{stdout: promptLintEnvelope(findings)}},
	})
	promptLintMessage(
		t,
		run,
		"info",
		"3 findings",
		"0 errors",
		"1 warnings",
		"1 info hidden (professor.promptLint.showInfo)",
		"1 invalid",
	)
	codes := []string{}
	for _, diagnostic := range run.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	if !slices.Equal(codes, []string{"aggressive-emphasis", "invalid-finding"}) {
		t.Errorf("diagnostic codes = %v, want the warning and the invalid finding only", codes)
	}
	if len(run.Actions) != 1 || !slices.Equal(run.Actions[0].Codes, []string{"aggressive-emphasis"}) {
		t.Errorf("quick fixes = %+v, want only the warning's rewrite", run.Actions)
	}
}

// TestVSCodePromptLintNeverRendersAFailureAsAbsence: every failed run is
// one error naming its cause and pfm's stderr, leaves no diagnostics, and a
// clean run says "no findings" outright.
func TestVSCodePromptLintNeverRendersAFailureAsAbsence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		call      promptLintCall
		kind      string
		fragments []string
	}{
		{"a clean run says no findings", promptLintCall{stdout: promptLintEnvelope(`[]`)}, "info", []string{"no findings in project/agents/demo.md"}},
		{
			"a non-zero exit shows its code and stderr",
			promptLintCall{exit: 4, stderr: "pfm headless: claude binary MISSING (claude)\n"},
			"error",
			[]string{"exited 4", "claude binary MISSING"},
		},
		{
			"pfm's own timeout says which setting",
			promptLintCall{exit: 3, stderr: "pfm headless: context deadline exceeded\n"},
			"error",
			[]string{"did not finish within 900 s (professor.promptLint.timeout)", "deadline exceeded"},
		},
		{
			"an answer off the schema says so",
			promptLintCall{exit: 5, stderr: "pfm headless: structured output missing or invalid\n"},
			"error",
			[]string{"did not match prompt-lint.schema.json", "structured output missing"},
		},
		{"unparsable output is an error", promptLintCall{stdout: "not json", stderr: "warn: x\n"}, "error", []string{"unparsable JSON", "warn: x"}},
		{"JSON without findings is an error", promptLintCall{stdout: `{"result":"hi"}`}, "error", []string{"without a structured_output.findings list"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run, runs, _ := runVSCodePromptLint(
				t,
				promptLintScenario{
					file:  "project/agents/demo.md",
					text:  promptLintSkill,
					calls: []promptLintCall{tc.call},
				},
			)
			if len(runs) != 1 {
				t.Fatalf("pfm ran %d times, want 1", len(runs))
			}
			promptLintMessage(t, run, tc.kind, tc.fragments...)
			if len(run.Diagnostics) != 0 {
				t.Errorf("diagnostics = %+v, want none", run.Diagnostics)
			}
		})
	}
}

// TestVSCodePromptLintRefusals: a missing system prompt, a second click on a running
// file, a file no glob matches and a window without pfm each refuse with
// why; a failure after a good run clears the old findings and says so.
func TestVSCodePromptLintRefusals(t *testing.T) {
	t.Parallel()
	good := promptLintCall{
		stdout: promptLintEnvelope(`[{"line":8,"endLine":8,"severity":"warning","rule":"r","message":"m"}]`),
	}
	t.Run("a missing system prompt is an error and runs nothing", func(t *testing.T) {
		t.Parallel()
		run, runs, home := runVSCodePromptLint(
			t,
			promptLintScenario{file: "p/CLAUDE.md", text: promptLintSkill, noSystemPrompt: true},
		)
		promptLintMessage(
			t,
			run,
			"error",
			filepath.Join(home, "ext", vscodeSystemPromptName),
			"ENOENT",
			"pfm install",
		)
		if len(runs) != 0 {
			t.Errorf("pfm ran %d times without its system prompt", len(runs))
		}
	})
	t.Run("a second click while running is refused", func(t *testing.T) {
		t.Parallel()
		run, runs, _ := runVSCodePromptLint(
			t,
			promptLintScenario{
				file:        "p/SKILL.md",
				text:        promptLintSkill,
				calls:       []promptLintCall{good},
				secondClick: true,
			},
		)
		if len(runs) != 1 || len(run.Messages) != 2 || run.Messages[0].Kind != "warning" ||
			!strings.Contains(run.Messages[0].Message, "p/SKILL.md is already being analyzed") {
			t.Errorf("runs %d, messages %+v: want one run and the second click refused first", len(runs), run.Messages)
		}
	})
	t.Run("the lens analyzes its own editor, active or not", func(t *testing.T) {
		t.Parallel()
		run, runs, _ := runVSCodePromptLint(
			t,
			promptLintScenario{
				file:      "p/SKILL.md",
				text:      promptLintSkill,
				calls:     []promptLintCall{good},
				lensClick: true,
			},
		)
		promptLintMessage(t, run, "info", "1 findings in p/SKILL.md")
		if len(runs) != 1 || len(run.Diagnostics) != 1 {
			t.Errorf("runs %d, diagnostics %+v: want one run and its finding", len(runs), run.Diagnostics)
		}
	})
	t.Run("a file no glob matches is refused", func(t *testing.T) {
		t.Parallel()
		run, runs, _ := runVSCodePromptLint(t, promptLintScenario{file: "p/README.md", text: promptLintSkill})
		promptLintMessage(t, run, "warning", "p/README.md matches no professor.promptLint.globs pattern")
		if len(runs) != 0 {
			t.Errorf("pfm ran %d times", len(runs))
		}
	})
	t.Run("a remote window without pfm is refused", func(t *testing.T) {
		t.Parallel()
		run, runs, _ := runVSCodePromptLint(
			t,
			promptLintScenario{
				file:   "p/SKILL.md",
				text:   promptLintSkill,
				window: chooseWindow{"ssh-remote", 2},
				noPfm:  true,
			},
		)
		promptLintMessage(t, run, "warning", "Prompt Lint runs no model here", "has no")
		if len(runs) != 0 {
			t.Errorf("pfm ran %d times", len(runs))
		}
	})
	t.Run("a failure after a good run clears its findings and says so", func(t *testing.T) {
		t.Parallel()
		run, _, _ := runVSCodePromptLint(t, promptLintScenario{
			file:  "p/SKILL.md",
			text:  promptLintSkill,
			runs:  2,
			calls: []promptLintCall{good, {exit: 4, stderr: "boom\n"}},
		})
		if len(run.Messages) != 2 || run.Messages[1].Kind != "error" ||
			!strings.Contains(run.Messages[1].Message, "earlier Prompt Lint findings were cleared") {
			t.Errorf("messages = %+v, want the failure to say the old findings were cleared", run.Messages)
		}
		if len(run.Diagnostics) != 0 {
			t.Errorf("diagnostics = %+v, want none after the failed run", run.Diagnostics)
		}
	})
	t.Run("an edit during the run asks for a rerun", func(t *testing.T) {
		t.Parallel()
		run, _, _ := runVSCodePromptLint(
			t,
			promptLintScenario{
				file:          "p/SKILL.md",
				text:          promptLintSkill,
				calls:         []promptLintCall{good},
				editDuringRun: true,
			},
		)
		promptLintMessage(t, run, "warning", "1 findings", "changed during the analysis: rerun Prompt Lint")
		if len(run.Diagnostics) != 1 {
			t.Errorf("diagnostics = %+v, want the run's one finding applied", run.Diagnostics)
		}
	})
	t.Run("a quick fix whose lines changed is withdrawn", func(t *testing.T) {
		t.Parallel()
		fix := promptLintCall{
			stdout: promptLintEnvelope(
				`[{"line":8,"endLine":8,"severity":"warning","rule":"r","message":"m","rewrite":"Be specific."}]`,
			),
		}
		run, _, _ := runVSCodePromptLint(
			t,
			promptLintScenario{
				file:          "p/SKILL.md",
				text:          promptLintSkill,
				calls:         []promptLintCall{fix},
				editBeforeFix: 8,
			},
		)
		if len(run.Actions) != 0 {
			t.Errorf("quick fixes = %+v, want none over edited lines", run.Actions)
		}
	})
}

// TestVSCodePromptLintCancelKillsPfm: cancelling the progress kills the
// running pfm at once and says the analysis was cancelled.
func TestVSCodePromptLintCancelKillsPfm(t *testing.T) {
	t.Parallel()
	start := time.Now()
	run, runs, _ := runVSCodePromptLint(t, promptLintScenario{
		file: "p/SKILL.md", text: promptLintSkill, calls: []promptLintCall{{sleepSeconds: 60}}, cancelAfterMs: 300,
	})
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("cancel took %s: pfm was not killed", elapsed)
	}
	if len(runs) != 1 {
		t.Fatalf("pfm ran %d times, want 1", len(runs))
	}
	promptLintMessage(t, run, "info", "analysis of p/SKILL.md cancelled")
}

// TestVSCodePromptLintLens: a prompt file gets one lens at line 1 that runs
// Prompt Lint with its own uri; a file no glob matches and a remote window
// without pfm get none.
func TestVSCodePromptLintLens(t *testing.T) {
	t.Parallel()
	t.Run("a prompt file gets one idle lens", func(t *testing.T) {
		t.Parallel()
		run, _, home := runVSCodePromptLint(
			t,
			promptLintScenario{
				file:  "p/SKILL.md",
				text:  promptLintSkill,
				calls: []promptLintCall{{stdout: promptLintEnvelope(`[]`)}},
			},
		)
		want := []promptLintLens{
			{
				0,
				"$(checklist) Prompt Lint",
				"professor.promptLint",
				[]string{"file://" + filepath.Join(home, "p", "SKILL.md")},
			},
		}
		if !reflect.DeepEqual(run.LensIdle, want) || !reflect.DeepEqual(run.LensAfter, want) {
			t.Errorf("lenses before %+v, after %+v, want %+v", run.LensIdle, run.LensAfter, want)
		}
		if run.LensSelector.Scheme != "file" {
			t.Errorf("lens provider selector = %+v, want scheme file", run.LensSelector)
		}
	})
	t.Run("a file no glob matches gets none", func(t *testing.T) {
		t.Parallel()
		run, _, _ := runVSCodePromptLint(t, promptLintScenario{file: "p/README.md", text: promptLintSkill})
		if len(run.LensIdle) != 0 {
			t.Errorf("lenses = %+v, want none", run.LensIdle)
		}
	})
	t.Run("a globs change refreshes the lens on an open file", func(t *testing.T) {
		t.Parallel()
		run, _, _ := runVSCodePromptLint(t, promptLintScenario{
			file: "p/README.md", text: promptLintSkill, calls: []promptLintCall{{stdout: promptLintEnvelope(`[]`)}},
			globsAfter: []string{"**/README.md"},
		})
		if len(run.LensIdle) != 0 || run.GlobsFires != 1 ||
			len(run.LensAfterGlobs) != 1 || run.LensAfterGlobs[0].Command != "professor.promptLint" {
			t.Errorf(
				"lenses before %+v, announced %d, after %+v: want none, one announcement, then the Prompt Lint lens",
				run.LensIdle,
				run.GlobsFires,
				run.LensAfterGlobs,
			)
		}
	})
	t.Run("a remote window without pfm gets none", func(t *testing.T) {
		t.Parallel()
		run, _, _ := runVSCodePromptLint(
			t,
			promptLintScenario{
				file:   "p/SKILL.md",
				text:   promptLintSkill,
				window: chooseWindow{"ssh-remote", 2},
				noPfm:  true,
			},
		)
		if len(run.LensIdle) != 0 {
			t.Errorf("lenses = %+v, want none", run.LensIdle)
		}
	})
}

// TestVSCodePromptLintLensCancel: while a run goes the lens reads
// Prompt Lint…/Cancel with the cancelPromptLint command and refreshes on a timer;
// running that command kills the fake pfm and reports a cancel, never a
// timeout; the lens is idle again afterwards.
func TestVSCodePromptLintLensCancel(t *testing.T) {
	t.Parallel()
	start := time.Now()
	run, runs, home := runVSCodePromptLint(t, promptLintScenario{
		file: "p/SKILL.md", text: promptLintSkill, calls: []promptLintCall{{sleepSeconds: 60}}, cancelLensAfterMs: 1500,
	})
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("cancel took %s: pfm was not killed", elapsed)
	}
	if len(runs) != 1 {
		t.Fatalf("pfm ran %d times, want 1", len(runs))
	}
	uri := "file://" + filepath.Join(home, "p", "SKILL.md")
	if len(run.LensRunning) != 1 || run.LensRunning[0].Command != "professor.cancelPromptLint" ||
		!strings.HasPrefix(run.LensRunning[0].Title, "$(sync~spin) Prompt Lint… ") ||
		!strings.HasSuffix(
			run.LensRunning[0].Title,
			"s · Cancel",
		) || !slices.Equal(run.LensRunning[0].Uris, []string{uri}) {
		t.Errorf(
			"running lens = %+v, want Prompt Lint… Ns · Cancel running professor.cancelPromptLint on %s",
			run.LensRunning,
			uri,
		)
	}
	promptLintMessage(t, run, "info", "analysis of p/SKILL.md cancelled")
	if len(run.LensAfter) != 1 || run.LensAfter[0].Command != "professor.promptLint" {
		t.Errorf("lens after the run = %+v, want the idle Prompt Lint lens", run.LensAfter)
	}
	// run start, the one-second tick, run end.
	if run.LensFires < 3 {
		t.Errorf("lens announced %d changes, want at least 3 (start, tick, end)", run.LensFires)
	}
}

// TestVSCodePromptLintManifestAndAssets: the command reads "Professor: Prompt
// Lint" in the palette (no icon), no editor/title entry carries it,
// cancelPromptLint is not contributed, the settings carry the settled
// defaults, the prompt and schema are staged beside prompt-lint.js, and the schema
// is one pfm's validator accepts and holds a finding to.
func TestVSCodePromptLintManifestAndAssets(t *testing.T) {
	t.Parallel()
	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Command  string `json:"command"`
		Title    string `json:"title"`
		Category string `json:"category"`
		Icon     string `json:"icon"`
		When     string `json:"when"`
		Group    string `json:"group"`
	}
	var manifest struct {
		Contributes struct {
			Commands []entry `json:"commands"`
			Menus    struct {
				CommandPalette []entry         `json:"commandPalette"`
				EditorTitle    json.RawMessage `json:"editor/title"`
			} `json:"menus"`
			Configuration struct {
				Properties map[string]struct {
					Default any `json:"default"`
				} `json:"properties"`
			} `json:"configuration"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	const when = "!remoteName || professor.pfmHere"
	want := entry{Command: "professor.promptLint", Title: "Prompt Lint", Category: "Professor"}
	if !slices.Contains(manifest.Contributes.Commands, want) {
		t.Errorf("commands %+v lack %+v", manifest.Contributes.Commands, want)
	}
	if manifest.Contributes.Menus.EditorTitle != nil {
		t.Errorf(
			"manifest has an editor/title entry %s; the Prompt Lint link is a CodeLens",
			manifest.Contributes.Menus.EditorTitle,
		)
	}
	for _, command := range manifest.Contributes.Commands {
		if command.Command == "professor.cancelPromptLint" {
			t.Errorf("professor.cancelPromptLint is contributed, so the palette lists it: %+v", command)
		}
	}
	if !slices.Contains(
		manifest.Contributes.Menus.CommandPalette,
		entry{Command: "professor.promptLint", When: when},
	) {
		t.Errorf("commandPalette %+v lacks professor.promptLint", manifest.Contributes.Menus.CommandPalette)
	}
	defaults := map[string]any{
		"professor.promptLint.model":   "opus",
		"professor.promptLint.effort":  "medium",
		"professor.promptLint.timeout": 900.0,
		"professor.promptLint.globs": []any{
			"**/SKILL.md", "**/agents/*.md", "**/commands/**/*.md", "**/CLAUDE.md",
			"**/AGENTS.md", "**/*.prompt.md", "**/*.agent.md", "**/*.instructions.md",
		},
	}
	for key, want := range defaults {
		if got := manifest.Contributes.Configuration.Properties[key].Default; !reflect.DeepEqual(got, want) {
			t.Errorf("%s default = %v, want %v", key, got, want)
		}
	}

	files, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	staged := map[string]bool{}
	for _, file := range files {
		staged[file.path] = true
	}
	for _, name := range []string{"prompt-lint.js", "prompt-lint.md", "prompt-lint.schema.json"} {
		if !staged[vscodeExtensionSource+"/"+name] {
			t.Errorf("assetFiles() does not stage %s/%s", vscodeExtensionSource, name)
		}
	}

	schemaRaw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/prompt-lint.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("prompt-lint.schema.json does not resolve: %v", err)
	}
	for _, tc := range []struct {
		instance string
		valid    bool
	}{
		{`{"findings":[]}`, true},
		{`{"findings":[{"line":1,"endLine":2,"severity":"info","rule":"cut-test","message":"m","rewrite":"r"}]}`, true},
		{`{"findings":[{"line":0,"endLine":1,"severity":"info","rule":"x","message":"m"}]}`, false},
		{`{"findings":[{"line":1,"endLine":1,"severity":"fatal","rule":"x","message":"m"}]}`, false},
		{`{"findings":[{"line":1,"endLine":1,"severity":"info","rule":"Not Kebab","message":"m"}]}`, false},
		{`{"findings":[{"line":1,"severity":"info","rule":"x","message":"m"}]}`, false},
		{`{}`, false},
	} {
		var instance any
		if err := json.Unmarshal([]byte(tc.instance), &instance); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(instance); (err == nil) != tc.valid {
			t.Errorf("schema validate %s = %v, want valid %v", tc.instance, err, tc.valid)
		}
	}
}
