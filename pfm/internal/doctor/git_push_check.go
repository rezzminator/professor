package doctor

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// gitPushFix is the one command that points git's github.com credentials at gh.
const gitPushFix = "gh auth setup-git --hostname github.com"

// githubOrigin matches an https or ssh github.com remote, capturing the
// scheme marker, owner and repository.
var githubOrigin = regexp.MustCompile(
	`^(?:(https)://(?:[^@/]+@)?github\.com/|(?:ssh://)?git@github\.com[:/])([^/]+)/([^/]+?)(?:\.git)?/?$`,
)

// gitPushProbeEnv is the environment every credential probe runs in: no
// terminal prompt, no askpass program (an empty GIT_ASKPASS stops git before
// core.askPass and SSH_ASKPASS), and Git Credential Manager told never to open
// a window, so the probe answers from what is stored or fails, never asks.
func gitPushProbeEnv() []string {
	return append(deps.WithoutGitRepoVars(os.Environ()),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GCM_INTERACTIVE=never")
}

// printGitPushDoctor asks, read-only, whether git can push the clone's
// github.com origin: the credential helper serving github.com, the user it
// answers, and the push permission gh reports for the repository. Every probe
// that could not run is its own warning, never a clean row; every warning
// carries warnGitPush, since a host that never pushes needs none of them.
func printGitPushDoctor(
	ctx context.Context, stdout io.Writer, clone string, runner deps.Runner, filter warningFilter,
) int {
	origin, err := cloneGit(ctx, runner, clone, nil, "remote", "get-url", "origin")
	if err != nil {
		return filter.warn(stdout, warnGitPush,
			fmt.Sprintf("doctor: git_push UNVERIFIED could not read %s's origin: %v", clone, err), "")
	}
	match := githubOrigin.FindStringSubmatch(origin)
	switch {
	case match == nil:
		fmt.Fprintf(stdout, "doctor: git_push does not apply: origin %s is not on github.com\n", origin)
		return 0
	case match[1] == "":
		fmt.Fprintf(stdout, "doctor: git_push origin %s is ssh: git pushes with an ssh key, which this row does not "+
			"check\n", origin)
		return 0
	}
	repository := match[2] + "/" + match[3]
	fix := "fix: " + gitPushFix
	probe := deps.RunOptions{Env: gitPushProbeEnv()}
	// git config exits 1 when no value is set; any other failure is a lookup
	// that could not run, never "no helper".
	lookup, err := runCloneGit(ctx, runner, clone, &probe,
		"config", "--get-urlmatch", "credential.helper", "https://github.com")
	helper := strings.TrimSpace(string(lookup.Stdout))
	switch {
	case err == nil && lookup.ExitCode == 1:
		return filter.warn(stdout, warnGitPush, fmt.Sprintf("doctor: git_push WARN no credential helper serves "+
			"github.com — git falls back to GIT_ASKPASS and may push %s as another account", repository), fix)
	case err != nil || lookup.ExitCode != 0 || helper == "":
		return filter.warn(stdout, warnGitPush, fmt.Sprintf("doctor: git_push UNVERIFIED could not read github.com's "+
			"credential.helper: %v", gitError(lookup, err)), "")
	}
	probe.Stdin = []byte("protocol=https\nhost=github.com\n\n")
	answer, err := cloneGit(ctx, runner, clone, &probe, "-c", "credential.interactive=false", "credential", "fill")
	gitUser := credentialUser(answer)
	if err != nil || gitUser == "" {
		return filter.warn(stdout, warnGitPush, fmt.Sprintf("doctor: git_push WARN credential helper %q gave no "+
			"github.com user without prompting (%v)", helper, err), fix)
	}
	ghUser, err := ghAnswer(ctx, runner, "api", "user", "--jq", ".login")
	if err != nil {
		return filter.warn(stdout, warnGitPush, fmt.Sprintf("doctor: git_push UNVERIFIED git user=%s; gh could not "+
			"name its own user: %v", gitUser, err), "")
	}
	push, err := ghAnswer(ctx, runner, "api", "repos/"+repository, "--jq", ".permissions.push")
	if err != nil {
		return filter.warn(stdout, warnGitPush, fmt.Sprintf("doctor: git_push UNVERIFIED git user=%s; gh could not "+
			"read %s's permissions: %v", gitUser, repository, err), "")
	}
	// gh's own helper answers its token's placeholder user: git then pushes
	// as gh. Any other user is the one some helper in the chain answered.
	if gitUser == "x-access-token" {
		gitUser = ghUser
	}
	switch {
	case gitUser != ghUser:
		return filter.warn(stdout, warnGitPush, fmt.Sprintf("doctor: git_push WARN git authenticates to github.com "+
			"as %s, gh as %s (push=%s on %s)", gitUser, ghUser, push, repository), fix)
	case push != printedTrue:
		return filter.warn(stdout, warnGitPush, fmt.Sprintf("doctor: git_push WARN %s has no push permission on %s "+
			"(push=%s)", gitUser, repository, push), "fix: gh auth login as an account with push, then "+gitPushFix)
	}
	fmt.Fprintf(stdout, "doctor: git_push ok %s user=%s push=true\n", repository, gitUser)
	return 0
}

// credentialUser is the username= line of a `git credential fill` answer;
// every other line, the password among them, is dropped unread.
func credentialUser(answer string) string {
	for line := range strings.SplitSeq(answer, "\n") {
		if user, ok := strings.CutPrefix(line, "username="); ok {
			return strings.TrimSpace(user)
		}
	}
	return ""
}

// ghAnswer runs one gh command, bounded, and returns its trimmed stdout.
func ghAnswer(ctx context.Context, runner deps.Runner, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cloneProbeTimeout)
	defer cancel()
	result, err := runner.Run(ctx, append([]string{"gh"}, args...), deps.RunOptions{Env: gitPushProbeEnv()})
	if err != nil || result.ExitCode != 0 {
		return "", gitError(result, err)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}
