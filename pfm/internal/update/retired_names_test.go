package update

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

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
	root := t.TempDir()
	writeRetiredFixture(t, root, map[string]string{
		"scripts/legacy.sh":            "#!/bin/sh\nclaude -p '/wave:refine the spec'\n# gitter Phase: JC-COMMIT\n",
		".claude/codex-build.json":     "{\n  \"skills\": [],\n  \"tools\": [\"mcp__chat__chat_inject\"]\n}\n",
		".codex/config.toml":           "[mcp]\nallow = \"mcp__harvester__readPage\"\n",
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
			Successor: "mcp__professor__chat_*",
		},
		{
			Path: ".codex/config.toml", Line: 2, Name: "mcp__harvester__", Kind: "tool",
			Successor: "mcp__professor__harvester_*",
		},
		{Path: "scripts/legacy.sh", Line: 2, Name: "/wave", Kind: "command", Successor: "/flights:spec"},
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
	for _, line := range []string{
		"/pfm:release prepare --from ../live",
		"/pfm:workbench new",
		"run /pcm then /pcm:update",
		"the /pfm CLI guide",
		"see ~/.claude/commands/pfm.md and $HOME/.claude/commands/pfm.md",
		".claude/commands/pcm.md",
		"/quality:md-forlint check",
		"mcp__professor__chat_inject and mcp__professor__harvester_read",
		"spawn super-rr",
		"import x from 'src/wave.js'; /waveform",
		"/jcx and /pfm:refresher and /qa:lively",
		"JC-COMMITTED is not a phase; DOCS-COMMIT and COMMIT are live",
		"xmono-architect mono-architects",
		"the /chat route",
		"https://example.com/wave and http://localhost:3000/jc",
		"/wave-something",
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
		"x .claude/commands/pfm/references/audit-scopes.md": ".claude/commands/pfm/references/audit-scopes.md",
	} {
		hits := retiredNameHits("f", line)
		if len(hits) != 1 || hits[0].Name != name {
			t.Errorf("line %q hits %+v, want exactly %q", line, hits, name)
		}
	}
}

// TestScanRetiredNamesReportsAnUnreadableFileAsAFailure: a file the scan
// cannot read, and a root that does not exist, are failures naming the path
// and the error — never an empty, clean scan.
func TestScanRetiredNamesReportsAnUnreadableFileAsAFailure(t *testing.T) {
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

// projectReportNoScan runs `pfm doctor --project-updates` without the
// retired-name scan: project_test.go's status fixtures pin the template
// statuses alone.
func projectReportNoScan(root, home string, jsonOutput bool, stdout *bytes.Buffer) int {
	return professor.RunProjectUpdates(root, home, jsonOutput, stdout, nil)
}

// retiredThisRelease is one line per name v0.80.0 retires, each in the form
// an adopter's file writes it, with the hit the scan owes it.
var retiredThisRelease = []struct{ line, name, kind, successor string }{
	{"spawn tracer-pro for the review", "tracer-pro", "agent", "tracer"},
	{"model: tracer-pro-max", "tracer-pro-max", "agent", "tracer"},
	{"use tracer-rr to read the repo", "tracer-rr", "agent", "clone-distiller"},
	{"arm it with /reminder 30d", "/reminder", "command", "/pfm:schedule"},
	{"run /tokens for the spend", "/tokens", "command", chatlogSuccessor},
	{"see .claude/skills/transcript/SKILL.md", "skills/transcript", "skill", chatlogSuccessor},
	{"load the transcript skill first", "transcript skill", "skill", chatlogSuccessor},
	{"type /close-out when done", "/close-out", "command", "/done-herewith"},
	{"see .claude/skills/close-out/SKILL.md", "skills/close-out", "skill", "/done-herewith"},
	{"run pfm price --json", "pfm price", "command", "pfm model-cost"},
	{"see docs/agents/_index.md", "docs/agents", "path", "docs/_index.md and docs/standards.md"},
}

// TestRetiredNamesPinEveryNameThisReleaseRetires: a project file naming any
// of the names v0.80.0 retires is reported with its successor, and the live
// neighbours of those names (their successors, the everyday word, a longer
// name) are no hit.
func TestRetiredNamesPinEveryNameThisReleaseRetires(t *testing.T) {
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
		"spawn tracer and tracer-loom; tracer-product is a word",
		"pfm model-cost --json and pfm prices",
		"the price of the plan; a price list",
		"pfm tokens --since 1d and pfm transcript show",
		"the transcript of the chat; read the transcript",
		"a close-out checklist for the week",
		"/pfm:schedule weekly and /done-herewith",
		"https://example.com/tokens and /tokenizer and /reminders and /remind",
		"super-rr and .claude/agents/tracer.md",
		"docs/_index.md, docs/standards.md and docs/agents-guide",
		"~/.claude/skills/chatlog/SKILL.md",
	} {
		if hits := retiredNameHits("f", line); len(hits) != 0 {
			t.Errorf("line %q hit %+v, want none", line, hits)
		}
	}
}

// TestScanRetiredNamesResolvesAProjectRootReachedThroughASymlink: a root that
// is a symlink to the project is scanned as the project, and a root that
// resolves to nothing is a Failure — never a clean scan of zero files.
func TestScanRetiredNamesResolvesAProjectRootReachedThroughASymlink(t *testing.T) {
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
// first, and a NUL at nulAt when nulAt >= 0 — and counts what was read of it.
type syntheticFile struct {
	size, nulAt int
	first       string
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
		case (position-len(f.first))%10 == 9:
			p[i] = '\n'
		default:
			p[i] = 'x'
		}
	}
	f.read += count
	return count, nil
}

func (f *syntheticFile) Close() error { return nil }

// TestScanRetiredNamesNeverReadsPastTheProbeOrTheCap: a binary file is
// decided by its first retiredBinaryProbe bytes, and a huge text file is read
// to retiredMaxRead at most — still scanned for the names it holds there.
func TestScanRetiredNamesNeverReadsPastTheProbeOrTheCap(t *testing.T) {
	root := t.TempDir()
	writeRetiredFixture(t, root, map[string]string{"data/dump.bin": "x", "data/huge.log": "x"})
	const size = 16 << 20
	files := map[string]*syntheticFile{
		"dump.bin": {size: size, nulAt: 100, first: "/wave\n"},
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
	if got := files["huge.log"].read; got > retiredMaxRead {
		t.Errorf("text file: %d bytes read, want at most the %d-byte cap", got, retiredMaxRead)
	}
	if len(scan.Failures) != 1 || scan.Failures[0].Path != "data/huge.log" ||
		!strings.Contains(scan.Failures[0].Error, "incomplete") {
		t.Errorf("truncated text coverage = %+v; want a named incomplete scan", scan.Failures)
	}
	if len(scan.Hits) != 1 || scan.Hits[0].Path != "data/huge.log" || scan.Hits[0].Line != 1 {
		t.Fatalf("hits = %+v, want only the huge text file's first line", scan.Hits)
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
		"    .claude/current.md:2   /wave — retired command, now /flights:spec\n",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, ".claude/updated-one.md:2") {
		t.Errorf("the UPDATED pinned file was scanned:\n%s", report)
	}
}
