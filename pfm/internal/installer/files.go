package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func sameFile(path string, content []byte, mode fs.FileMode) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode.Perm() {
		return false
	}
	existing, err := os.ReadFile(path)
	return err == nil && bytes.Equal(existing, content)
}

func resolvedLink(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target), true
}

func newestBackup(path string) string {
	matches, _ := filepath.Glob(path + ".pre-professor-*")
	sort.Strings(matches)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func availableBackup(path, stamp string) string {
	base := path + ".pre-professor-" + stamp
	if _, err := os.Lstat(base); errors.Is(err, fs.ErrNotExist) {
		return base
	}
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s.%d", base, index)
		if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			return candidate
		}
	}
}

func copyBackup(source, target string) error {
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	return atomicfile.Write(target, content, info.Mode().Perm())
}

func sourceLine(path string) string {
	return `[[ -r "` + path + `" ]] && source "` + path + `"`
}

// isFleetSourceLine reports whether a ~/.zshrc line is the line that loads the
// shim. Both the rewriter and the early-call scan below ask this question, and
// they must never disagree about where the source line is: one decides where
// the launchers start existing, the other reports what runs before they do.
func isFleetSourceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return !strings.HasPrefix(trimmed, "#") &&
		strings.Contains(trimmed, "source") &&
		(strings.Contains(trimmed, "pfm.zsh") || strings.Contains(trimmed, "cc-fleet.zsh"))
}

// fleetCommandCall matches a fleet launcher INVOKED as a command — at the start
// of a line, or after a separator that begins a new one, and ending where a word
// ends. `alias cc=…`, `PATH=$PATH:/opt/cc` and `$HOME/.cc/2` are definitions and
// paths rather than calls, and deliberately do not match.
//
// The trailing class is not \b: `-` is a non-word character, so a word boundary
// would let `cc` match inside `cc-ls` and report the wrong name.
//
// Longest names lead the alternation: Go's regexp picks the branch a
// backtracking search would find first, so `cc` ahead of `cc-ls` would match
// the prefix, fail the trailing separator, and miss the line.
var fleetCommandCall = regexp.MustCompile(
	`(^|[;&|(){}])[ \t]*(cc-swap|cc-open|cc-ls|cc1|cc2|cc|cx)([ \t;&|)}]|$)`,
)

// earlyFleetCalls returns the ~/.zshrc lines that CALL a fleet command above the
// source line, each formatted for the installer report.
//
// The source line goes at the BOTTOM (see rewriteZshrc), so every line above it
// runs before any launcher exists — and that is not the "command not found" it
// ought to be. `cc` is also the POSIX C compiler and `cx` is a name anything may
// claim, so the shell silently runs a STRANGER. The case this was written for: a
// terminal profile that opened a chat on launch called `cc` from ~/.zshrc, got
// /usr/bin/cc, and greeted every new terminal with "clang: error: no input
// files" while the fleet loaded fine a few lines later — so calling it by hand
// always worked and the bug read as a broken picker.
//
// A ~/.zshrc with no source line at all scans whole: the line is about to be
// appended at the bottom, so everything in the file is above it.
func earlyFleetCalls(content string) []string {
	if content == "" {
		return nil
	}
	var early []string
	for index, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if isFleetSourceLine(line) {
			break
		}
		code := line
		if hash := strings.IndexByte(code, '#'); hash >= 0 {
			code = code[:hash]
		}
		if fleetCommandCall.MatchString(code) {
			early = append(early, fmt.Sprintf("line %d: %s", index+1, line))
		}
	}
	return early
}

func rewriteZshrc(content, wanted string, uninstall bool) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	result := make([]string, 0, len(lines)+3)
	inserted := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		generatedComment := trimmed == "# The shell launchers delegate to the pfm engine." ||
			strings.HasPrefix(trimmed, "# Professor fleet — launchers") ||
			strings.Contains(trimmed, "legacy oracle remains unsourced")
		fleetSource := isFleetSourceLine(line)
		if generatedComment {
			continue
		}
		if fleetSource {
			if !uninstall && !inserted {
				result = append(result, "# The shell launchers delegate to the pfm engine.", wanted)
				inserted = true
			}
			continue
		}
		result = append(result, line)
	}
	if !uninstall && !inserted {
		for len(result) > 0 && result[len(result)-1] == "" {
			result = result[:len(result)-1]
		}
		if len(result) > 0 {
			result = append(result, "")
		}
		result = append(result,
			"# The shell launchers delegate to the pfm engine.",
			wanted,
		)
	}
	for len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	if len(result) == 0 {
		return ""
	}
	return strings.Join(result, "\n") + "\n"
}
