//go:build jail

package harness

import (
	"os"
	"testing"
)

func TestReadReturnsBothStreamsAndTheExitCode(t *testing.T) {
	var got, torn Result
	argv := []string{"sh", "-c", `echo "out $GREETING"; echo err >&2; exit 3`}
	Run(t, Do("root", func(c *Ctx, in int) int {
		got = c.Read(argv...)
		// A teardown runs after testing has ended the step's own context: its Read still runs.
		c.Cleanup(func() { torn = c.Read(argv...) })
		return in
	}), 0, Hooks{Env: []string{"PATH=" + os.Getenv("PATH"), "GREETING=owner"}})
	want := Result{Stdout: "out owner\n", Stderr: "err\n", Code: 3}
	if got != want || torn != want {
		t.Fatalf("Read = %+v, in a Cleanup %+v, want %+v for both", got, torn, want)
	}
}
