package main

import (
	"io"

	"github.com/rezzminator/professor/pfm/internal/schedule"
)

// runChatSchedule dispatches `pfm chat schedule set|ls|rm`; the logic lives in
// internal/schedule, this is the dispatch seam.
func runChatSchedule(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	notFound := func(name string, w io.Writer) int { return renderNoSuchChat(name, io.Discard, w, false) }
	return schedule.RunScheduleCommand(args, stdout, stderr, firstRuntime(runtimes), notFound)
}

// runScheduleFire is `pfm internal schedule-fire`.
func runScheduleFire(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	return schedule.RunFire(args, stdout, stderr, &runtime)
}
