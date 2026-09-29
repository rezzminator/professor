package testjail

import (
	"os"
	"syscall"
	"testing"
)

// WriteExecutable writes an executable fixture at path and fails the test on
// error. Use it for any script a parallel test writes and then runs.
//
// A fork anywhere in the process while the file is open for writing hands the
// child that write descriptor until the child execs, and exec of the fixture
// then fails with ETXTBSY ("text file busy") — a flake that grows with test
// parallelism. Holding syscall.ForkLock for writing across open, write and
// close keeps every os/exec fork out of that window (golang/go#22315).
func WriteExecutable(t *testing.T, path string, content []byte) {
	t.Helper()
	syscall.ForkLock.Lock()
	err := os.WriteFile(path, content, 0o700)
	syscall.ForkLock.Unlock()
	if err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
