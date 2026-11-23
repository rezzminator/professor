//go:build jail

package costsink

// The sink child itself is the subject: descriptor 3 must really arrive in it as the bound socket, and the
// port must accept connections while it holds the socket.

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// fakeSinkBinary stands in for pfm: invoked as `internal cost-sink --pid N --start S` it records its
// arguments, whether descriptor 3 is a socket and the environment it was given, says one line on
// stderr (which Arm points at the sink log) and then holds the socket until the test writes the stop pipe.
// The record lands whole (written beside, then renamed), so a reader never sees half of it.
func fakeSinkBinary(t *testing.T, dir string) (binary, record string) {
	t.Helper()
	stop := filepath.Join(dir, "stop")
	if err := syscall.Mkfifo(stop, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_STOP", stop)
	t.Cleanup(func() {
		// The child holds the pipe open for reading and writing since before it records anything, so
		// the line waits in the pipe for it; the open here never blocks.
		if release, err := os.OpenFile(stop, os.O_RDWR, 0); err == nil {
			_, _ = release.WriteString("stop\n")
			_ = release.Close()
		}
	})
	binary, record = filepath.Join(dir, "fake-pfm"), filepath.Join(dir, "record.txt")
	script := "#!/bin/sh\n" +
		"exec 4<>\"$FAKE_STOP\"\n" +
		"{ echo \"args: $*\"; if [ -S /dev/fd/3 ]; then echo fd3-socket; else echo fd3-missing; fi; " +
		"echo \"marker: $FAKE_MARKER\"; } > \"$FAKE_RECORD.tmp\" && mv \"$FAKE_RECORD.tmp\" \"$FAKE_RECORD\"\n" +
		"echo \"fake sink started\" >&2\n" +
		"read -r _ <&4\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary, record
}

func waitForFile(t *testing.T, path string) string {
	t.Helper()
	var body string
	testwait.Until(t, path+" to appear", func() (bool, string) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return false, err.Error()
		}
		body = string(raw)
		return len(raw) > 0, "empty file"
	})
	return body
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
	conn, err := (&net.Dialer{}).DialContext(testwait.Context(t), "tcp4", "127.0.0.1:"+strconv.Itoa(armed.Port))
	if err != nil {
		t.Fatalf("dial the armed port %d: %v", armed.Port, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close the dial to the armed port: %v", err)
	}
	logBody := waitForFile(t, paths.LogRoot(paths.OSEnv{}, home).CostSink())
	if logBody != "fake sink started\n" {
		t.Errorf("the sink log %q lacks the child's stderr line", logBody)
	}
}
