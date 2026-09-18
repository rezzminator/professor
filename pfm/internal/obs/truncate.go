package obs

import (
	"strconv"
	"unicode/utf8"
)

// MaxValueBytes is the field cap of spec § Middleware: at DEBUG a middleware
// may dump its full scrubbed arguments, and every string field is cut at 2 KB
// with a marker naming what was dropped. It runs inside the handler (Scrub),
// so no call site can write more than this per field.
const MaxValueBytes = 2048

// truncatedMarker opens the marker a cut value ends with: `…(truncated N bytes)`.
const truncatedMarker = "…(truncated "

// TruncateValue returns value whole when it fits MaxValueBytes, else its
// MaxValueBytes head — never split inside a rune — plus the marker counting the
// bytes that did not survive.
func TruncateValue(value string) string {
	if len(value) <= MaxValueBytes {
		return value
	}
	cut := MaxValueBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + truncatedMarker + strconv.Itoa(len(value)-cut) + " bytes)"
}
