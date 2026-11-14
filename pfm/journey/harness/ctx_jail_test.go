//go:build jail

package harness

import (
	"os"
	"testing"
	"time"
)

func TestReadReturnsBothStreamsAndTheExitCode(t *testing.T) {
	var got Result
	Run(t, Do("root", func(c *Ctx, in int) int {
		got = c.Read("sh", "-c", `echo "out $GREETING"; echo err >&2; exit 3`)
		return in
	}), 0, Hooks{Env: []string{"PATH=" + os.Getenv("PATH"), "GREETING=owner"}})
	if want := (Result{Stdout: "out owner\n", Stderr: "err\n", Code: 3}); got != want {
		t.Fatalf("Read = %+v, want %+v", got, want)
	}
}

func TestPollWaitsForItsConditionAndAMissNamesWhatItSaw(t *testing.T) {
	out, failed, parent := inChild(t, func(t *testing.T) {
		Run(t, Do("root", func(c *Ctx, in int) int {
			tries := 0
			c.Poll("the third look", time.Second, func() (bool, string) { tries++; return tries == 3, "" })
			c.Poll("the flag", 50*time.Millisecond, func() (bool, string) { return false, "not yet" })
			return in
		}), 0, Hooks{})
	})
	if !parent {
		return
	}
	if got := lineAfter(out, "root: waited"); got != "root: waited 50ms for the flag; last seen:" || !failed {
		t.Fatalf("poll miss line %q failed=%v, want the flag named and the child failed\n%s", got, failed, out)
	}
	if got := lineAfter(out, "not yet"); got != "not yet" {
		t.Fatalf("poll miss did not print what it last saw\n%s", out)
	}
}
