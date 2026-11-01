package servicemanager

import "context"

// UserSystemctlArgv is the argv of `systemctl --user args…`, for a caller that
// names the command before it runs.
func UserSystemctlArgv(args ...string) []string {
	return append([]string{"systemctl", "--user"}, args...)
}

// UserSystemctl runs `systemctl --user args…` once through runner.
func UserSystemctl(ctx context.Context, runner Runner, args ...string) Answer {
	return answerOf(ctx, runner, UserSystemctlArgv(args...))
}
