package deps

import "context"

// CommitGate opens a commit section: a span that must run to its end once it
// starts, such as starting a detached process and recording it. ok=false
// means the caller's deadline has passed and nothing may start; end closes
// the section and is called exactly once, after the span's last write.
type CommitGate func() (end func(), ok bool)

type commitGateKey struct{}

// WithCommitGate returns ctx carrying gate, so a span deep in the call tree
// asks the process owner (a prompt hook bounded by its deadline) before it
// starts.
func WithCommitGate(ctx context.Context, gate CommitGate) context.Context {
	return context.WithValue(ctx, commitGateKey{}, gate)
}

// BeginCommit opens a commit section on ctx's gate. With no gate on ctx it
// always opens and end does nothing. A span that opened runs its writes on
// context.WithoutCancel(ctx), so the deadline that closed the gate never
// cuts it in half.
func BeginCommit(ctx context.Context) (end func(), ok bool) {
	gate, found := ctx.Value(commitGateKey{}).(CommitGate)
	if !found || gate == nil {
		return func() {}, true
	}
	return gate()
}
