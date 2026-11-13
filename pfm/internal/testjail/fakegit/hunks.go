package fakegit

import (
	"fmt"
	"strings"
)

// fakeGitChange is one run of changed lines: a[aStart:aEnd] became b[bStart:bEnd].
type fakeGitChange struct{ aStart, aEnd, bStart, bEnd int }

// fakeGitContext is the lines of context around each change.
const fakeGitContext = 3

// fakeGitChanges is the line changes from a to b: the common head and tail
// are kept, as git trims them, and the rest pairs lines along their longest
// common subsequence. It refuses when that pairing is not the only one, or a
// pure insertion or deletion could slide along equal lines: there git's
// heuristics choose, and the model does not play them (a replacement stays
// aligned with its other side, as git keeps it).
func fakeGitChanges(a, b []string) ([]fakeGitChange, error) {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	midA, midB := a[prefix:len(a)-suffix], b[prefix:len(b)-suffix]
	n, m := len(midA), len(midB)
	if n*m > 4_000_000 {
		return nil, fmt.Errorf("a diff of %d by %d changed lines is not modelled", n, m)
	}
	// length[i][j] is the longest common subsequence of midA[i:] and midB[j:],
	// count[i][j] the number of distinct pairings reaching it, capped at 2.
	length, count := make([][]int, n+1), make([][]int, n+1)
	for i := range length {
		length[i], count[i] = make([]int, m+1), make([]int, m+1)
	}
	for i := n; i >= 0; i-- {
		for j := m; j >= 0; j-- {
			if i == n || j == m {
				count[i][j] = 1
				continue
			}
			best := max(length[i+1][j], length[i][j+1])
			if midA[i] == midB[j] {
				best = max(best, length[i+1][j+1]+1)
			}
			length[i][j] = best
			total := 0
			if midA[i] == midB[j] && length[i+1][j+1]+1 == best {
				total += count[i+1][j+1]
			}
			if length[i+1][j] == best {
				total += count[i+1][j]
			}
			if length[i][j+1] == best {
				total += count[i][j+1]
			}
			if length[i+1][j+1] == best {
				total -= count[i+1][j+1]
			}
			count[i][j] = min(total, 2)
		}
	}
	if count[0][0] > 1 {
		return nil, fmt.Errorf("a diff whose lines pair more than one way is not modelled")
	}
	var changes []fakeGitChange
	i, j := 0, 0
	for i < n || j < m {
		startI, startJ := i, j
		for i < n && j < m && (midA[i] != midB[j] || length[i][j] != length[i+1][j+1]+1) {
			if length[i+1][j] == length[i][j] {
				i++
			} else {
				j++
			}
		}
		if i == n || j == m {
			i, j = n, m
		}
		if i > startI || j > startJ {
			changes = append(changes, fakeGitChange{prefix + startI, prefix + i, prefix + startJ, prefix + j})
		}
		if i < n && j < m {
			i, j = i+1, j+1
		}
	}
	for _, change := range changes {
		if (change.bStart == change.bEnd && fakeGitSlides(a, change.aStart, change.aEnd)) ||
			(change.aStart == change.aEnd && fakeGitSlides(b, change.bStart, change.bEnd)) {
			return nil, fmt.Errorf("a diff whose change could slide along equal lines is not modelled")
		}
	}
	return changes, nil
}

// fakeGitSlides reports whether the changed run lines[start:end] could move a
// line up or down and still describe the same edit.
func fakeGitSlides(lines []string, start, end int) bool {
	return start < end &&
		((start > 0 && lines[start-1] == lines[end-1]) || (end < len(lines) && lines[end] == lines[start]))
}

// fakeGitFuncname is the hunk header's context: git's default, the nearest
// line above the hunk that opens with an ASCII letter, "_" or "$", cut to 80
// bytes and stripped of trailing white space.
func fakeGitFuncname(above []string) string {
	for i := len(above) - 1; i >= 0; i-- {
		line := above[i]
		if line == "" {
			continue
		}
		if first := line[0]; ('a' <= first && first <= 'z') || ('A' <= first && first <= 'Z') || first == '_' ||
			first == '$' {
			return " " + strings.TrimRight(line[:min(len(line), 80)], " \t\n\v\f\r")
		}
	}
	return ""
}

// fakeGitLines splits text into lines, each keeping its newline.
func fakeGitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// fakeGitRange is a hunk side: "start,count", the count left out when it is
// one, and the start naming the line before an empty side.
func fakeGitRange(start, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", start)
	case 1:
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}
