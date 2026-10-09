package digest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// goldenCase is one captured run of the Python reader this package replaced,
// or one OpenCode case written for the port: its argument list, the empty
// directories its home held, its home's symlinks (path under the home → link
// text, as readlink prints it), and the OpenCode store seed built into the
// home at test time (testdata/opencode/{name}.sql over schema.sql). Links and
// stores live here, never in the repository: a tracked dangling or in-repo
// link is refused by scripts/check-codex-markers.mjs, and no binary database
// is committed.
type goldenCase struct {
	Args     []string          `json:"args"`
	Dirs     []string          `json:"dirs"`
	Links    map[string]string `json:"links"`
	OpenCode string            `json:"opencode"`
}

// openCodeRoot is where a case home keeps OpenCode's data, its default root.
const openCodeRoot = ".local/share/opencode"

const (
	claudeFixture = "{HOME}/.claude/projects/-work-repo/11111111-2222-3333-4444-555555555555/subagents/" +
		"agent-a0123456789abcdef.jsonl"
	codexFixture = "{HOME}/.codex/sessions/2026/09/29/" +
		"rollout-2026-09-29T22-42-11-01a0eee7-4d90-7e82-8340-2904c468c83f.jsonl"
)

// TestDigestReplaysThePythonGoldens runs every captured case against a copy of
// its home and compares stdout, stderr, the exit code and any --out file with
// the capture; a case's stdout.intended, stderr.intended, exit.intended or outfile.intended replaces the capture
// where the port differs on purpose.
func TestDigestReplaysThePythonGoldens(t *testing.T) {
	cases, err := filepath.Glob(filepath.Join("testdata", "cases", "*", "case.json"))
	if err != nil || len(cases) == 0 {
		t.Fatalf("no golden cases found (err %v)", err)
	}
	for _, spec := range cases {
		folder, err := filepath.Abs(filepath.Dir(spec))
		if err != nil {
			t.Fatal(err)
		}
		t.Run(filepath.Base(folder), func(t *testing.T) { replayGolden(t, folder) })
	}
}

func replayGolden(t *testing.T, folder string) {
	var golden goldenCase
	data, err := os.ReadFile(filepath.Join(folder, "case.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatalf("case.json: %v", err)
	}
	home := filepath.Join(t.TempDir(), "home")
	if _, err := os.Stat(filepath.Join(folder, "home")); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		copyHome(t, filepath.Join(folder, "home"), home)
	}
	for _, dir := range golden.Dirs {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	makeLinks(t, home, golden.Links)
	if golden.OpenCode != "" {
		seedOpenCodeStore(t, filepath.Join(home, openCodeRoot), golden.OpenCode)
	}
	fill := strings.NewReplacer("{CLAUDE}", strings.ReplaceAll(claudeFixture, "{HOME}", home),
		"{CODEX}", strings.ReplaceAll(codexFixture, "{HOME}", home), "{HOME}", home)
	args := make([]string, len(golden.Args))
	outFile := ""
	for index, arg := range golden.Args {
		args[index] = fill.Replace(arg)
		if index > 0 && golden.Args[index-1] == "--out" {
			outFile = args[index]
		}
	}
	t.Chdir(home)
	var stdout, stderr bytes.Buffer
	code := Invoke(context.Background(), args, &stdout, &stderr, pythonEnv(home))
	mask := homeMask(t, home)
	gotStdout := mask.Replace(stdout.String())
	if outFile != "" {
		gotStdout = checkWroteBytes(t, outFile, gotStdout)
	}
	compareGolden(t, folder, "stdout", gotStdout)
	compareGolden(t, folder, "stderr", mask.Replace(stderr.String()))
	compareGolden(t, folder, "exit", strconv.Itoa(code)+"\n")
	if outFile == "" {
		return
	}
	_, wantErr := os.Stat(filepath.Join(folder, "outfile"))
	if errors.Is(wantErr, fs.ErrNotExist) {
		if info, err := os.Stat(outFile); err == nil && !info.IsDir() {
			t.Errorf("--out wrote %s where the capture wrote nothing", outFile)
		}
		return
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read the --out file: %v", err)
	}
	compareGolden(t, folder, "outfile", mask.Replace(string(got)))
}

// wroteBytes is the byte count of a WROTE line.
var wroteBytes = regexp.MustCompile(`(?m)^(WROTE .* · )(\d+)( bytes · \d+ lines)$`)

// checkWroteBytes holds a WROTE line's byte count to the file it names and
// masks it: the count follows the length of the home path the file holds,
// which differs between the capture and the run.
func checkWroteBytes(t *testing.T, outFile, stdout string) string {
	t.Helper()
	match := wroteBytes.FindStringSubmatch(stdout)
	if match == nil {
		return stdout
	}
	info, err := os.Stat(outFile)
	if err != nil || strconv.FormatInt(info.Size(), 10) != match[2] {
		t.Errorf("WROTE says %s bytes; the file: %v (stat err %v)", match[2], info, err)
	}
	return wroteBytes.ReplaceAllString(stdout, "${1}{BYTES}${3}")
}

// pythonEnv is the roots the Python reader found in a home: its projects
// folder under ~/.claude and each ~/.cc account, its Codex homes.
func pythonEnv(home string) Env {
	env := Env{Home: home}
	claudeBases := []string{filepath.Join(home, ".claude")}
	accounts, _ := filepath.Glob(filepath.Join(home, ".cc", "*"))
	sort.Strings(accounts)
	claudeBases = append(claudeBases, accounts...)
	for _, base := range claudeBases {
		if info, err := os.Stat(filepath.Join(base, "projects")); err == nil && info.IsDir() {
			env.ClaudeRoots = append(env.ClaudeRoots, filepath.Join(base, "projects"))
		}
	}
	codexBases := []string{filepath.Join(home, ".codex")}
	extra, _ := filepath.Glob(filepath.Join(home, ".cc", "*", "codex"))
	sort.Strings(extra)
	for _, base := range append(codexBases, extra...) {
		if info, err := os.Stat(base); err == nil && info.IsDir() {
			env.CodexHomes = append(env.CodexHomes, base)
		}
	}
	// pfm's OpenCode root is configured whether or not a store exists there
	env.OpenCodeRoots = []string{filepath.Join(home, openCodeRoot)}
	return env
}

// seedOpenCodeStore builds root/opencode.db from testdata/opencode: its schema,
// then the named seed.
func seedOpenCodeStore(t *testing.T, root, seed string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create the OpenCode root: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatalf("open the OpenCode fixture store: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close the OpenCode fixture store: %v", err)
		}
	}()
	for _, name := range []string{"schema", seed} {
		script, err := os.ReadFile(filepath.Join("testdata", "opencode", name+".sql"))
		if err != nil {
			t.Fatalf("read the OpenCode seed %s: %v", name, err)
		}
		if _, err := db.Exec(string(script)); err != nil {
			t.Fatalf("seed the OpenCode store from %s.sql: %v", name, err)
		}
	}
}

// homeMask writes the run's home, as given and resolved, as {HOME}.
func homeMask(t *testing.T, home string) *strings.Replacer {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) > len(home) {
		return strings.NewReplacer(resolved, "{HOME}", home, "{HOME}")
	}
	return strings.NewReplacer(home, "{HOME}", resolved, "{HOME}")
}

// compareGolden compares got with the case's override file label.intended, else its
// capture label.
func compareGolden(t *testing.T, folder, label, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join(folder, label+".intended"))
	if errors.Is(err, fs.ErrNotExist) {
		want, err = os.ReadFile(filepath.Join(folder, label))
	}
	if errors.Is(err, fs.ErrNotExist) {
		want, err = nil, nil
	}
	if err != nil {
		t.Fatalf("read golden %s: %v", label, err)
	}
	if label == "stdout" {
		want = wroteBytes.ReplaceAll(want, []byte("${1}{BYTES}${3}"))
	}
	if got != string(want) {
		t.Errorf("%s differs: %s", label, firstDifference(got, string(want)))
	}
}

// firstDifference names the first line two texts differ on.
func firstDifference(got, want string) string {
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for index := range max(len(gotLines), len(wantLines)) {
		var left, right string
		if index < len(gotLines) {
			left = gotLines[index]
		}
		if index < len(wantLines) {
			right = wantLines[index]
		}
		if left != right {
			return fmt.Sprintf("line %d\n got: %q\nwant: %q", index+1, left, right)
		}
	}
	return "line counts only"
}

// makeLinks creates a case's declared symlinks in its scratch home, each
// parent directory first, in path order.
func makeLinks(t *testing.T, home string, links map[string]string) {
	t.Helper()
	paths := make([]string, 0, len(links))
	for path := range links {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		link := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatalf("case link %s -> %s: create its directory: %v", path, links[path], err)
		}
		if err := os.Symlink(links[path], link); err != nil {
			t.Fatalf("case link %s -> %s: %v", path, links[path], err)
		}
	}
}

// copyHome copies a case's home; a symlink in it is refused, since a case
// declares its links in case.json.
func copyHome(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("symlink %s: declare it under links in the case's case.json", path)
		case entry.IsDir():
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", from, err)
	}
}
