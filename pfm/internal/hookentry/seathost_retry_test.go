package hookentry

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestSeatRetryChoice(t *testing.T) {
	const ask = "press Enter to retry, q to close\n"
	for _, row := range []struct {
		name, typed string
		quit        bool
		code        int
		asked       bool
		stderr      string
	}{
		{name: "Enter retries", typed: "\n", asked: true, stderr: ask},
		{name: "q closes", typed: "q\n", quit: true, asked: true, stderr: ask},
		{name: "another answer is asked again", typed: "x\n q \n", quit: true, asked: true, stderr: ask + ask},
		{
			name: "an input that ends on a terminal still open is a read fault", code: 1,
			stderr: ask + "pfm internal seat-host: read retry choice: EOF\n",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			var stderr bytes.Buffer
			// No terminal: descriptor -1 polls as not hung up.
			quit, code, asked := seatRetryChoice(bufio.NewReader(strings.NewReader(row.typed)), -1, &stderr)
			if quit != row.quit || code != row.code || asked != row.asked || stderr.String() != row.stderr {
				t.Fatalf(
					"seatRetryChoice(%q) = quit %t, code %d, asked %t, stderr %q; want quit %t, code %d, asked %t, stderr %q",
					row.typed,
					quit,
					code,
					asked,
					stderr.String(),
					row.quit,
					row.code,
					row.asked,
					row.stderr,
				)
			}
		})
	}
}
