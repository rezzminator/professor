package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

func writeRetiredFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestScanRetiredNamesReportsEachHitByPathAndLine: a retired name in an
// script and a JSON file is reported as path:line with its
// successor; a pinned file, pfm's baseline, the engine mirrors, .git,
// .worktrees, node_modules and a binary file are never scanned, while the
// hand-written .codex/config.toml is.
func TestScanRetiredNamesReportsEachHitByPathAndLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeRetiredFixture(t, root, map[string]string{
		"scripts/legacy.sh":            "#!/bin/sh\nclaude -p '/wave:refine the spec'\n# gitter Phase: JC-COMMIT\n",
		".claude/codex-build.json":     "{\n  \"skills\": [],\n  \"tools\": [\"mcp__chat__chat_inject\"]\n}\n",
		".codex/config.toml":           "[mcp]\nallow = \"chat_inject\"\n",
		"CLAUDE.md":                    "run /jc first\n",
		".professor/baseline.json":     "{\"files\": {\".claude/commands/pfm.md\": {}}}\n",
		"AGENTS.md":                    "/wave\n",
		"sub/AGENTS.md":                "/wave\n",
		".codex/agents/gitter.toml":    "JC-COMMIT\n",
		".opencode/agent/gitter.md":    "JC-COMMIT\n",
		".git/HEAD":                    "/wave\n",
		".worktrees/flight/scripts.sh": "/wave\n",
		"node_modules/pkg/index.js":    "\"/wave\"\n",
		"bin/tool":                     "\x00\x01/wave\n",
	})

	scan := ScanRetiredNames(root, map[string]bool{"CLAUDE.md": true})

	want := []professor.RetiredNameHit{
		{
			Path:      ".claude/codex-build.json",
			Line:      3,
			Name:      "mcp__chat__",
			Kind:      "tool",
			Successor: "mcp__chat-professor__*",
		},
		{
			Path: ".codex/config.toml", Line: 2, Name: "chat_inject", Kind: "tool",
			Successor: "mcp__chat-professor__call",
		},
		{Path: "scripts/legacy.sh", Line: 2, Name: "/wave", Kind: "command", Successor: flightsSuccessor},
		{Path: "scripts/legacy.sh", Line: 3, Name: "JC-COMMIT", Kind: "phase", Successor: "COMMIT"},
	}
	if !reflect.DeepEqual(scan.Hits, want) {
		t.Fatalf("hits =\n%+v\nwant\n%+v", scan.Hits, want)
	}
	if len(scan.Failures) != 0 {
		t.Fatalf("failures = %+v, want none", scan.Failures)
	}
}

// TestRetiredNamesNeverHitALiveNameSharingAPrefix pins the boundaries: a live
// command, agent, phase or tool that shares a retired name's prefix, and an
// ordinary path or URL containing one, is no hit; each retired form is.
func TestRetiredNamesNeverHitALiveNameSharingAPrefix(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"/pfm:release prepare --from ../live",
		"/pfm:workbench new",
		"run /pcm:update or /pcmx",
		"the /pfm CLI guide",
		"see ~/.claude/commands/pfm.md and $HOME/.claude/commands/pfm.md",
		".claude/commands/pcm.md",
		"/quality:md-forlint check",
		"mcp__chat-professor__call",
		"tools: Read, mcp__forward-compaction__forward_compaction, mcp__forward-compaction__look_back",
		"spawn super-rr",
		"import x from 'src/wave.js'; /waveform",
		"/jcx and /pfm:refresher and /qa:lively",
		"JC-COMMITTED is not a phase; DOCS-COMMIT and COMMIT are live",
		"xmono-architect mono-architects",
		"the /chat route",
		"https://example.com/wave and http://localhost:3000/jc",
		"/wave-something",
		"evidence: https://opencode.ai/docs/agents/ and [agents](https://opencode.ai/docs/agents)",
		"<https://example.com/docs/agents/API.md>",
		"subagent_type: documenter",
		"templates/global/agents/documenter.md and .claude/agents/documenter.md",
		"spawn the documenter agent",
		"the pfm guard script and a xpfm-guard.sh or guard-stamp.shx",
		"/random, /rndx and /rnd-seed; https://example.com/rnd",
	} {
		if hits := retiredNameHits("f", line); len(hits) != 0 {
			t.Errorf("line %q hit %+v, want none", line, hits)
		}
	}
	for line, name := range map[string]string{
		"/pfm:refresh is gone":             "/pfm:refresh",
		"run /wave":                        "/wave",
		"`/wave:refine`":                   "/wave",
		"(/jc:wave)":                       "/jc",
		"cat .claude/commands/pfm.md":      ".claude/commands/pfm.md",
		"\"mcp__chat__chat_ls\"":           "mcp__chat__",
		"/chat:save now":                   "/chat:",
		"agent: rr-super":                  "rr-super",
		".claude/agents/mono-architect.md": "mono-architect",
		"use /quality:forlint.":            "/quality:forlint",
		"Phase: JC-COMMIT.":                "JC-COMMIT",
		"/documenter:archive":              "/documenter",
		"skills/wave-builder/SKILL.md":     "wave-builder",
		"/qa:live":                         "/qa:live",
		"run /rnd new chain slug goal":     "/rnd",
		"dispatch it with /pcm audit all":  "/pcm",
		"(the user's `/pcm`)":              "/pcm",
		"(/rnd:hammer)":                    "/rnd",
		"x .claude/commands/pfm/references/audit-scopes.md": ".claude/commands/pfm/references/audit-scopes.md",
	} {
		hits := retiredNameHits("f", line)
		if len(hits) != 1 || hits[0].Name != name {
			t.Errorf("line %q hits %+v, want exactly %q", line, hits, name)
		}
	}
}

// TestRetiredNamesPinTheRemovedGuardScripts: a settings file or script that
// still names the removed PreToolUse guard scripts is reported as a retired
// path with no successor, in the form an adopter writes it.
func TestRetiredNamesPinTheRemovedGuardScripts(t *testing.T) {
	t.Parallel()
	for line, name := range map[string]string{
		`"command": "bash $CLAUDE_PROJECT_DIR/.claude/scripts/pfm-guard.sh"`: "pfm-guard.sh",
		"bash .claude/scripts/guard-stamp.sh --read":                         "guard-stamp.sh",
		"pfm-guard.sh": "pfm-guard.sh",
	} {
		want := []professor.RetiredNameHit{{Path: "f", Line: 1, Name: name, Kind: "path"}}
		if hits := retiredNameHits("f", line); !reflect.DeepEqual(hits, want) {
			t.Errorf("line %q hits %+v, want %+v", line, hits, want)
		}
	}
}

// TestScanRetiredNamesReportsAnUnreadableFileAsAFailure: a file the scan
// cannot read, and a root that does not exist, are failures naming the path
// and the error — never an empty, clean scan.
func TestScanRetiredNamesReportsAnUnreadableFileAsAFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeRetiredFixture(t, root, map[string]string{
		"scripts/locked.sh": "/wave\n",
		"scripts/open.sh":   "/jc\n",
	})
	open := func(path string) (io.ReadCloser, error) {
		if strings.HasSuffix(path, "locked.sh") {
			return nil, errors.New("open " + path + ": permission denied")
		}
		return os.Open(path)
	}

	scan := scanRetiredNamesWith(root, nil, open)

	if len(scan.Failures) != 1 || scan.Failures[0].Path != "scripts/locked.sh" ||
		!strings.Contains(scan.Failures[0].Error, "permission denied") {
		t.Fatalf("failures = %+v, want scripts/locked.sh permission denied", scan.Failures)
	}
	if len(scan.Hits) != 1 || scan.Hits[0].Path != "scripts/open.sh" {
		t.Fatalf("hits = %+v, want the readable file's /jc", scan.Hits)
	}

	missing := ScanRetiredNames(filepath.Join(root, "absent"), nil)
	if len(missing.Failures) != 1 || len(missing.Hits) != 0 {
		t.Fatalf("missing root scan = %+v, want one failure", missing)
	}
}

// TestRetiredPfmCommandReadsTheRetiredList: the dispatch of a retired `pfm
// codex|opencode` reads the name and successor from retiredNames, the one
// table; a command the table does not retire is no answer.
func TestRetiredPfmCommandReadsTheRetiredList(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct{ command, name, successor string }{
		{"codex", "pfm codex", "engine-sync codex"},
		{"opencode", "pfm opencode", "engine-sync opencode"},
	} {
		name, successor, ok := RetiredPfmCommand(testCase.command)
		if !ok || name != testCase.name || successor != testCase.successor {
			t.Errorf("RetiredPfmCommand(%q) = %q, %q, %v; want %q, %q, true",
				testCase.command, name, successor, ok, testCase.name, testCase.successor)
		}
	}
	for _, command := range []string{"", "ls", "chat", "no-such-command"} {
		if name, successor, ok := RetiredPfmCommand(command); ok {
			t.Errorf("RetiredPfmCommand(%q) = %q, %q, true; want none", command, name, successor)
		}
	}
}

// TestDoctorProjectUpdatesNamesAFileHoldingARetiredPfmCodexCommand: an
// adopter file holding `pfm codex build .` is a RETIRED-NAME row naming the
// file, the line and the successor `engine-sync codex`, through the same
// pass `pfm doctor --project-updates` runs.
func TestDoctorProjectUpdatesNamesAFileHoldingARetiredPfmCodexCommand(t *testing.T) {
	t.Parallel()
	fixture := newProjectUpdateFixture(t)
	writeRetiredFixture(t, fixture.project, map[string]string{
		"scripts/mirror.sh": "#!/bin/sh\npfm codex build .\npfm opencode check .\n",
	})
	var stdout bytes.Buffer
	if code := professor.RunProjectUpdates(
		fixture.project, fixture.runtime.Paths.Home, false, &stdout, ScanRetiredNames,
	); code != 1 {
		t.Fatalf("RunProjectUpdates() code=%d, want 1 (review):\n%s", code, stdout.String())
	}
	for _, want := range []string{
		"    scripts/mirror.sh:2   pfm codex — retired command, now engine-sync codex\n",
		"    scripts/mirror.sh:3   pfm opencode — retired command, now engine-sync opencode\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("report missing %q:\n%s", want, stdout.String())
		}
	}
}

// projectReportNoScan runs `pfm doctor --project-updates` without the
// retired-name scan: project_test.go's status fixtures pin the template
// statuses alone.
func projectReportNoScan(root, home string, jsonOutput bool, stdout *bytes.Buffer) int {
	return professor.RunProjectUpdates(root, home, jsonOutput, stdout, nil)
}

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
	{"type /imagine:build-spec to spec it", "/imagine:build-spec", "command", flightsSuccessor},
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

// TestScanRetiredNamesResolvesAProjectRootReachedThroughASymlink: a root that
// is a symlink to the project is scanned as the project, and a root that
// resolves to nothing is a Failure — never a clean scan of zero files.
func TestScanRetiredNamesResolvesAProjectRootReachedThroughASymlink(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	writeRetiredFixture(t, project, map[string]string{"scripts/old.sh": "run /wave\n"})
	link := filepath.Join(t.TempDir(), "app")
	if err := os.Symlink(project, link); err != nil {
		t.Fatal(err)
	}

	scan := ScanRetiredNames(link, nil)
	if len(scan.Hits) != 1 || scan.Hits[0].Path != "scripts/old.sh" || len(scan.Failures) != 0 {
		t.Fatalf("symlinked root scan = %+v, want the one hit in scripts/old.sh", scan)
	}

	dangling := filepath.Join(t.TempDir(), "gone")
	if err := os.Symlink(filepath.Join(project, "absent"), dangling); err != nil {
		t.Fatal(err)
	}
	scan = ScanRetiredNames(dangling, nil)
	if len(scan.Failures) != 1 || len(scan.Hits) != 0 {
		t.Fatalf("dangling root scan = %+v, want one failure", scan)
	}
}

// syntheticFile is a file that streams size bytes — lines of text opening with
// first, and a NUL at nulAt when nulAt >= 0, one line after first when
// oneLine — and counts what was read of it.
type syntheticFile struct {
	size, nulAt int
	first       string
	oneLine     bool
	read        int
}

func (f *syntheticFile) Read(p []byte) (int, error) {
	if f.read >= f.size {
		return 0, io.EOF
	}
	count := min(len(p), f.size-f.read)
	for i := 0; i < count; i++ {
		position := f.read + i
		switch {
		case position == f.nulAt:
			p[i] = 0
		case position < len(f.first):
			p[i] = f.first[position]
		case !f.oneLine && (position-len(f.first))%10 == 9:
			p[i] = '\n'
		default:
			p[i] = 'x'
		}
	}
	f.read += count
	return count, nil
}

func (f *syntheticFile) Close() error { return nil }

// TestScanRetiredNamesBoundsALineAndOnlyProbesBinary bounds multi-line and
// newline-free files, preserves earlier hits and probes only binary files.
func TestScanRetiredNamesBoundsALineAndOnlyProbesBinary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeRetiredFixture(t, root, map[string]string{"data/dump.bin": "x", "data/flat.log": "x", "data/huge.log": "x"})
	// Multi-line files stop at the byte cap, newline-free ones at the line cap.
	const readCap = 8 << 20
	const size = readCap + 1<<20
	files := map[string]*syntheticFile{
		"dump.bin": {size: size, nulAt: 100, first: "/wave\n"},
		"flat.log": {size: size, nulAt: -1, first: "/wave\n", oneLine: true},
		"huge.log": {size: size, nulAt: -1, first: "/wave\n"},
	}
	open := func(path string) (io.ReadCloser, error) {
		if file, ok := files[filepath.Base(path)]; ok {
			return file, nil
		}
		return os.Open(path)
	}

	scan := scanRetiredNamesWith(root, nil, open)

	if got := files["dump.bin"].read; got > retiredBinaryProbe {
		t.Errorf("binary file: %d bytes read, want at most the %d-byte probe", got, retiredBinaryProbe)
	}
	if got := files["huge.log"].read; got != readCap+1 {
		t.Errorf("text file: %d bytes read, want the %d-byte cap plus one sentinel", got, readCap)
	}
	if got := files["flat.log"].read; got > retiredMaxLine+2*retiredBinaryProbe+4096 {
		t.Errorf("newline-free file: %d bytes read, want the scan stopped near the %d-byte line bound",
			got, retiredMaxLine)
	}
	encoded, err := json.Marshal(scan)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Failures) != 0 || len(scan.Incomplete) != 2 || scan.Incomplete[0].Path != "data/flat.log" ||
		!strings.Contains(scan.Incomplete[0].Error, "line 2 is longer than") ||
		scan.Incomplete[1].Path != "data/huge.log" || !strings.Contains(scan.Incomplete[1].Error, "8388608-byte scan limit") {
		t.Errorf("text coverage = %s; want line-bound and byte-bound INCOMPLETE rows", encoded)
	}
	if len(scan.Hits) != 2 || scan.Hits[0].Path != "data/flat.log" || scan.Hits[1].Path != "data/huge.log" ||
		scan.Hits[0].Line != 1 || scan.Hits[1].Line != 1 {
		t.Fatalf("hits = %+v, want the first line of each text file", scan.Hits)
	}
	for _, size := range []int{0, readCap, readCap + 1} {
		file := &syntheticFile{size: size, nulAt: -1, first: "/wave\n"}
		hits, incomplete, err := scanRetiredFile(context.Background(), func(string) (io.ReadCloser, error) {
			return file, nil
		}, "boundary.txt", "boundary.txt")
		wantHits := 1
		if size == 0 {
			wantHits = 0
		}
		if err != nil || len(hits) != wantHits || (incomplete != "") != (size > readCap) || file.read != size {
			t.Errorf("size %d: hits=%+v incomplete=%q err=%v read=%d", size, hits, incomplete, err, file.read)
		}
	}
}

// newRetiredGitProject is a git work tree holding a tracked script, an
// untracked unignored note and an ignored build output, each naming a retired
// command.
func newRetiredGitProject(t *testing.T) string {
	t.Helper()
	root := newUpdateGitFixture(t)
	writeRetiredFixture(t, root, map[string]string{
		".gitignore":          "out/\ndata/\n",
		"scripts/tracked.sh":  "run /wave\n",
		"notes.md":            "also /jc\n",
		"out/generated.js":    "/wave\n",
		"data/pg/segment.dat": "/wave\n",
	})
	gitTemp(t, root, "add", ".gitignore", "scripts/tracked.sh")
	return root
}

// TestScanRetiredNamesReadsOnlyTheProjectsOwnText: in a git work tree the scan
// reads the tracked and the unignored files, and never opens an ignored one;
// a directory git ignores, even an unreadable one, never fails the report.
func TestScanRetiredNamesReadsOnlyTheProjectsOwnText(t *testing.T) {
	t.Parallel()
	root := newRetiredGitProject(t)
	locked := filepath.Join(root, "data", "pg")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	var opened []string
	open := func(path string) (io.ReadCloser, error) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		opened = append(opened, filepath.ToSlash(relative))
		return os.Open(path)
	}

	scan := scanRetiredNamesWith(root, nil, open)

	var paths []string
	for _, hit := range scan.Hits {
		paths = append(paths, hit.Path)
	}
	sort.Strings(paths)
	if want := []string{"notes.md", "scripts/tracked.sh"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("hit paths = %v, want %v (opened %v)", paths, want, opened)
	}
	for _, path := range opened {
		if strings.HasPrefix(path, "out/") || strings.HasPrefix(path, "data/") {
			t.Errorf("the scan opened ignored file %s", path)
		}
	}
	if len(scan.Failures) != 0 {
		t.Fatalf("failures = %+v, want none: an ignored directory is not the project's text", scan.Failures)
	}
}

// TestScanRetiredNamesSkipsDependencyAndBuildDirsOutsideGit: with no git work
// tree to say what is the project's own, the walk skips the dependency, build
// and cache directories by name, an unreadable one included.
func TestScanRetiredNamesSkipsDependencyAndBuildDirsOutsideGit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{"src/own.sh": "run /jc\n"}
	skipped := []string{".venv", "venv", "target", "dist", "build", "vendor", "__pycache__"}
	for _, dir := range skipped {
		files[dir+"/lib/mod.py"] = "/wave\n"
	}
	writeRetiredFixture(t, root, files)
	locked := filepath.Join(root, "target", "lib")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	scan := ScanRetiredNames(root, nil)

	if len(scan.Hits) != 1 || scan.Hits[0].Path != "src/own.sh" || len(scan.Failures) != 0 {
		t.Fatalf("scan = %+v, want src/own.sh's hit alone and no failure", scan)
	}
}

// TestScanRetiredNamesDoesNotWidenToAWalkWhenGitFails: inside a work tree whose
// git cannot answer (here a broken repository config), the scan fails by name
// instead of falling back to reading every file under the root.
func TestScanRetiredNamesDoesNotWidenToAWalkWhenGitFails(t *testing.T) {
	t.Parallel()
	root := newRetiredGitProject(t)
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[[[ broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	scan := ScanRetiredNames(root, nil)

	if len(scan.Hits) != 0 || len(scan.Failures) != 1 || scan.Failures[0].Path != "." ||
		!strings.Contains(scan.Failures[0].Error, "git") {
		t.Fatalf("scan = %+v, want no hits and one failure at \".\" naming git", scan)
	}
}

// TestRetiredNameInACurrentPinnedFileIsReported: a retired reference the
// project added to a file whose pin is CURRENT is reported under RETIRED-NAME —
// the template diff shows nothing of it — while an UPDATED pin's file, which
// carries its upstream diff for the adopter to port, is not scanned.
func TestRetiredNameInACurrentPinnedFileIsReported(t *testing.T) {
	t.Parallel()
	fixture := newProjectUpdateFixture(t)
	writeProjectFixtureFile(t, fixture.project, ".claude/current.md", "local truth\nrun /wave\n")
	writeProjectFixtureFile(t, fixture.project, ".claude/updated-one.md", "local truth\nrun /jc\n")

	var stdout bytes.Buffer
	code := professor.RunProjectUpdates(fixture.project, fixture.runtime.Paths.Home, false, &stdout, ScanRetiredNames)

	report := stdout.String()
	if code != 1 {
		t.Fatalf("RunProjectUpdates() code=%d, want 1:\n%s", code, report)
	}
	for _, want := range []string{
		"  RETIRED-NAME  1\n",
		"    .claude/current.md:2   /wave — retired command, now the flights-foreman agent\n",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, ".claude/updated-one.md:2") {
		t.Errorf("the UPDATED pinned file was scanned:\n%s", report)
	}
}

func TestRetiredGitEnumerationReportsWarningsAndDeduplicates(t *testing.T) {
	for _, warning := range []string{"", "warning: could not open directory 'locked/': Permission denied\n"} {
		t.Run(warning, func(t *testing.T) {
			fake := &deps.FakeRunner{}
			fake.Script(
				[]string{"git", "rev-parse", "--is-inside-work-tree"},
				deps.RunResult{Stdout: []byte("true\n")},
				nil,
			)
			fake.Script(
				[]string{"git", "ls-files"},
				deps.RunResult{Stdout: []byte("a.md\x00a.md\x00"), Stderr: []byte(warning)},
				nil,
			)
			t.Cleanup(StubRunnerForTest(fake))
			files, _, err := gitProjectFiles(context.Background(), t.TempDir())
			if warning != "" {
				if err == nil || !strings.Contains(err.Error(), "locked/") {
					t.Fatalf("enumeration error=%v, want directory warning", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(files, []string{"a.md"}) {
				t.Fatalf("files=%v error=%v, want one path", files, err)
			}
		})
	}
}

// TestScanRetiredNamesSkipsANameTheProjectDefines: a project that keeps its own
// /km command, agent or skill file is not naming
// a retired thing; the same name in a project without that file is a hit.
func TestScanRetiredNamesSkipsANameTheProjectDefines(t *testing.T) {
	t.Parallel()
	defining := t.TempDir()
	writeRetiredFixture(t, defining, map[string]string{
		".claude/commands/km.md":        "mine\n",
		".claude/commands/qa/live.md":   "mine\n",
		".claude/agents/rndier.md":      "mine\n",
		".claude/skills/wave-builder/x": "mine\n",
		"docs/agents/API.md":            "kept hub\n",
		".claude/agents/ultimate.md":    "mine\n",
		".claude/agents/imagine.md":     "mine\n",
		"docs/notes.md": "/km /qa:live rndier wave-builder docs/agents /jc JC-UPDATE\n" +
			"subagent_type: \"ultimate\" subagent_type:\"ultimate\" subagent_type: 'ultimate' " +
			"subagent_type: ultimate subagent_type=\"ultimate\" {\"subagent_type\": \"ultimate\"} " +
			"{\"subagent_type\":\"ultimate\"} .claude/agents/ultimate.md the `ultimate` agent, " +
			"the `ultimate` sub-agent, the `ultimate` subagent\n" +
			"subagent_type: \"imagine\" subagent_type=\"imagine\" .claude/agents/imagine.md the `imagine` agent\n",
		"docs/other.md": "mcp__chat__chat_ls\n",
	})
	// The live ultimate skill is not the retired agent: its own files leave the
	// agent references flagged.
	skillOnly := t.TempDir()
	writeRetiredFixture(t, skillOnly, map[string]string{
		".claude/skills/ultimate/SKILL.md": "mine\n",
		"docs/notes.md":                    "/ultimate subagent_type: \"ultimate\"\n",
	})
	bare := t.TempDir()
	writeRetiredFixture(t, bare, map[string]string{
		"docs/notes.md": "/km /qa:live rndier wave-builder docs/agents /jc subagent_type: \"ultimate\" subagent_type: \"imagine\"\n",
	})

	names := func(scan professor.RetiredNameScan) []string {
		var got []string
		for _, hit := range scan.Hits {
			if hit.Path == "docs/notes.md" || hit.Path == "docs/other.md" {
				got = append(got, hit.Name)
			}
		}
		sort.Strings(got)
		return got
	}
	if got, want := names(
		ScanRetiredNames(defining, nil),
	), []string{
		"/jc",
		"JC-UPDATE",
		"docs/agents",
		"mcp__chat__",
	}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("hits where the project defines the names = %v, want %v", got, want)
	}
	if got, want := names(ScanRetiredNames(skillOnly, nil)), []string{"subagent_type: \"ultimate\""}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("hits where the project keeps only the live ultimate skill = %v, want %v", got, want)
	}
	if got, want := names(
		ScanRetiredNames(bare, nil),
	), []string{
		"/jc",
		"/km",
		"/qa:live",
		"docs/agents",
		"rndier",
		"subagent_type: \"imagine\"",
		"subagent_type: \"ultimate\"",
		"wave-builder",
	}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("hits in a project defining nothing = %v, want %v", got, want)
	}
}

func TestChatCallRetiredNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeRetiredFixture(
		t,
		root,
		map[string]string{
			"old.md": "mcp__chat__call\npfm chat inject target message\nchat_inject\n",
			"new.md": "mcp__chat-professor__call\npfm chat call target message\n",
		},
	)
	scan := ScanRetiredNames(root, nil)
	if len(scan.Failures) != 0 {
		t.Fatalf("scan failures = %+v", scan.Failures)
	}
	want := []professor.RetiredNameHit{
		{
			Path:      "old.md",
			Line:      1,
			Name:      "mcp__chat__",
			Kind:      "tool",
			Successor: "mcp__chat-professor__*",
		},
		{Path: "old.md", Line: 2, Name: "pfm chat inject", Kind: "command", Successor: "pfm chat call"},
		{Path: "old.md", Line: 3, Name: "chat_inject", Kind: "tool", Successor: "mcp__chat-professor__call"},
	}
	if !reflect.DeepEqual(scan.Hits, want) {
		t.Fatalf("retired hits = %+v, want %+v", scan.Hits, want)
	}
}

func TestScanRetiredNamesReportsRetiredPathsEvenWhenPresent(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ name, exists string }{
		{".claude/commands/pfm.md", ".claude/commands/pfm.md"},
		{".claude/commands/pfm/references/audit-scopes.md", ".claude/commands/pfm/references/audit-scopes.md"},
		{"docs/agents", "docs/agents/index.md"},
		{"pfm-guard.sh", "pfm-guard.sh"},
		{"pfm-guard.sh", ".claude/scripts/pfm-guard.sh"},
		{"guard-stamp.sh", "guard-stamp.sh"},
		{"guard-stamp.sh", ".claude/scripts/guard-stamp.sh"},
		{"build-codex.mjs", "build-codex.mjs"},
		{"build-codex.mjs", ".claude/scripts/build-codex.mjs"},
		{"scripts/codex-sync.sh", "scripts/codex-sync.sh"},
		{"scripts/codex-sync.sh", ".claude/scripts/codex-sync.sh"},
	} {
		t.Run(row.exists, func(t *testing.T) {
			root := t.TempDir()
			writeRetiredFixture(
				t,
				root,
				map[string]string{row.exists: "legacy file\n", "notes.md": "run " + row.name + "\n"},
			)
			scan := ScanRetiredNames(root, nil)
			if len(scan.Failures) != 0 || len(scan.Hits) != 1 || scan.Hits[0].Name != row.name ||
				scan.Hits[0].Kind != "path" ||
				scan.Hits[0].Path != "notes.md" {
				t.Fatalf("existing retired path scan = %+v, want notes.md hit for %s", scan, row.name)
			}
			if row.name == "scripts/codex-sync.sh" && scan.Hits[0].Successor != "engine-sync codex" {
				t.Fatalf("codex sync successor = %q, want engine-sync codex", scan.Hits[0].Successor)
			}
		})
	}
}

func TestRetiredNamesReportRemovedTwinAgents(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ line, name, successor, definition string }{
		{"spawn gpt-foreman", "", "", ""},
		{"spawn gpt-lander", "gpt-lander", "", ""},
		{"spawn gpt-executor", "gpt-executor", "", ""},
		{`subagent_type: "terra"`, `subagent_type: "terra"`, "", ""},
		{`subagent_type:"terra"`, `subagent_type:"terra"`, "", ""},
		{`subagent_type: 'terra'`, `subagent_type: 'terra'`, "", ""},
		{"subagent_type: terra", "subagent_type: terra", "", ""},
		{`subagent_type="terra"`, `subagent_type="terra"`, "", ""},
		{`{"subagent_type": "terra"}`, `subagent_type": "terra"`, "", ""},
		{`{"subagent_type":"terra"}`, `subagent_type":"terra"`, "", ""},
		{".claude/agents/terra.md", "agents/terra.md", "", ""},
		{"the `terra` agent", "`terra` agent", "", ""},
		{"the `terra` sub-agent", "`terra` sub-agent", "", ""},
		{"the `terra` subagent", "`terra` subagent", "", ""},
		{"terra firma and terra incognita", "", "", ""},
		{"terrestrial prose and terra-cotta; /terra and `terra` skill", "", "", ""},
		{`subagent_type: "terra" and .claude/agents/terra.md`, "", "", ".claude/agents/terra.md"},
	} {
		t.Run(row.line, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{"notes.md": row.line + "\n"}
			if row.definition != "" {
				files[row.definition] = "mine\n"
			}
			writeRetiredFixture(t, root, files)
			scan := ScanRetiredNames(root, nil)
			var want []professor.RetiredNameHit
			if row.name != "" {
				want = []professor.RetiredNameHit{
					{Path: "notes.md", Line: 1, Name: row.name, Kind: "agent", Successor: row.successor},
				}
			}
			if len(scan.Failures) != 0 || !slices.Equal(scan.Hits, want) {
				t.Fatalf("retired agent scan = %+v, want %+v", scan, want)
			}
		})
	}
}

// TestRetiredNamesReportTheFlightsCut: every agent and command the flights
// family cut retires is a hit with no successor, and the names beside them
// that only share a prefix — the current foremen, gpt-foreman, /flights:doctor —
// are none, nor is flights-speccer, which ships again (templates/global/agents).
func TestRetiredNamesReportTheFlightsCut(t *testing.T) {
	t.Parallel()
	rows := []struct{ line, name, kind, successor string }{
		{"spawn foreman-pro for it", "foreman-pro", "agent", ""},
		{"spawn gpt-foreman-pro for it", "gpt-foreman-pro", "agent", ""},
		{"hand it to flights-mechanical-executor", "flights-mechanical-executor", "agent", ""},
		{"hand it to gpt-flights-mechanical-executor", "gpt-flights-mechanical-executor", "agent", ""},
		{"gpt-flights-speccer writes the spec", "gpt-flights-speccer", "agent", ""},
		{"run /flights:spec first", "/flights:spec", "command", ""},
		{"run /flights:init once", "/flights:init", "command", ""},
		{"run /flights:orchestrate-cross-harness on codex", "/flights:orchestrate-cross-harness", "command", ""},
	}
	root := t.TempDir()
	var content strings.Builder
	var want []professor.RetiredNameHit
	for index, row := range rows {
		content.WriteString(row.line + "\n")
		want = append(want, professor.RetiredNameHit{
			Path: "notes.md", Line: index + 1, Name: row.name, Kind: row.kind, Successor: row.successor,
		})
	}
	content.WriteString("spawn flights-foreman, flights-foreman-sonnet or gpt-foreman; run /flights:doctor\n")
	content.WriteString("flights-speccer writes the spec\n")
	writeRetiredFixture(t, root, map[string]string{"notes.md": content.String()})
	scan := ScanRetiredNames(root, nil)
	if len(scan.Failures) != 0 || !reflect.DeepEqual(scan.Hits, want) {
		t.Fatalf("flights cut scan =\n%+v\nwant\n%+v", scan, want)
	}
}

func TestRetiredNamesReportTheLanderTiers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeRetiredFixture(t, root, map[string]string{"notes.md": "spawn flights-lander-mini for a small change\n" +
		"spawn flights-lander-pro for a large one\n" +
		"spawn flights-lander or gpt-flights-lander; it spawns flights-hunter and flights-sub-lander\n"})
	want := []professor.RetiredNameHit{
		{Path: "notes.md", Line: 1, Name: "flights-lander-mini", Kind: "agent", Successor: "flights-lander"},
		{Path: "notes.md", Line: 2, Name: "flights-lander-pro", Kind: "agent", Successor: "flights-lander"},
	}
	scan := ScanRetiredNames(root, nil)
	if len(scan.Failures) != 0 || !reflect.DeepEqual(scan.Hits, want) {
		t.Fatalf("lander tiers scan =\n%+v\nwant\n%+v", scan, want)
	}
}

func TestScanRetiredNamesStreamsTrackedTextToClean(t *testing.T) {
	t.Parallel()
	root := newUpdateGitFixture(t)
	content := "/wave\n" + strings.Repeat(
		"x",
		1<<20,
	) + "\n\n" + strings.Repeat(
		"y",
		70<<10,
	) + " /jc\r\n/quality:forlint"
	writeRetiredFixture(t, root, map[string]string{"large.txt": content})
	gitTemp(t, root, "add", "large.txt")
	scan := ScanRetiredNames(root, nil)
	want := []professor.RetiredNameHit{
		{Path: "large.txt", Line: 1, Name: "/wave", Kind: "command", Successor: flightsSuccessor},
		{Path: "large.txt", Line: 4, Name: "/jc", Kind: "command"},
		{Path: "large.txt", Line: 5, Name: "/quality:forlint", Kind: "command", Successor: "/quality:md-forlint"},
	}
	if len(scan.Failures) != 0 || len(scan.Incomplete) != 0 || !reflect.DeepEqual(scan.Hits, want) {
		t.Errorf("large tracked scan = %+v, want complete hits %+v", scan, want)
	}
	clean := strings.NewReplacer("/wave", "/flights:doctor", "/jc", "/quality:prompt", "/quality:forlint", "/quality:md-forlint").
		Replace(content)
	writeRetiredFixture(t, root, map[string]string{"large.txt": clean})
	scan = ScanRetiredNames(root, nil)
	if len(scan.Hits) != 0 || len(scan.Failures) != 0 || len(scan.Incomplete) != 0 {
		t.Fatalf("edited large tracked file = %+v, want full clean coverage", scan)
	}
}

type retiredFailingFile struct {
	io.Reader
	readErr, closeErr error
}

func (file retiredFailingFile) Read(p []byte) (int, error) {
	n, err := file.Reader.Read(p)
	if errors.Is(err, io.EOF) && file.readErr != nil {
		return n, file.readErr
	}
	return n, err
}
func (file retiredFailingFile) Close() error { return file.closeErr }

func TestScanRetiredNamesRetainsCompletedHitsOnReadOrCloseFailure(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"read", "close"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			writeRetiredFixture(t, root, map[string]string{"partial.txt": "fixture\n"})
			open := func(string) (io.ReadCloser, error) {
				file := retiredFailingFile{Reader: strings.NewReader("/wave\n" + strings.Repeat("x\n", 5000))}
				if operation == "read" {
					file.readErr = errors.New("fixture read failed")
				} else {
					file.closeErr = errors.New("fixture close failed")
				}
				return file, nil
			}
			scan := scanRetiredNamesWith(root, nil, open)
			if len(scan.Hits) != 1 || scan.Hits[0].Line != 1 || scan.Hits[0].Name != "/wave" ||
				len(scan.Failures) != 1 ||
				!strings.Contains(scan.Failures[0].Error, "fixture "+operation+" failed") {
				t.Fatalf("partial scan = %+v, want the completed hit and contextual %s failure", scan, operation)
			}
		})
	}
}
