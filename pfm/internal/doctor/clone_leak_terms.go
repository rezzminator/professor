package doctor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// printLeakTermsDoctor reports the clone's private leak denylist, which the
// pre-push leak gate (scripts/leak-check.sh) refuses every push without. The
// count follows the gate's own reading: '#' comments and blank lines skip,
// '!' lines are directives (an unknown one, or !structural-only beside a term,
// an ignore or an identity, fails the gate), every other line is one term; a
// file with no '!identity' line is NO-IDENTITY, since the gate refuses a push
// no commit address may be matched against. Every warning carries
// warnLeakTerms, since a host that never pushes needs none of them.
func printLeakTermsDoctor(stdout io.Writer, clone string, filter warningFilter) int {
	if _, err := os.Stat(filepath.Join(clone, "scripts", "leak-check.sh")); err != nil {
		fmt.Fprintf(stdout, "doctor: leak_terms does not apply: %s has no scripts/leak-check.sh\n", clone)
		return 0
	}
	path := filepath.Join(clone, "scripts", "leak-terms.txt")
	fix := "fix: write " + path + " by hand, one lowercase regex per line ('#' comments), arming a term only once " +
		"git -C " + clone + " grep -iE '{term}' finds nothing"
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return filter.warn(stdout, warnLeakTerms,
			"doctor: leak_terms ABSENT "+path+" — the pre-push leak gate refuses every push", fix)
	}
	if err == nil && !info.Mode().IsRegular() {
		// The gate refuses anything but a regular file; opening a FIFO would hang doctor.
		return filter.warn(stdout, warnLeakTerms,
			"doctor: leak_terms BROKEN "+path+" is not a regular file — the pre-push leak gate refuses every push", "")
	}
	var file *os.File
	if err == nil {
		file, err = os.Open(path)
	}
	if err != nil {
		return filter.warn(
			stdout,
			warnLeakTerms,
			fmt.Sprintf("doctor: leak_terms UNREADABLE %s error=%v", path, err),
			"",
		)
	}
	defer func() { _ = file.Close() }()
	terms, ignores, identities, structuralOnly, unknown := 0, 0, 0, false, ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		case line == "!structural-only":
			structuralOnly = true
		case strings.HasPrefix(line, "!ignore-token ") || strings.HasPrefix(line, "!ignore-path "):
			ignores++
		case strings.HasPrefix(line, "!identity "):
			identities++
		case strings.HasPrefix(line, "!"):
			if unknown == "" {
				unknown = line
			}
		default:
			terms++
		}
	}
	if err := scanner.Err(); err != nil {
		return filter.warn(
			stdout,
			warnLeakTerms,
			fmt.Sprintf("doctor: leak_terms UNREADABLE %s error=%v", path, err),
			"",
		)
	}
	identityFix := "fix: add one line '!identity {address regex}' to " + path +
		" per address this host commits under, matched whole against each new commit's author and committer"
	const refused = " — the pre-push leak gate refuses every push"
	switch {
	case unknown != "":
		return filter.warn(stdout, warnLeakTerms,
			"doctor: leak_terms BROKEN "+path+": unknown directive "+unknown+refused, "")
	case structuralOnly && terms+ignores+identities > 0:
		return filter.warn(stdout, warnLeakTerms, "doctor: leak_terms BROKEN "+path+
			": !structural-only cannot be mixed with private terms or ignores"+refused, "")
	case structuralOnly:
		fmt.Fprintf(stdout, "doctor: leak_terms structural-only %s — private terms deliberately off\n", path)
	case terms == 0:
		return filter.warn(stdout, warnLeakTerms,
			"doctor: leak_terms EMPTY "+path+" has zero usable terms"+refused, fix)
	case identities == 0:
		return filter.warn(stdout, warnLeakTerms,
			"doctor: leak_terms NO-IDENTITY "+path+" names no address commits may be pushed under"+refused, identityFix)
	default:
		fmt.Fprintf(stdout, "doctor: leak_terms ok %s terms=%d identities=%d\n", path, terms, identities)
	}
	return 0
}
