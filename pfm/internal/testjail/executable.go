package testjail

import (
	"os"
	"syscall"
)

// WriteExecutable writes a file this process or one of its children may later
// execute: a fake engine, a wrapper script, a stub on a jailed PATH. It holds
// the read side of syscall.ForkLock while the file is open for writing, which
// keeps every fork of this process out of that window. A child forked inside it
// inherits the write descriptor until its own exec closes it, and executing the
// file meanwhile fails with ETXTBSY ("text file busy"); parallel tests fork
// constantly. Every test write whose mode may carry an exec bit goes through
// here — arch-check C26 holds the rule. A package this one imports cannot
// import it back; such a package takes the same lock around its own write and
// points here.
func WriteExecutable(path string, body []byte, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, body, mode)
}
