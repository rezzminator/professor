package statusline

import "github.com/charmbracelet/x/ansi"

// rowPart is one segment of a full row; short is what it becomes once its
// drop rank is reached ("" removes it).
type rowPart struct {
	text, short string
	drop        int
}

func appendRowSegment(line, segment string) string {
	switch {
	case segment == "":
		return line
	case line == "":
		return segment
	}
	return line + rowSep + segment
}

// fitRow joins the parts and, while the row is wider than columns leaves for
// it, gives parts up rank by rank. columns 0 (no width in the payload) keeps
// the full row.
func fitRow(parts []rowPart, columns int) string {
	join := func() string {
		line := ""
		for _, part := range parts {
			line = appendRowSegment(line, part.text)
		}
		return line
	}
	line := join()
	budget := columns - rowPrefixWidth
	for rank := dropCwd; columns > 0 && rank <= dropEffort && ansi.StringWidth(line) > budget; rank++ {
		for index := range parts {
			if parts[index].drop == rank {
				parts[index].text = parts[index].short
			}
		}
		line = join()
	}
	return line
}
