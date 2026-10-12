package costsink

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakeSinkBinary stands in for pfm: invoked as `internal cost-sink --pid N --start S` it records its
// arguments, whether descriptor 3 is a socket and the environment it was given, says one line on
// stderr (which Arm points at the sink log) and then holds the socket until the stop file appears.
// The record lands whole (written beside, then renamed), so a reader never sees half of it.
func fakeSinkBinary(t *testing.T, dir string) (binary, record string) {
	t.Helper()
	stop := filepath.Join(dir, "stop")
	t.Setenv("FAKE_STOP", stop)
	t.Cleanup(func() { _ = os.WriteFile(stop, []byte("stop"), 0o600) })
	binary, record = filepath.Join(dir, "fake-pfm"), filepath.Join(dir, "record.txt")
	script := "#!/bin/sh\n" +
		"{ echo \"args: $*\"; if [ -S /dev/fd/3 ]; then echo fd3-socket; else echo fd3-missing; fi; " +
		"echo \"marker: $FAKE_MARKER\"; } > \"$FAKE_RECORD.tmp\" && mv \"$FAKE_RECORD.tmp\" \"$FAKE_RECORD\"\n" +
		"echo \"fake sink started\" >&2\n" +
		"while [ ! -e \"$FAKE_STOP\" ]; do sleep 0.05; done\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary, record
}

func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if body, err := os.ReadFile(path); err == nil && len(body) > 0 {
			return string(body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

func TestArmHandsTheBoundSocketToADetachedSinkAndReturnsItsPort(t *testing.T) {
	home := t.TempDir()
	binary, record := fakeSinkBinary(t, t.TempDir())
	self := os.Getpid()
	stat, err := gather.NewProcFS("").Stat(self)
	if err != nil {
		t.Fatalf("read our own start time: %v", err)
	}
	armed, err := Arm(ArmOptions{
		Self: binary,
		PID:  self,
		Home: home,
		Environ: []string{
			"PATH=/usr/bin:/bin",
			"FAKE_RECORD=" + record,
			"FAKE_MARKER=kept",
			"FAKE_STOP=" + os.Getenv("FAKE_STOP"),
		},
	})
	if err != nil {
		t.Fatalf("Arm: %v", err)
	}
	if armed.Port <= 0 {
		t.Fatalf("Arm returned port %d", armed.Port)
	}
	got := waitForFile(t, record)
	for _, want := range []string{
		"args: internal cost-sink --pid " + strconv.Itoa(self) + " --start " + strconv.FormatUint(stat.StartTime, 10),
		"fd3-socket",
		"marker: kept",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the sink process recorded %q, want it to contain %q", got, want)
		}
	}
	// The port was bound before the child existed and the child holds the socket, so the kernel
	// accepts a connection even though the fake child never serves it.
	conn, err := net.DialTimeout("tcp4", "127.0.0.1:"+strconv.Itoa(armed.Port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial the armed port %d: %v", armed.Port, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close the dial to the armed port: %v", err)
	}
	logBody := waitForFile(t, paths.CostSinkLog(home))
	if !strings.Contains(logBody, "fake sink started") {
		t.Errorf("the sink log %q lacks the child's stderr line", logBody)
	}
}

func TestArmRefusesWhatItCannotServe(t *testing.T) {
	home := t.TempDir()
	binary, _ := fakeSinkBinary(t, t.TempDir())
	for _, tc := range []struct {
		name string
		opts ArmOptions
		want string
	}{
		{"no pid", ArmOptions{Self: binary, Home: home}, "arm needs a pid"},
		{"no binary", ArmOptions{PID: os.Getpid(), Home: home}, "arm needs a pid"},
		{"a pid that is not running", ArmOptions{Self: binary, PID: 1 << 30, Home: home}, "start time of pid"},
		{"a binary that cannot start", ArmOptions{Self: filepath.Join(home, "missing"), PID: os.Getpid(), Home: home}, "internal cost-sink"},
	} {
		if _, err := Arm(tc.opts); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Arm error = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}
