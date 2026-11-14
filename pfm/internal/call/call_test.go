package call

import (
	"context"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// A process outside every chat derives no sender. That is worth one line only when a message would carry the
// sender's signature: a harness command is never signed, and one command reports it once however many times
// it signs: a live try and the resume body after it under one ShareSender.
func TestSenderFailureIsReportedOnceAndOnlyForASignedMessage(t *testing.T) {
	const noSender = "pfm: identify message sender: ERROR: this process is not inside a pfm chat: " +
		"no live tmux pane or Herdr pane holds it\n"
	for _, tc := range []struct {
		name, message, typed, warnings string
		resume                         bool
	}{
		{
			name: "signed message", message: "who is speaking", warnings: noSender,
			typed: "who is speaking  — UNSIGNED — sender identity underivable; " +
				"no tmux session (ambient or via ancestry); no session id",
		},
		{name: "harness command", message: "/rename Luna", typed: "/rename Luna"},
		{
			name: "signed message then its resume body", message: "ping", warnings: noSender, resume: true,
			typed: "ping  — UNSIGNED — sender identity underivable; no tmux session (ambient or via ancestry); no session id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
			engine := newSignatureEngineWith(t, "cc-1-2-3", fake, fakeIdentifier{err: resolve.ErrNotInChat},
				&fakeSpawner{}, func(options *Options) { options.AllowUnsigned = true })
			var warnings strings.Builder
			engine.warningWriter = &warnings
			ctx := context.Background()
			if tc.resume {
				ctx = engine.ShareSender(ctx, tc.message)
			}
			result, err := engine.Call(ctx, Request{Target: "chat", Message: tc.message})
			if err != nil || result.Code != 0 {
				t.Fatalf("Call(%q) = %+v, %v", tc.message, result, err)
			}
			if got := lastLiteral(fake); got != tc.typed || warnings.String() != tc.warnings {
				t.Fatalf("Call(%q) typed %q and warned %q, want %q and %q",
					tc.message, got, warnings.String(), tc.typed, tc.warnings)
			}
			if !tc.resume {
				return
			}
			prepared, err := engine.PrepareForResume(ctx, "11111111-2222-4333-8444-555555555555", "cc", tc.message)
			want := tc.message + "\n\n— UNSIGNED — sender identity underivable; " +
				"no tmux session (ambient or via ancestry); no session id"
			if err != nil || prepared.Message != want || !prepared.Unsigned || warnings.String() != tc.warnings {
				t.Fatalf("resume body %+v, %v after warnings %q; want %q unsigned, warnings still %q",
					prepared, err, warnings.String(), want, tc.warnings)
			}
		})
	}
}
