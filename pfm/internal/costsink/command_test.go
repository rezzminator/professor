package costsink

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// RunSink's serving path needs the inherited listening descriptor 3 and is played by the real-process
// test in cmd/pfm; here are the refusals that happen before it touches a descriptor.
func TestRunSinkRefusesArgumentsThatNameNoChat(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"nothing", nil},
		{"a pid without a start time", []string{"--pid", "4242"}},
		{"a start time without a pid", []string{"--start", "99"}},
		{"a negative pid", []string{"--pid", "-1", "--start", "99"}},
		{"a stray positional word", []string{"--pid", "4242", "--start", "99", "extra"}},
	} {
		var stderr bytes.Buffer
		if code := RunSink(tc.args, &stderr, paths.OSEnv{}); code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", tc.name, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "usage: pfm internal cost-sink") {
			t.Errorf("%s: stderr %q lacks the usage line", tc.name, stderr.String())
		}
	}
}
