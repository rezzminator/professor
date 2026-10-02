package config

import (
	"os"
	"syscall"
)

// writeExecutableUnderForkLock is testjail.WriteExecutable for this package's
// internal tests, which cannot import testjail: testjail imports config. It holds
// the read side of syscall.ForkLock while the file is open for writing, so no
// child forked meanwhile keeps the write descriptor and makes a later exec of
// the file fail with ETXTBSY ("text file busy").
func writeExecutableUnderForkLock(path string, body []byte, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, body, mode)
}
