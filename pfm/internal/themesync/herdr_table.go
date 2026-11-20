package themesync

import (
	"fmt"
	"regexp"
	"strings"
)

// herdrTableHeader reports whether a header line opens table or one of its
// subtables ([table], [table.x], [[table.x]]).
func herdrTableHeader(text, table string) bool {
	match := herdrHeaderPattern.FindStringSubmatch(text)
	return len(match) > 1 && (match[1] == table || strings.HasPrefix(match[1], table+"."))
}

// HerdrTableSpan finds where a Herdr config holds table: the first [table]
// or [table.*] header through the last such section's last content line,
// trailing blank and comment lines excluded. found is false when no header
// names it. A table set at the root, or split around another table, is an
// error: no span holds it whole. It is the one door the theme block and
// install's keys block place themselves through.
func HerdrTableSpan(content, table string) (start, end int, found bool, err error) {
	lines := herdrLines(content)
	root := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(table) + `\s*[.=]`)
	var headers, tableHeaders []int
	for index, line := range lines {
		if herdrHeaderPattern.MatchString(line.text) {
			headers = append(headers, index)
			if herdrTableHeader(line.text, table) {
				tableHeaders = append(tableHeaders, index)
			}
		} else if len(headers) == 0 && root.MatchString(line.text) {
			return 0, 0, false, fmt.Errorf("themesync: herdr config sets %s at the root (line %d: %q); "+
				"move it under a [%s] table", table, index+1, line.text, table)
		}
	}
	if len(tableHeaders) == 0 {
		return 0, 0, false, nil
	}
	first, last := tableHeaders[0], tableHeaders[len(tableHeaders)-1]
	for _, header := range headers {
		if header > first && header < last && !herdrTableHeader(lines[header].text, table) {
			return 0, 0, false, fmt.Errorf(
				"themesync: herdr config splits its %s tables around line %d (%q); "+
					"keep [%s] and [%s.*] together",
				table,
				header+1,
				lines[header].text,
				table,
				table,
			)
		}
	}
	endLine := len(lines) // exclusive index of the last line the span may hold
	for _, header := range headers {
		if header > last {
			endLine = header
			break
		}
	}
	for endLine-1 > last {
		trimmed := strings.TrimSpace(lines[endLine-1].text)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		endLine--
	}
	return lines[first].start, lines[endLine-1].end, true, nil
}
