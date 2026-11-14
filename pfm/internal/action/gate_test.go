package action

import (
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestDeviceGateWithoutAControllingTerminalRefusesSilentlyOrLogsWhy is DeviceGate's no-tty test. A
// device that does not exist (fs.ErrNotExist opening it) is the ordinary, unremarkable shape of a
// detached invocation and stays quiet; any other open failure (here a directory, EISDIR) is a probe
// that could not run and must leave a line naming what failed, never a bare (false, nil).
func TestDeviceGateWithoutAControllingTerminalRefusesSilentlyOrLogsWhy(t *testing.T) {
	for _, test := range []struct {
		name   string
		device func(dir string) string
		logged bool
	}{
		{"absent device", func(dir string) string { return filepath.Join(dir, "no-tty") }, false},
		{"unopenable device", func(dir string) string { return dir }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			confirmed, err := (DeviceGate{Device: test.device(t.TempDir())}).Confirm(ctx, GateRequest{
				Name:           "Builder",
				BirthAccount:   2,
				PrimaryAccount: 1,
			})
			if err != nil {
				t.Fatalf("Confirm() error = %v, want a graceful refusal", err)
			}
			if confirmed {
				t.Fatal("Confirm() reported confirmed with no controlling terminal")
			}
			found := false
			for _, record := range recorder.Records() {
				if record.Message == "device gate: open /dev/tty failed" {
					found = true
				}
			}
			if found != test.logged {
				t.Fatalf("device-gate log record present = %v, want %v: %s", found, test.logged, recorder.Raw())
			}
		})
	}
}
