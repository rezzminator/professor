package action

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestDeviceGateWithoutAControllingTerminalRefusesSilentlyOrLogsWhy is
// DeviceGate's first test (L1-F14): it had zero before this. A process with
// no controlling terminal at all (fs.ErrNotExist / os.ErrPermission opening
// /dev/tty) is the ordinary, unremarkable shape of a detached invocation and
// stays quiet; anything else (this sandbox's own /dev/tty open fails with
// ENXIO, "no such device or address") is a probe that could not run and must
// leave a line naming what failed, never a bare (false, nil).
func TestDeviceGateWithoutAControllingTerminalRefusesSilentlyOrLogsWhy(t *testing.T) {
	ctx, recorder := obs.Test(t)
	probe, probeErr := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if probeErr == nil {
		_ = probe.Close()
		t.Skip("this environment has a controlling terminal — DeviceGate's no-tty path is not exercised")
	}

	confirmed, err := (DeviceGate{}).Confirm(ctx, GateRequest{
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
	if errors.Is(probeErr, fs.ErrNotExist) || errors.Is(probeErr, os.ErrPermission) {
		// The quiet, ordinary path — every such caller hits this on every
		// invocation, so nothing is logged and there is nothing to assert.
		return
	}
	found := false
	for _, record := range recorder.Records() {
		if record.Message == "device gate: open /dev/tty failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no device-gate log record for the /dev/tty failure (%v): %s", probeErr, recorder.Raw())
	}
}
