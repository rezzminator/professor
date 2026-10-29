package codexprompt

import (
	"strings"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// reviewTestModel is the model every rewrite below is pinned to, read from the
// one constant so this test cannot pass against a second literal.
var reviewTestModel = codeReviewDefaultModel

func reviewWant(effort string) string {
	return codexReviewShellCommand(reviewTestModel, effort)
}

func TestRewriteCodeReviewMapsEveryLevelAndLeavesEverythingElse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "low", in: "`/code-review low`", want: "`" + reviewWant("low") + "`"},
		{name: "medium", in: "`/code-review medium`", want: "`" + reviewWant("medium") + "`"},
		{name: "high", in: "`/code-review high`", want: "`" + reviewWant("high") + "`"},
		{name: "xhigh", in: "`/code-review xhigh`", want: "`" + reviewWant("xhigh") + "`"},
		{name: "max takes Codex's highest effort", in: "/code-review max", want: reviewWant("xhigh")},
		{name: "bare form takes the cheapest", in: "run /code-review now", want: "run " + reviewWant("low") + " now"},
		{
			name: "surrounding prose is untouched",
			in:   "Your last step is `/code-review low` over your own change: fix every finding.",
			want: "Your last step is `" + reviewWant("low") + "` over your own change: fix every finding.",
		},
		{
			name: "a flag with no codex twin leaves with the invocation",
			in:   "`/code-review high --fix` then report",
			want: "`" + reviewWant("high") + "` then report",
		},
		{
			name: "a bare invocation's flag leaves too",
			in:   "/code-review --comment",
			want: reviewWant("low"),
		},
		{
			name: "an unknown flag stays visible in the text",
			in:   "/code-review low --whatever",
			want: reviewWant("low") + " --whatever",
		},
		{
			name: "a word that merely starts with a level is not a level",
			in:   "/code-review lowish",
			want: reviewWant("low") + " lowish",
		},
		{name: "a longer command is not this one", in: "/code-review-x low", want: "/code-review-x low"},
		{name: "a namespaced command is not this one", in: "/code-review:foo", want: "/code-review:foo"},
		{name: "a path is not an invocation", in: "$CDOCS/code-review.md", want: "$CDOCS/code-review.md"},
		{
			name: "a URL is not an invocation",
			in:   "https://example.test/code-review low",
			want: "https://example.test/code-review low",
		},
		{name: "ultra takes Codex's highest effort too", in: "/code-review ultra", want: reviewWant("xhigh")},
		{name: "text without the command is returned as it is", in: "no command here", want: "no command here"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := RewriteCodeReview(testCase.in)
			if got != testCase.want {
				t.Fatalf("RewriteCodeReview(%q) = %q, want %q", testCase.in, got, testCase.want)
			}
			if again := RewriteCodeReview(got); again != got {
				t.Fatalf("RewriteCodeReview is not idempotent: second pass = %q, first = %q", again, got)
			}
		})
	}
}

// The lander sizes its own review effort from the diff and reviews the FLIGHT's
// whole diff, not one task's files. Its source line writes the slot form, which
// has to map to a flight-scoped prompt whose model_reasoning_effort is still the
// slot the surrounding prose tells the lander to fill — a baked effort would
// silently review a 40-file flight at the cheapest tier.
func TestRewriteCodeReviewSlotFormIsFlightScopedWithARunTimeEffort(t *testing.T) {
	got := RewriteCodeReview("Run `/code-review " + codeReviewSlot + "` over the flight's diff.")
	want := "Run `" + codexReviewShellCommand(reviewTestModel, codeReviewSlot) + "` over the flight's diff."
	if got != want {
		t.Fatalf("slot form = %q, want %q", got, want)
	}
	if !strings.Contains(got, `model_reasoning_effort="`+codeReviewSlot+`"`) {
		t.Fatalf("slot form baked an effort instead of leaving the slot: %q", got)
	}
	if !strings.Contains(got, codeReviewFlightPrompt) || strings.Contains(got, codeReviewPrompt) {
		t.Fatalf("slot form carries the task-scoped prompt, want the flight-scoped one: %q", got)
	}
	if again := RewriteCodeReview(got); again != got {
		t.Fatalf("slot rewrite is not idempotent: second pass = %q, first = %q", again, got)
	}
	level := RewriteCodeReview("/code-review low")
	if !strings.Contains(level, codeReviewPrompt) || strings.Contains(level, codeReviewSlot) {
		t.Fatalf("a written level must stay task-scoped at a baked effort: %q", level)
	}
}

func TestComposedCodexFleetPromptCarriesTheShellReview(t *testing.T) {
	prompt, err := FleetPrompt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, codeReviewCommand) {
		t.Fatalf("the composed Codex fleet prompt still spells %s", codeReviewCommand)
	}
	mapped := RewriteCodeReview("- Your last step is `" + codeReviewCommand + " low` over your own change.\n")
	if strings.Contains(mapped, codeReviewCommand) || !strings.Contains(mapped, reviewWant("low")) {
		t.Fatalf("the compose-time mapping left a fixture tail unmapped:\n%s", mapped)
	}
	claude, err := harnessprompts.Composed(t.TempDir(), pfmengine.MustLookup(pfmengine.Claude).LongName)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(claude), "codex review -c") {
		t.Fatal("the Claude fleet prompt spells Codex's review — the shared parts are no longer engine-neutral")
	}
}
