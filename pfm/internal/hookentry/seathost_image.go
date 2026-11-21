package hookentry

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/binwatch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// refreshSeatHostImage re-execs this supervisor from the installed pfm when an
// install has replaced the binary it runs. A reload or a retry restarts the
// engine inside this one process, so without it a chat keeps the supervisor
// code of the day it opened, fixes and all left out. Exec keeps the pid and its
// kernel birth, the identity the seat's owner proof keys on, so the new image
// proves the seat's binding as the same supervisor, without the conversation
// lock a waiting reload holds (bindNativeHost), and starts the pending engine.
// image is the executable this process started from (binwatch.OwnImage at
// start, imageErr its failure); environ is the new image's environment, an
// ended engine's carrying paths.EnvSeatHostEnd (seatHostEndEnviron). It returns
// only when the running image is current or the swap failed; a failure is said
// on stderr and the running image carries on.
func refreshSeatHostImage(
	image binwatch.Image,
	imageErr error,
	replace func(binary string, argv, env []string) error,
	environ []string,
	stderr io.Writer,
) {
	if imageErr != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: locate the installed pfm, keeping the running one: %v\n", imageErr)
		return
	}
	replaced, err := image.Replaced()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: identify the installed pfm %s, keeping the running one: %v\n",
			image.Path, err)
		return
	}
	if !replaced {
		return
	}
	if err := replace(image.Path, os.Args, environ); err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: switch to the installed pfm %s, keeping the running one: %v\n",
			image.Path, err)
	}
}

const seatHostEndFaulted = ":faulted"

// seatHostEndEnviron is this process's environment with paths.EnvSeatHostEnd
// naming an engine's end: its exit status, and whether its run faulted.
func seatHostEndEnviron(status int, faulted bool) []string {
	end := strconv.Itoa(status)
	if faulted {
		end += seatHostEndFaulted
	}
	return seatEnvironment(os.Environ(), []string{paths.EnvSeatHostEnd + "=" + end}, []string{paths.EnvSeatHostEnd})
}

// parseSeatHostEnd reads the paths.EnvSeatHostEnd value seatHostEndEnviron wrote.
func parseSeatHostEnd(end string) (status int, faulted bool, err error) {
	number, faulted := strings.CutSuffix(end, seatHostEndFaulted)
	status, err = strconv.Atoi(number)
	if err != nil || status < 0 || status > 255 {
		return 0, false, fmt.Errorf("engine end %q is no exit status 0-255", end)
	}
	return status, faulted, nil
}

// resumeSeatHostEnd finishes, on the installed pfm, the engine end the replaced
// image handed over through paths.EnvSeatHostEnd: the seat's binding must name
// this very process, the same pid and kernel birth the exec kept. reloading is
// true when the seat awaits a reload after a clean run; the caller then starts
// the pending engine as any re-executed supervisor does.
func resumeSeatHostEnd(
	ctx context.Context,
	db *fleetdb.Store,
	id, end string,
	stderr io.Writer,
	runtime config.Runtime,
) (code int, reloading bool) {
	// A later engine start's own re-exec must never read this end as its own.
	if err := os.Unsetenv(paths.EnvSeatHostEnd); err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: clear the resumed engine end: %v\n", err)
		return 1, false
	}
	status, faulted, err := parseSeatHostEnd(end)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: resume seat %s: %v\n", id, err)
		return 1, false
	}
	host := fleetdb.Seat{SeatID: id}
	if err := gather.StampHerdrHost(gather.NewProcFS(runtime.Paths.ProcRoot), &host, os.Getpid()); err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: resume seat %s: %v\n", id, err)
		return 1, false
	}
	_, reloading, code = seatEngineEnded(ctx, db, host, status, faulted, stderr, runtime)
	return code, reloading
}
