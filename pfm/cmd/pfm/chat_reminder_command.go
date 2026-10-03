package main

import (
	"io"

	"github.com/rezzminator/professor/pfm/internal/reminder"
)

// runChatReminder dispatches `pfm chat reminder set|ls|rm`; the logic lives in
// internal/reminder, this is the dispatch seam.
func runChatReminder(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	notFound := func(name string, w io.Writer) int { return renderNoSuchChat(name, io.Discard, w, false) }
	return reminder.RunReminderCommand(args, stdout, stderr, firstRuntime(runtimes), notFound)
}

// runReminderFire is `pfm internal reminder-fire`.
func runReminderFire(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	return reminder.RunFire(args, stdout, stderr, &runtime)
}
