package hookentry

import (
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// refreshSeatHostImage re-execs this supervisor from the installed pfm when an
// install has replaced the binary it runs. A reload or a retry restarts the
// engine inside this one process, so without it a chat keeps the supervisor
// code of the day it opened, fixes and all left out. Exec keeps the pid and its
// kernel birth, the identity the seat's owner proof keys on, so the new image
// proves the seat's binding as the same supervisor, without the conversation
// lock a waiting reload holds (bindNativeHost), and starts the pending engine.
// It returns only when the running image is current or the swap failed; a
// failure is said on stderr and the running image carries on.
func refreshSeatHostImage(
	proc gather.ProcFS,
	locate func() (string, error),
	replace func(binary string, argv, env []string) error,
	stderr io.Writer,
) {
	binary, err := locate()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: locate the installed pfm, keeping the running one: %v\n", err)
		return
	}
	images, ok := proc.(gather.ProcImage)
	if !ok {
		fmt.Fprintln(stderr, "pfm internal seat-host: this process table cannot read which file the supervisor "+
			"executes, keeping the running pfm")
		return
	}
	installed, err := gather.FileIDOf(binary)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: identify the installed pfm %s, keeping the running one: %v\n",
			binary, err)
		return
	}
	running, err := images.Image(os.Getpid())
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: identify the running pfm, keeping it: %v\n", err)
		return
	}
	if running == installed {
		return
	}
	if err := replace(binary, os.Args, os.Environ()); err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: switch to the installed pfm %s, keeping the running one: %v\n",
			binary, err)
	}
}
