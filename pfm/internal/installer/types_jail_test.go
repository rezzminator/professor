//go:build jail

package installer

import (
	"errors"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestExecCommandRunnerPresentBinaryRecordsItsExit(t *testing.T) {
	for _, output := range []bool{false, true} {
		t.Run(map[bool]string{false: "Run", true: "Output"}[output], func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			var err error
			if output {
				var result []byte
				result, err = (execCommandRunner{}).Output(ctx, "sh", "-c", "printf fixture; exit 7")
				if string(result) != "fixture" {
					t.Fatalf("output=%q", result)
				}
			} else {
				err = (execCommandRunner{}).Run(ctx, "sh", "-c", "exit 7")
			}
			var exit commandExitError
			if !errors.As(err, &exit) || exit.code != 7 {
				t.Fatalf("err=%v", err)
			}
			runs := 0
			for _, record := range recorder.Records() {
				if record.Message == "runner.run" {
					runs++
					code, _ := record.Field(obs.FieldExit)
					if record.Level != "INFO" || code != float64(7) {
						t.Fatalf("record=%+v", record)
					}
				}
			}
			if runs != 1 {
				t.Fatalf("runs=%d: %s", runs, recorder.Raw())
			}
		})
	}
}
