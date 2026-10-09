package digest

import (
	"bytes"
	"context"
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
)

// goldenCase is one captured run of the Python reader this package replaced:
// its argument list and the empty directories its home held.
type goldenCase struct {
	Args []string `json:"args"`
	Dirs []string `json:"dirs"`
}

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
	copyHome(t, filepath.Join(folder, "home"), home)
	for _, dir := range golden.Dirs {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
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
	return env
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

// copyHome copies a case's home, its symlinks as links.
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
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
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
