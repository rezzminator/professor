package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/retired"
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

const fixtureFlightsSuccessor = "the flights:foreman agent"

// fixtureRetiredNames is the retired list the scanner tests read: one entry
// per match shape, kind and probe they pin.
var fixtureRetiredNames = []retired.Name{
	{Name: "/wave", Kind: retired.KindCommand, Successor: fixtureFlightsSuccessor, Match: retired.MatchFamily},
	{Name: "wave-builder", Kind: retired.KindSkill, Successor: fixtureFlightsSuccessor, Match: retired.MatchExact},
	{Name: "/jc", Kind: retired.KindCommand, Match: retired.MatchFamily},
	{Name: "/qa:live", Kind: retired.KindCommand, Match: retired.MatchExact},
	{Name: "JC-COMMIT", Kind: retired.KindPhase, Successor: "COMMIT", Match: retired.MatchExact},
	{Name: "JC-UPDATE", Kind: retired.KindPhase, Match: retired.MatchExact},
	{Name: "/pfm:refresh", Kind: retired.KindCommand, Successor: "the pcm agent", Match: retired.MatchExact},
	{Name: "/quality:forlint", Kind: retired.KindCommand, Successor: "/law:markdown", Match: retired.MatchExact},
	{Name: ".claude/commands/pfm.md", Kind: retired.KindPath, Successor: "the pcm agent", Match: retired.MatchExact},
	{Name: "/pcm", Kind: retired.KindCommand, Successor: "the pcm agent", Match: retired.MatchExact},
	{Name: "mcp__chat__", Kind: retired.KindTool, Successor: "mcp__chat-professor__*", Match: retired.MatchPrefix},
	{Name: "chat_inject", Kind: retired.KindTool, Successor: "mcp__chat-professor__call", Match: retired.MatchExact},
	{Name: "/chat:", Kind: retired.KindCommand, Successor: "mcp__chat-professor__*", Match: retired.MatchPrefix},
	{Name: "/documenter", Kind: retired.KindCommand, Successor: "/law:doc", Match: retired.MatchFamily},
	{Name: "/km", Kind: retired.KindCommand, Match: retired.MatchExact},
	{Name: "/rnd", Kind: retired.KindCommand, Match: retired.MatchFamily},
	{Name: "mono-architect", Kind: retired.KindAgent, Match: retired.MatchExact},
	{Name: "rndier", Kind: retired.KindAgent, Match: retired.MatchExact},
	{Name: "rr-super", Kind: retired.KindAgent, Successor: "super-rr", Match: retired.MatchExact},
	{Name: "plot-inventory", Kind: retired.KindSkill, Successor: "plots:plot-inventory", Match: retired.MatchExact},
	{
		Name:      "/tokens",
		Kind:      retired.KindCommand,
		Successor: "the chatlog skill",
		Match:     retired.MatchFamily,
		Route:     true,
	},
	{Name: "pfm sketch", Kind: retired.KindCommand, Successor: "sketcher draw", Match: retired.MatchFamily},
	{Name: "pfm doodle", Kind: retired.KindCommand, Match: retired.MatchExact},
	{Name: "docs/agents", Kind: retired.KindPath, Successor: "docs/_index.md", Match: retired.MatchExact},
	{Name: "pfm-guard.sh", Kind: retired.KindPath, Match: retired.MatchExact},
	{Name: "scripts/codex-sync.sh", Kind: retired.KindPath, Successor: "engine-sync codex", Match: retired.MatchExact},
	{
		Name: `subagent_type: "ultimate"`, Kind: retired.KindAgent, Successor: "/imagine", Match: retired.MatchExact,
		Probe: ".claude/agents/ultimate.md",
	},
	{
		Name: "`ultimate` agent", Kind: retired.KindAgent, Successor: "/imagine", Match: retired.MatchExact,
		Probe: ".claude/agents/ultimate.md",
	},
	{
		Name: `subagent_type: "imagine"`, Kind: retired.KindAgent, Successor: "imaginator", Match: retired.MatchExact,
		Probe: ".claude/agents/imagine.md",
	},
}

// scanFixture is the scan over fixtureRetiredNames, opening files with open
// (nil: the real files).
func scanFixture(
	root string,
	pinned map[string]bool,
	open func(string) (io.ReadCloser, error),
) professor.RetiredNameScan {
	if open == nil {
		open = openRetiredFile
	}
	return scanRetiredNames(context.Background(), fixtureRetiredNames, root, pinned, open)
}

// stageRetiredLists writes names as the retired lists of home's clone, the
// file RetiredNameScanner reads.
func stageRetiredLists(t *testing.T, home string, names []retired.Name) {
	t.Helper()
	repo, err := paths.GlobalSourceRepo(home)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(retired.Lists{Names: names})
	if err != nil {
		t.Fatal(err)
	}
	writeRetiredFixture(t, repo, map[string]string{retired.FileRel: string(raw)})
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

	scan := scanFixture(root, map[string]bool{"CLAUDE.md": true}, nil)

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
		{Path: "scripts/legacy.sh", Line: 2, Name: "/wave", Kind: "command", Successor: fixtureFlightsSuccessor},
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
		"/pfm:release cut patch \"fix\"",
		"/pfm:workbench new",
		"run /pcm:update or /pcmx",
		"the /pfm CLI guide",
		"see ~/.claude/commands/pfm.md and $HOME/.claude/commands/pfm.md",
		".claude/commands/pcm.md",
		"/law:markdown check",
		"/improve:iterative target",
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
		"GET /tokens returns the list",
		"curl -X POST /tokens",
		"the /tokens/{id} route and /tokens{?page}",
		"load the plots:plot-inventory skill",
	} {
		if hits := retiredNameHits(fixtureRetiredNames, "f", line); len(hits) != 0 {
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
		"run /tokens --today":              "/tokens",
		"`/tokens:week`":                   "/tokens",
		"run the plot-inventory skill":     "plot-inventory",
	} {
		hits := retiredNameHits(fixtureRetiredNames, "f", line)
		if len(hits) != 1 || hits[0].Name != name {
			t.Errorf("line %q hits %+v, want exactly %q", line, hits, name)
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

	scan := scanFixture(root, nil, open)

	if len(scan.Failures) != 1 || scan.Failures[0].Path != "scripts/locked.sh" ||
		!strings.Contains(scan.Failures[0].Error, "permission denied") {
		t.Fatalf("failures = %+v, want scripts/locked.sh permission denied", scan.Failures)
	}
	if len(scan.Hits) != 1 || scan.Hits[0].Path != "scripts/open.sh" {
		t.Fatalf("hits = %+v, want the readable file's /jc", scan.Hits)
	}

	missing := scanFixture(filepath.Join(root, "absent"), nil, nil)
	if len(missing.Failures) != 1 || len(missing.Hits) != 0 {
		t.Fatalf("missing root scan = %+v, want one failure", missing)
	}
}

// TestRetiredPfmCommandReadsTheRetiredList: the dispatch of a retired `pfm
// {command}` reads the name and successor from the retired list; a command
// the list does not retire with a successor, or retires as another kind, is
// no answer.
func TestRetiredPfmCommandReadsTheRetiredList(t *testing.T) {
	t.Parallel()
	names := append([]retired.Name{
		{Name: "pfm scribble", Kind: retired.KindTool, Successor: "scribbler", Match: retired.MatchExact},
	}, fixtureRetiredNames...)
	if name, successor, ok := RetiredPfmCommand(
		names,
		"sketch",
	); !ok || name != "pfm sketch" ||
		successor != "sketcher draw" {
		t.Errorf(
			"RetiredPfmCommand(sketch) = %q, %q, %v; want %q, %q, true",
			name,
			successor,
			ok,
			"pfm sketch",
			"sketcher draw",
		)
	}
	for _, command := range []string{"", "doodle", "scribble", "ls", "sketch build"} {
		if name, successor, ok := RetiredPfmCommand(names, command); ok {
			t.Errorf("RetiredPfmCommand(%q) = %q, %q, true; want none", command, name, successor)
		}
	}
}

// TestDoctorProjectUpdatesNamesAFileHoldingARetiredPfmCommand: an adopter
// file holding a retired `pfm sketch build .` is a RETIRED-NAME row naming the
// file, the line and the successor, through the same pass `pfm doctor
// --project-updates` runs over the clone's retired list; a list that cannot
// be read fails the report instead of passing for a clean scan.
func TestDoctorProjectUpdatesNamesAFileHoldingARetiredPfmCommand(t *testing.T) {
	t.Parallel()
	fixture := newProjectUpdateFixture(t)
	writeRetiredFixture(t, fixture.project, map[string]string{
		"scripts/mirror.sh": "#!/bin/sh\npfm sketch build .\npfm doodle check .\n",
	})
	run := func() (int, string) {
		var stdout bytes.Buffer
		code := professor.RunProjectUpdates(
			fixture.project, fixture.runtime.Paths.Home, false, &stdout,
			RetiredNameScanner(context.Background(), fixture.runtime.Paths.Home),
		)
		return code, stdout.String()
	}

	code, report := run()
	if code != 1 {
		t.Fatalf("RunProjectUpdates() code=%d, want 1 (review):\n%s", code, report)
	}
	for _, want := range []string{
		"    scripts/mirror.sh:2   pfm sketch — retired command, now sketcher draw\n",
		"    scripts/mirror.sh:3   pfm doodle — retired command, no successor\n",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}

	repo, err := paths.GlobalSourceRepo(fixture.runtime.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repo, filepath.FromSlash(retired.FileRel))
	if err := os.WriteFile(file, []byte(`{"names":`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, report = run()
	if code == 0 || code == 1 ||
		!strings.Contains(report, "FAILED — 1 file(s) could not be scanned for retired names") ||
		!strings.Contains(report, file) {
		t.Fatalf(
			"RunProjectUpdates() over an unreadable list code=%d, want a FAILED report naming %s:\n%s",
			code,
			file,
			report,
		)
	}
}

// projectReportNoScan runs `pfm doctor --project-updates` without the
// retired-name scan: project_test.go's status fixtures pin the template
// statuses alone.
func projectReportNoScan(root, home string, jsonOutput bool, stdout *bytes.Buffer) int {
	return professor.RunProjectUpdates(root, home, jsonOutput, stdout, nil)
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

	scan := scanFixture(link, nil, nil)
	if len(scan.Hits) != 1 || scan.Hits[0].Path != "scripts/old.sh" || len(scan.Failures) != 0 {
		t.Fatalf("symlinked root scan = %+v, want the one hit in scripts/old.sh", scan)
	}

	dangling := filepath.Join(t.TempDir(), "gone")
	if err := os.Symlink(filepath.Join(project, "absent"), dangling); err != nil {
		t.Fatal(err)
	}
	scan = scanFixture(dangling, nil, nil)
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

	scan := scanFixture(root, nil, open)

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
		hits, incomplete, err := scanRetiredFile(
			context.Background(),
			fixtureRetiredNames,
			func(string) (io.ReadCloser, error) {
				return file, nil
			},
			"boundary.txt",
			"boundary.txt",
		)
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

	scan := scanFixture(root, nil, open)

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

	scan := scanFixture(root, nil, nil)

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

	scan := scanFixture(root, nil, nil)

	if got, want := strings.ReplaceAll(
		fmt.Sprintf("%+v", scan),
		root,
		"{root}",
	), "{Hits:[] Failures:[{Path:. Error:inspect git work tree: git rev-parse exited 128: fatal: bad config line 1 in file {root}/.git/config}] Incomplete:[] Accepted:[] AcceptedHits:0}"; got != want {
		t.Fatalf("scan = %q, want %q", got, want)
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
	code := professor.RunProjectUpdates(
		fixture.project, fixture.runtime.Paths.Home, false, &stdout,
		RetiredNameScanner(context.Background(), fixture.runtime.Paths.Home),
	)

	report := stdout.String()
	if code != 1 {
		t.Fatalf("RunProjectUpdates() code=%d, want 1:\n%s", code, report)
	}
	for _, want := range []string{
		"  RETIRED-NAME  1\n",
		"    .claude/current.md:2   /wave — retired command, now " + fixtureFlightsSuccessor + "\n",
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
		scanFixture(defining, nil, nil),
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
	if got, want := names(scanFixture(skillOnly, nil, nil)), []string{"subagent_type: \"ultimate\""}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("hits where the project keeps only the live ultimate skill = %v, want %v", got, want)
	}
	if got, want := names(
		scanFixture(bare, nil, nil),
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

func TestScanRetiredNamesReportsRetiredPathsEvenWhenPresent(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ name, exists string }{
		{".claude/commands/pfm.md", ".claude/commands/pfm.md"},
		{"docs/agents", "docs/agents/index.md"},
		{"pfm-guard.sh", "pfm-guard.sh"},
		{"pfm-guard.sh", ".claude/scripts/pfm-guard.sh"},
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
			scan := scanFixture(root, nil, nil)
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
	scan := scanFixture(root, nil, nil)
	want := []professor.RetiredNameHit{
		{Path: "large.txt", Line: 1, Name: "/wave", Kind: "command", Successor: fixtureFlightsSuccessor},
		{Path: "large.txt", Line: 4, Name: "/jc", Kind: "command"},
		{Path: "large.txt", Line: 5, Name: "/quality:forlint", Kind: "command", Successor: "/law:markdown"},
	}
	if len(scan.Failures) != 0 || len(scan.Incomplete) != 0 || !reflect.DeepEqual(scan.Hits, want) {
		t.Errorf("large tracked scan = %+v, want complete hits %+v", scan, want)
	}
	clean := strings.NewReplacer("/wave", "/flights:doctor", "/jc", "/law:prompt", "/quality:forlint", "/law:markdown").
		Replace(content)
	writeRetiredFixture(t, root, map[string]string{"large.txt": clean})
	scan = scanFixture(root, nil, nil)
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
	for operation, want := range map[string]string{"read": "{Hits:[{Path:partial.txt Line:1 Name:/wave Kind:command Successor:the flights:foreman agent}] Failures:[{Path:partial.txt Error:read partial.txt: fixture read failed}] Incomplete:[] Accepted:[] AcceptedHits:0}", "close": "{Hits:[{Path:partial.txt Line:1 Name:/wave Kind:command Successor:the flights:foreman agent}] Failures:[{Path:partial.txt Error:close partial.txt: fixture close failed}] Incomplete:[] Accepted:[] AcceptedHits:0}"} {
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
			scan := scanFixture(root, nil, open)
			if got := strings.ReplaceAll(fmt.Sprintf("%+v", scan), root, "{root}"); got != want {
				t.Fatalf("partial scan = %q, want %q", got, want)
			}
		})
	}
}
