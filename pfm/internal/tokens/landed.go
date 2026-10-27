package tokens

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// landedRules names the exclusion rules landed code lines are counted by;
// it changes whenever a rule does, so two trees compare only under one name.
// A changed path counts unless it is, checked in this order:
//
//   - binary: a numstat row of "-", or an untracked file holding a NUL byte
//     in its first 8000 bytes;
//   - lock: go.sum, *.lock, package-lock.json, pnpm-lock.yaml, yarn.lock,
//     npm-shrinkwrap.json;
//   - generated: under templates/generated/ or pfm/harness-prompts/generated/,
//     *.pb.go, *_gen.go, *.gen.*, or a file whose first 5 lines hold
//     "Code generated" and then "DO NOT EDIT";
//   - test: *_test.go, test_*.py, *_test.py, *.test.*, *.spec.*, or any
//     path segment test, tests, testdata, __tests__ or fixtures;
//   - doc: *.md, *.txt, *.rst, or any path segment docs.
const landedRules = "landed-rules/1"

// The exclusion classes, in the order a path is checked against them.
const (
	classBinary    = "binary"
	classLock      = "lock"
	classGenerated = "generated"
	classTest      = "test"
	classDoc       = "doc"
)

var (
	lockNames = map[string]bool{
		"go.sum": true, "package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
		"npm-shrinkwrap.json": true,
	}
	testSegments  = map[string]bool{"test": true, "tests": true, "testdata": true, "__tests__": true, "fixtures": true}
	generatedDirs = []string{"templates/generated/", "pfm/harness-prompts/generated/"}
	generatedMark = regexp.MustCompile(`(?s)Code generated.*DO NOT EDIT`)
)

// generatedLines is how many first lines carry a generated-file mark;
// binarySniff how many first bytes of an untracked file a NUL marks binary in.
const (
	generatedLines = 5
	binarySniff    = 8000
)

// landedLines is what the tree's worktree landed against its base: the code
// lines counted, and the lines (files, for binary) each class excluded.
type landedLines struct {
	top                                 string
	code                                int64
	tests, docs, generated, locks       int64
	binaryFiles                         int64
	trackedFiles, untrackedFiles, files int64
}

// pathClass is the exclusion class of a repository-relative path whose first
// lines are head ("" when unreadable or deleted); "" when its lines count.
func pathClass(path string, head []byte) string {
	slashed := filepath.ToSlash(path)
	segments := strings.Split(slashed, "/")
	name := segments[len(segments)-1]
	switch {
	case lockNames[name] || strings.HasSuffix(name, ".lock"):
		return classLock
	case isGenerated(slashed, name, head):
		return classGenerated
	case isTest(name, segments):
		return classTest
	case isDoc(name, segments):
		return classDoc
	}
	return ""
}

func isGenerated(slashed, name string, head []byte) bool {
	for _, dir := range generatedDirs {
		if strings.HasPrefix(slashed, dir) {
			return true
		}
	}
	if strings.HasSuffix(name, ".pb.go") || strings.HasSuffix(name, "_gen.go") || strings.Contains(name, ".gen.") {
		return true
	}
	return generatedMark.Match(head)
}

func isTest(name string, segments []string) bool {
	if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, "_test.py") ||
		(strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py")) ||
		strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") {
		return true
	}
	for _, segment := range segments {
		if testSegments[segment] {
			return true
		}
	}
	return false
}

func isDoc(name string, segments []string) bool {
	switch filepath.Ext(name) {
	case ".md", ".txt", ".rst":
		return true
	}
	for _, segment := range segments[:len(segments)-1] {
		if segment == "docs" {
			return true
		}
	}
	return false
}

// add books n lines of a path in class.
func (l *landedLines) add(class string, n int64) {
	switch class {
	case "":
		l.code += n
	case classBinary:
		l.binaryFiles++
	case classLock:
		l.locks += n
	case classGenerated:
		l.generated += n
	case classTest:
		l.tests += n
	case classDoc:
		l.docs += n
	}
}

// git runs one git command and returns its stdout; a failure is an error
// naming the command, never an empty answer.
func git(ctx context.Context, runner deps.Runner, args ...string) ([]byte, error) {
	argv := append([]string{"git"}, args...)
	result, err := runner.Run(ctx, argv, deps.RunOptions{})
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return result.Stdout, nil
}

// measureLanded counts the code lines dir's repository landed against base:
// added plus removed lines of `git diff --numstat base` (the working tree,
// staged or not, against base) plus every untracked, unignored file's lines,
// each path classed by pathClass. dir resolves to its repository's top level,
// so the diff and the untracked list name the same paths.
func measureLanded(ctx context.Context, runner deps.Runner, dir, base string) (*landedLines, error) {
	topOut, err := git(ctx, runner, "-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	top := strings.TrimSpace(string(topOut))
	landed := &landedLines{top: top}
	numstat, err := git(ctx, runner, "-C", top, "diff", "--numstat", "-z", base)
	if err != nil {
		return nil, err
	}
	if err := landed.readNumstat(numstat); err != nil {
		return nil, fmt.Errorf("git -C %s diff --numstat -z %s: %w", top, base, err)
	}
	others, err := git(ctx, runner, "-C", top, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, path := range strings.Split(strings.TrimSuffix(string(others), "\x00"), "\x00") {
		if path == "" {
			continue
		}
		if err := landed.addUntracked(path); err != nil {
			return nil, err
		}
	}
	return landed, nil
}

// readNumstat books every row of `git diff --numstat -z`: "added\tremoved\tpath\0",
// a rename "added\tremoved\t\0old\0new\0" booked under its new path.
func (l *landedLines) readNumstat(out []byte) error {
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		if fields[i] == "" {
			continue
		}
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			return fmt.Errorf("unreadable numstat row %q", fields[i])
		}
		path := parts[2]
		if path == "" {
			if i+2 >= len(fields) {
				return fmt.Errorf("numstat rename row %q names no paths", fields[i])
			}
			path = fields[i+2]
			i += 2
		}
		l.trackedFiles++
		l.files++
		if parts[0] == "-" || parts[1] == "-" {
			l.add(classBinary, 0)
			continue
		}
		added, addErr := strconv.ParseInt(parts[0], 10, 64)
		removed, removeErr := strconv.ParseInt(parts[1], 10, 64)
		if addErr != nil || removeErr != nil {
			return fmt.Errorf("unreadable numstat counts in %q", fields[i])
		}
		head, err := readHead(filepath.Join(l.top, path))
		if err != nil {
			return err
		}
		l.add(pathClass(path, head), added+removed)
	}
	return nil
}

// addUntracked books an untracked file: its lines, all of them added.
// A symlink or any other non-regular file carries no lines of its own and
// books as binary.
func (l *landedLines) addUntracked(path string) error {
	full := filepath.Join(l.top, path)
	info, err := os.Lstat(full)
	if err != nil {
		return fmt.Errorf("stat untracked %s: %w", full, err)
	}
	l.untrackedFiles++
	l.files++
	if !info.Mode().IsRegular() {
		l.add(classBinary, 0)
		return nil
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("read untracked %s: %w", full, err)
	}
	if bytes.IndexByte(content[:min(len(content), binarySniff)], 0) >= 0 {
		l.add(classBinary, 0)
		return nil
	}
	lines := int64(bytes.Count(content, []byte("\n")))
	if len(content) != 0 && content[len(content)-1] != '\n' {
		lines++
	}
	l.add(pathClass(path, firstLines(content)), lines)
	return nil
}

// readHead is the first generatedLines lines of a tracked file; a file the
// diff deleted has none.
func readHead(path string) ([]byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	head, readErr := io.ReadAll(io.LimitReader(file, 64<<10))
	if closeErr := file.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}
	if readErr != nil {
		return nil, fmt.Errorf("read %s: %w", path, readErr)
	}
	return firstLines(head), nil
}

// firstLines is content cut to its first generatedLines lines.
func firstLines(content []byte) []byte {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 4096), len(content)+1)
	var out []byte
	for n := 0; n < generatedLines && scanner.Scan(); n++ {
		out = append(append(out, scanner.Bytes()...), '\n')
	}
	return out
}
