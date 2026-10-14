package main

import (
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

// statedTestSender gives a jailed CLI test the identity a real caller has.
//
// A jail has no tmux server of its own, no session id and no process ancestry
// to recover a handle from, so an call launched inside one derives NOTHING —
// and pfm now REFUSES to send an unsigned message rather than handing the
// recipient an instruction from nobody. Stating the sender is what a detached
// or scrubbed real caller is told to do (`CHAT_SENDER_SESSION=$(pfm whoami)`),
// so a fixture that states it is exercising the supported path rather than
// working around the guard.
//
// Never reach for --allow-unsigned here: that flag skips the very check these
// tests share a code path with.
func statedTestSender(t *testing.T) {
	t.Helper()
	t.Setenv(pfmcall.SenderSessionEnv, "cc-1700000000-1-1")
	t.Setenv(pfmcall.SenderLabelEnv, "jail-fixture")
	t.Setenv(pfmcall.SenderIDEnv, "00000000-0000-4000-8000-000000000000")
}
