package update

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/professor"
)

// retiredThisRelease is one line per name v0.80.0, the imagine rename, the
// context-audit skill and the project-template prune retire, each in the form an adopter's file writes it, with the hit the scan
// owes it.
var retiredThisRelease = []struct{ line, name, kind, successor string }{
	{"spawn tracer-pro for the review", "tracer-pro", "agent", "tracer"},
	{"model: tracer-pro-max", "tracer-pro-max", "agent", "tracer"},
	{"use tracer-rr to read the repo", "tracer-rr", "agent", "clone-distiller"},
	{"run pfm chat reminder ls", "pfm chat reminder", "command", "pfm chat schedule"},
	{"node .claude/scripts/build-codex.mjs check", "build-codex.mjs", "path", "engine-sync codex build|check"},
	{"run pfm codex build . after an edit", "pfm codex", "command", "engine-sync codex"},
	{"pfm opencode check", "pfm opencode", "command", "engine-sync opencode"},
	{`"Bash(pfm codex:*)",`, "pfm codex", "command", "engine-sync codex"},
	{"arm it with /reminder 30d", "/reminder", "command", "/pfm:schedule"},
	{"run /tokens for the spend", "/tokens", "command", chatlogSuccessor},
	{"see .claude/skills/transcript/SKILL.md", "skills/transcript", "skill", chatlogSuccessor},
	{"load the transcript skill first", "transcript skill", "skill", chatlogSuccessor},
	{"type /close-out when done", "/close-out", "command", "/done-herewith"},
	{
		"tools: Read, mcp__sub-agent-compact__forward_handoff",
		"mcp__sub-agent-compact__",
		"tool",
		forwardCompactionSuccessor,
	},
	{"see .claude/skills/close-out/SKILL.md", "skills/close-out", "skill", "/done-herewith"},
	{"run pfm price --json", "pfm price", "command", "pfm model-cost"},
	{"run pfm transcript counts {path}", "pfm transcript counts", "command", chatlogSuccessor},
	{"run pfm transcript types {path} --lines 4-9", "pfm transcript types", "command", chatlogSuccessor},
	{"cost it with pfm tokens --timeline {path}", "pfm tokens --timeline", "command", chatlogSuccessor},
	{"see docs/agents/_index.md", "docs/agents", "path", "docs/_index.md and docs/standards.md"},
	{"type /ultimate-spec to spec first", "/ultimate-spec", "command", "/imagine"},
	{`Agent(subagent_type: "ultimate", prompt: "spec it")`, `subagent_type: "ultimate"`, "agent", "imaginator"},
	{"subagent_type: ultimate", "subagent_type: ultimate", "agent", "imaginator"},
	{`{"subagent_type": "ultimate"}`, `subagent_type": "ultimate"`, "agent", "imaginator"},
	{`{"subagent_type":"ultimate"}`, `subagent_type":"ultimate"`, "agent", "imaginator"},
	{"see .claude/agents/ultimate.md", "agents/ultimate.md", "agent", "imaginator"},
	{"an `ultimate` agent specs it", "`ultimate` agent", "agent", "imaginator"},
	{`Agent({subagent_type:"ultimate"})`, `subagent_type:"ultimate"`, "agent", "imaginator"},
	{`subagent_type: 'ultimate'`, `subagent_type: 'ultimate'`, "agent", "imaginator"},
	{`Task(subagent_type="ultimate")`, `subagent_type="ultimate"`, "agent", "imaginator"},
	{"the `ultimate` sub-agent specs it", "`ultimate` sub-agent", "agent", "imaginator"},
	{"the `ultimate` subagent specs it", "`ultimate` subagent", "agent", "imaginator"},
	{"load /callmeter:ultimate-visualization", "/callmeter:ultimate-visualization", "skill", "/imagine"},
	{"type /imagine:build to build it", "/imagine:build", "command", "/imagine"},
	{"type /imagine:build-fast to build it", "/imagine:build-fast", "command", "/imagine"},
	{"type /imagine:build-spec to spec it", "/imagine:build-spec", "command", "/flights:spec"},
	{"run /imagine:callmeter on the data", "/imagine:callmeter", "command", "/imagine"},
	{`Agent(subagent_type: "imagine", prompt: "build it")`, `subagent_type: "imagine"`, "agent", "imaginator"},
	{`Agent({subagent_type:"imagine"})`, `subagent_type:"imagine"`, "agent", "imaginator"},
	{`subagent_type: 'imagine'`, `subagent_type: 'imagine'`, "agent", "imaginator"},
	{"subagent_type: imagine", "subagent_type: imagine", "agent", "imaginator"},
	{`Task(subagent_type="imagine")`, `subagent_type="imagine"`, "agent", "imaginator"},
	{`{"subagent_type": "imagine"}`, `subagent_type": "imagine"`, "agent", "imaginator"},
	{`{"subagent_type":"imagine"}`, `subagent_type":"imagine"`, "agent", "imaginator"},
	{"see .claude/agents/imagine.md", "agents/imagine.md", "agent", "imaginator"},
	{"an `imagine` agent builds it", "`imagine` agent", "agent", "imaginator"},
	{"the `imagine` sub-agent builds it", "`imagine` sub-agent", "agent", "imaginator"},
	{"the `imagine` subagent builds it", "`imagine` subagent", "agent", "imaginator"},
	{"load the imagine-core skill", "imagine-core", "skill", "the imaginator agent"},
	{"run /imagine:evolve weekly", "/imagine:evolve", "command", ""},
	{"spawn imagine-historian to mine it", "imagine-historian", "agent", ""},
	{"run /context-meter for the budget", "/context-meter", "command", "/context-audit"},
	{"ask /mentor about funding", "/mentor", "command", ""},
	{"type /marketer audit", "/marketer", "command", ""},
	{"route it to /officer.", "/officer", "command", ""},
	{"code follows rules/writing-code.md", "writing-code", "path", "rules/coding.md"},
	{"per the `writing-code` rule", "writing-code", "path", "rules/coding.md"},
}

// TestRetiredNamesPinEveryNameThisReleaseRetires: a project file naming any
// of the names v0.80.0 and the imagine rename retire is reported with its
// successor, and the live neighbours of those names (their successors, the
// everyday word, a longer name, the live /ultimate skill) are no hit.
func TestRetiredNamesPinEveryNameThisReleaseRetires(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var content strings.Builder
	var want []professor.RetiredNameHit
	for index, retired := range retiredThisRelease {
		content.WriteString(retired.line + "\n")
		want = append(want, professor.RetiredNameHit{
			Path: "docs/runbook.md", Line: index + 1, Name: retired.name, Kind: retired.kind,
			Successor: retired.successor,
		})
	}
	writeRetiredFixture(t, root, map[string]string{"docs/runbook.md": content.String()})

	scan := ScanRetiredNames(root, nil)
	if !reflect.DeepEqual(scan.Hits, want) || len(scan.Failures) != 0 {
		t.Fatalf("hits =\n%+v\nfailures %+v\nwant\n%+v", scan.Hits, scan.Failures, want)
	}

	for _, line := range []string{
		"POST /tokens", "GET /tokens/{id}", "DELETE /reminder/:id",
		"POST `/tokens`", "an API endpoint /tokens/{id}",
		"pfm chat schedule ls and build-codex.mjsx",
		"spawn tracer and tracer-loom; tracer-product is a word",
		"pfm model-cost --json and pfm prices",
		"engine-sync codex build . and engine-sync opencode check; the pfm codexes and pfm codex-sync",
		"the price of the plan; a price list",
		"pfm tokens --since 1d and pfm transcript show",
		"pfm tokens --flight {dir} and pfm transcript agents {path}; pfm transcript locate {id}",
		"the transcript of the chat; read the transcript",
		"a close-out checklist for the week",
		"/pfm:schedule weekly and /done-herewith",
		"https://example.com/tokens and /tokenizer and /reminders and /remind",
		"super-rr and .claude/agents/tracer.md",
		"docs/_index.md, docs/standards.md and docs/agents-guide",
		"~/.claude/skills/chatlog/SKILL.md",
		"the ultimate goal; it ultimately works; the ultimate agent of change",
		"/ultimately and /ultimate-pro and /imagine:ultimate and /callmeter:ultimate",
		"run /ultimate; the ultimate skill loaded through the Skill tool",
		"Skill(skill: \"ultimate\") and `ultimate` skill and skill: ultimate",
		"templates/global/skills/ultimate/SKILL.md and .claude/skills/ultimate/SKILL.md",
		"code follows rules/coding.md, the `coding` rule; hand-writing-code and writing-codes",
		"/ultimate-visualization and /imagine/ultimate.md and commands/ultimate.md",
		"https://example.com/ultimate and https://example.com/ultimate-spec",
		"run /imagine to make it; /imagine: with a colon; /imagine:ultimate and /imagine/build.md",
		"templates/global/commands/imagine.md and https://example.com/imagine:build",
		"/imagine:builder, /imagine:build-fasts, /imagine:build-specs and /imagine:callmeters",
		"the imaginator agent and imaginator-core and imagine-cores and imagine-core2",
		`subagent_type: "imaginator" and subagent_type: "imagine-pro" and subagent_type: imagined`,
		"agents/imaginator.md and `imaginator` agent and `imaginator` sub-agent and imagine agents",
		`subagent_type: "ultimate-pro" and subagent_type: ultimately`,
		"agents/ultimate-pro.md and `ultimate-pro` agent and ultimate agents",
		"/context-audit and /context-meters; /mentorship, /marketers and /officers; a mentor, an officer",
	} {
		if hits := retiredNameHits("f", line); len(hits) != 0 {
			t.Errorf("line %q hit %+v, want none", line, hits)
		}
	}

	// The removed /imagine:build-spec description, as an adopter copied it: the
	// first /imagine:build sits inside /imagine:build-spec, and the scan goes on
	// past it to the one that ends the line.
	line := "/imagine:build-spec → imagine; building it → /imagine:build."
	var names []string
	for _, hit := range retiredNameHits("f", line) {
		names = append(names, hit.Name)
	}
	if want := []string{"/imagine:build", "/imagine:build-spec"}; !slices.Equal(names, want) {
		t.Errorf("line %q hit %q, want %q", line, names, want)
	}
}
