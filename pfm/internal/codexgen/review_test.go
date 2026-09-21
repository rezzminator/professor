package codexgen

import (
	"path/filepath"
	"strings"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// reviewTestModel is the model every rewrite below is pinned to: the tier
// map's own entry for the review alias, read from the map so this test cannot
// pass against a second literal that has drifted from it.
var reviewTestModel = defaultConfig().ModelMap[codeReviewModelAlias]

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
			got := rewriteCodeReview(testCase.in, nil)
			if got != testCase.want {
				t.Fatalf("rewriteCodeReview(%q) = %q, want %q", testCase.in, got, testCase.want)
			}
			if again := rewriteCodeReview(got, nil); again != got {
				t.Fatalf("rewriteCodeReview is not idempotent: second pass = %q, first = %q", again, got)
			}
		})
	}
}

// The model is the tier map's, so a project whose config maps the review alias
// somewhere else gets ITS model — and a caller with no map of its own (the
// fleet prompt, the global-role compiler) still gets the compiler default
// rather than an empty model codex-cli would reject.
func TestRewriteCodeReviewReadsTheModelFromTheTierMap(t *testing.T) {
	got := rewriteCodeReview("/code-review low", map[string]string{codeReviewModelAlias: "gpt-test-model"})
	if want := codexReviewShellCommand("gpt-test-model", "low"); got != want {
		t.Fatalf("rewrite with a mapped tier = %q, want %q", got, want)
	}
	if model := codeReviewModel(map[string]string{"sonnet": "other"}); model != reviewTestModel {
		t.Fatalf("model for a map without the review alias = %q, want the compiler default %q", model, reviewTestModel)
	}
	if reviewTestModel == "" {
		t.Fatal("the compiler's tier map has no entry for the review alias — the rewrite would emit an empty model")
	}
}

// Door 1: transformMarkdown, the transform every compiled project command,
// agent body, agent description and AGENTS.md passes through.
func TestTransformMarkdownRewritesCodeReview(t *testing.T) {
	got := transformMarkdown("Read CLAUDE.md, then `/code-review high`.", TransformOptions{
		ModelMap:          map[string]string{"opus": "gpt-frontier"},
		ReplaceClaudeFile: true,
	})
	want := "Read AGENTS.md, then `" + codexReviewShellCommand("gpt-frontier", "high") + "`."
	if got != want {
		t.Fatalf("transformMarkdown = %q, want %q", got, want)
	}
}

// Doors 1 and 2 from the files they wrote: a compiled project command, a
// compiled project role (body AND description), the compiled AGENTS.md, and a
// compiled machine-global role. A Codex artifact still spelling /code-review
// sends its seat to Codex's whole-branch /review.
func TestEveryCompiledCodexArtifactCarriesTheShellReview(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n\nClose with `/code-review low`.\n")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "dev.md"),
		"---\nname: dev\ndescription: Builds, then runs /code-review low.\ntools: Read\nmodel: sonnet\n---\n\n"+
			"Your last step is `/code-review high` over your own change.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "commands", "land.md"),
		"---\ndescription: Lands a change; closes with /code-review medium.\n---\n\nRun `/code-review max`.\n",
	)
	writeTestFile(
		t,
		filepath.Join(home, ".professor", "templates", "global", "agents", "gamma.md"),
		"---\nname: gamma\ndescription: Global role; reviews with /code-review low.\ntools: Read\nmodel: sonnet\n"+
			"---\n\nClose with `/code-review xhigh`.\n",
	)
	if result, err := Run(Options{Root: root, Home: home, Mode: ModeBuild}); err != nil || !result.OK {
		t.Fatalf("build: result=%#v err=%v", result, err)
	}
	if _, err := RunGlobalAgents(GlobalAgentsOptions{Home: home}); err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	cases := []struct {
		name    string
		path    string
		efforts []string
	}{
		{name: "AGENTS.md", path: filepath.Join(root, "AGENTS.md"), efforts: []string{"low"}},
		{
			name:    "project role",
			path:    filepath.Join(root, ".codex", "agents", "dev.toml"),
			efforts: []string{"low", "high"},
		},
		{
			name:    "project command",
			path:    filepath.Join(root, ".codex", "skills", "land", "SKILL.md"),
			efforts: []string{"medium", "xhigh"},
		},
		{
			name:    "global role",
			path:    filepath.Join(paths.GeneratedCodexAgentsDir(home), "gamma.toml"),
			efforts: []string{"low", "xhigh"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := string(mustReadTestFile(t, testCase.path))
			if strings.Contains(got, codeReviewCommand) {
				t.Fatalf("%s still spells %s — the seat would run Codex's whole-branch review:\n%s",
					testCase.path, codeReviewCommand, got)
			}
			for _, effort := range testCase.efforts {
				if !strings.Contains(got, reviewWant(effort)) {
					t.Fatalf("%s carries no %s review at effort %q:\n%s", testCase.path, "codex review -c", effort, got)
				}
			}
			if strings.HasSuffix(testCase.path, ".toml") {
				if err := validateTOML(got); err != nil {
					t.Fatalf("%s does not parse as TOML with the review command embedded: %v", testCase.path, err)
				}
			}
		})
	}
}

// A command's frontmatter is re-emitted as YAML, and the replacement carries
// double quotes: a quoted scalar has to come back quoted in a style that
// survives them, or the compiled command's frontmatter no longer parses and
// Codex loses the whole command.
func TestCompiledCommandFrontmatterStaysParseableAroundTheReview(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "commands", "quoted.md"),
		"---\ndescription: \"Lands it; closes with /code-review low.\"\n---\n\nBody.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "commands", "folded.md"),
		"---\ndescription: >-\n  Lands it; closes\n  with /code-review high.\n---\n\nBody.\n",
	)
	if result, err := Run(Options{Root: root, Home: home, Mode: ModeBuild}); err != nil || !result.OK {
		t.Fatalf("build: result=%#v err=%v", result, err)
	}
	for _, command := range []struct{ name, effort string }{{"quoted", "low"}, {"folded", "high"}} {
		t.Run(command.name, func(t *testing.T) {
			path := filepath.Join(root, ".codex", "skills", command.name, "SKILL.md")
			got := string(mustReadTestFile(t, path))
			fields, _, err := parseFrontmatter(got)
			if err != nil {
				t.Fatalf("%s no longer parses as frontmatter: %v\n%s", path, err, got)
			}
			if strings.Contains(fields["description"], codeReviewCommand) {
				t.Fatalf("%s description still spells %s: %q", path, codeReviewCommand, fields["description"])
			}
			if !strings.Contains(fields["description"], reviewWant(command.effort)) {
				t.Fatalf("%s description carries no %q review: %q", path, command.effort, fields["description"])
			}
		})
	}
}

// Door 3: the composed Codex fleet prompt — the bytes the installer stages,
// writes into developer_instructions and prepends to every compiled role, and
// the bytes doctor compares a config against. The shared tail stays
// engine-neutral ON DISK, which is what the Claude prompt proves.
func TestComposedCodexFleetPromptCarriesTheShellReview(t *testing.T) {
	prompt, err := FleetPrompt()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, codeReviewCommand) {
		t.Fatalf("the composed Codex fleet prompt still spells %s", codeReviewCommand)
	}
	if !strings.Contains(prompt, reviewWant("low")) {
		t.Fatalf("the composed Codex fleet prompt carries no scoped shell review:\n%s", prompt)
	}
	claude, err := harnessprompts.Composed(pfmengine.MustLookup(pfmengine.Claude).LongName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claude), codeReviewCommand+" low") {
		t.Fatalf(
			"the Claude fleet prompt lost %s low — the shared parts are no longer engine-neutral",
			codeReviewCommand,
		)
	}
	if strings.Contains(string(claude), "codex review -c") {
		t.Fatal("the Claude fleet prompt carries the Codex shell review — the mapping leaked into the shared parts")
	}
}
