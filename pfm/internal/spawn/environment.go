package spawn

import "context"

type backendEnvironmentKey struct{}

// WithBackendEnvironment binds the launch surface's environment. Shared daemons
// bind an empty environment and supply only their verified requester seat.
func WithBackendEnvironment(ctx context.Context, environ []string) context.Context {
	return context.WithValue(ctx, backendEnvironmentKey{}, append([]string{}, environ...))
}

// BackendEnvironment returns request-local values, or the direct CLI fallback.
func BackendEnvironment(ctx context.Context, fallback []string) []string {
	if ctx != nil {
		if environ, ok := ctx.Value(backendEnvironmentKey{}).([]string); ok {
			return append([]string{}, environ...)
		}
	}
	return append([]string{}, fallback...)
}
