package tmux

import "strings"

// Separator is the ASCII unit separator a format string joins fields with.
const Separator = "\x1f"

// Escaped is how a tmux that refuses to emit control bytes spells Separator.
const Escaped = `\037`

// FormatJoin builds a format string from tmux field specifiers.
func FormatJoin(fields ...string) string {
	return strings.Join(fields, Separator)
}

// FormatSplit parses the output of a tmux -F format string with either control-separator spelling.
// Every probe asks tmux for fields joined by the ASCII unit separator (0x1F),
// a byte no pane title, path or window name can contain. Older tmux builds
// render it as the four printable characters \037, while newer builds emit the
// raw byte.
func FormatSplit(line string, count int) []string {
	return strings.SplitN(strings.ReplaceAll(line, Escaped, Separator), Separator, count)
}
