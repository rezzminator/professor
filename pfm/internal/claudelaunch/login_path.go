package claudelaunch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/loginshell"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	envPATH      = "PATH"
	envUserShell = "SHELL"
	// loginPATHStart and loginPATHEnd fence the PATH the login shell prints, so
	// whatever its startup files write around it is ignored.
	loginPATHStart = "__PFM_LOGIN_PATH_START__"
	loginPATHEnd   = "__PFM_LOGIN_PATH_END__"
	// loginPATHTimeout bounds the login shell; a slower one leaves the launch PATH unchanged.
	loginPATHTimeout = 3 * time.Second
	// loginPATHWaitDelay bounds the wait for output a process the startup files left behind still holds.
	loginPATHWaitDelay = 500 * time.Millisecond
	// loginBasePATH is the PATH login(1) starts a session with.
	loginBasePATH = "/usr/bin:/bin:/usr/sbin:/sbin"
)

// loginPATHScript prints the login shell's PATH between the markers.
const loginPATHScript = `printf '%s%s%s' '` + loginPATHStart + `' "$PATH" '` + loginPATHEnd + `'`

// loginPATHResult is a login shell's PATH entries, or why it has none.
type loginPATHResult struct {
	entries []string
	problem string
	// lookupNote is why the account's login shell was not used, when SHELL was probed instead.
	lookupNote string
}

// pathEnvThrough is the PATH assignment a Claude launch adds: the PATH lookup
// reads, its order kept, with each absolute entry of the user's login-shell
// PATH it lacks placed where the login PATH puts it (mergePATH). Claude Code
// runs every Bash call as a non-interactive bash that reads no startup file, so
// a chat launched from a bare PATH (systemd, Herdr, headless) would otherwise
// miss the tools the login shell adds (mise, nvm, asdf, brew). added is the
// login entries it adds, in login order; nothing added, or a login shell that
// gave no PATH, yields none. c times the probe; runner runs it, nil the real one.
func pathEnvThrough(
	lookup func(string) (string, bool), c clock.Clock, runner deps.Runner,
) (env, added []string) {
	fallback, _ := lookup(envUserShell)
	login := loginPATH(context.Background(), c, fallback, runner)
	if login.problem != "" {
		return nil, nil
	}
	launch, _ := lookup(envPATH)
	merged, added := mergePATH(launch, login.entries)
	if len(added) == 0 {
		return nil, nil
	}
	return []string{envPATH + "=" + merged}, added
}

// loginPATH is the user's login PATH within parent, timed by c. With no runner it
// is asked through loginPATHCache per fallback (the only input that varies in a
// process): a success once per process, a failure again once loginPATHRetry has
// passed on c. A test process asks no real login shell: its startup files would
// add this host's folders to every launch a test renders, so the answer adds
// nothing. A caller passing its runner bypasses the cache, neither reading nor
// writing it, so no answer a fake gave reaches another launch. Each probe warns
// its failure, or an account lookup that fell back to fallback (the launch's SHELL).
func loginPATH(parent context.Context, c clock.Clock, fallback string, runner deps.Runner) loginPATHResult {
	if runner != nil {
		return warnLoginPATH(probeLoginPATH(parent, c, fallback, runner))
	}
	if testing.Testing() {
		return loginPATHResult{}
	}
	return loginPATHCache.get(fallback, c, func() loginPATHResult {
		return warnLoginPATH(probeLoginPATH(parent, c, fallback, nil))
	})
}

// warnLoginPATH warns result's problem, else its lookup note, and returns it.
func warnLoginPATH(result loginPATHResult) loginPATHResult {
	switch {
	case result.problem != "":
		obs.Logger(context.Background()).Warn("claudelaunch: login PATH", obs.FieldErr, result.problem)
	case result.lookupNote != "":
		obs.Logger(context.Background()).Warn("claudelaunch: login PATH", obs.FieldErr, result.lookupNote)
	}
	return result
}

// probeLoginPATH runs the user's account login shell, else fallback, as a login interactive shell with no controlling
// terminal (an interactive shell in a background group of one stops on
// SIGTTIN) and stdin from /dev/null, and reads the PATH it prints within
// loginPATHTimeout on c.
func probeLoginPATH(parent context.Context, c clock.Clock, fallback string, runner deps.Runner) loginPATHResult {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	// The account's shell, never SHELL first: Claude Code sets SHELL to bash in
	// every Bash call and process it spawns, so a chat started from a chat would
	// otherwise probe bash, whatever the account logs in with. The lookup is
	// bounded by a kernel deadline on its child (context.WithTimeout, not c): it
	// runs where no login shell will (SHELL unset and the lookup failing), and a
	// unit test's fake clock owns the shell probe's wait alone.
	lookupCtx, cancelLookup := context.WithTimeout(parent, loginPATHTimeout)
	shell, lookupErr := loginshell.Lookup(lookupCtx, runner, os.Getuid())
	cancelLookup()
	lookupNote := ""
	if lookupErr == nil && shell == "" {
		lookupErr = errors.New("the account has no login shell")
	}
	if lookupErr != nil {
		shell = fallback
		lookupNote = fmt.Sprintf("login shell lookup: %v; using %s", lookupErr, envUserShell)
	}
	if shell == "" {
		return loginPATHResult{problem: fmt.Sprintf(
			"login shell lookup: %v, and %s is unset: the launch PATH is kept as it is", lookupErr, envUserShell,
		)}
	}
	ctx, cancel := clock.WithTimeout(parent, c, loginPATHTimeout)
	defer cancel()
	argv := []string{shell, "-l", "-i", "-c", loginPATHScript}
	// Run from HOME, as login(1) does: mise, direnv or asdf must not report the tools of pfm's own folder.
	env := loginEnvironment(os.Environ(), shell)
	options := deps.RunOptions{Setsid: true, WaitDelay: loginPATHWaitDelay, Env: env, Dir: loginHome(env)}
	result, err := runner.Run(ctx, argv, options)
	if ctx.Err() != nil {
		return loginPATHResult{lookupNote: lookupNote, problem: withNote(fmt.Sprintf(
			"%s -l -i printed no PATH within %s: the launch PATH is kept as it is", shell, loginPATHTimeout,
		), lookupNote)}
	}
	// A shell that exited 0 while a process its startup files left behind still
	// held stdout past loginPATHWaitDelay ends in exec.ErrWaitDelay: the PATH it
	// printed is used, and only a missing one is a problem.
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return loginPATHResult{lookupNote: lookupNote, problem: withNote(
			fmt.Sprintf("%s -l -i: %v: the launch PATH is kept as it is", shell, err), lookupNote)}
	}
	output := string(result.Stdout)
	value, found := "", false
	if start := strings.LastIndex(output, loginPATHStart); start >= 0 {
		value, _, found = strings.Cut(output[start+len(loginPATHStart):], loginPATHEnd)
	}
	if !found {
		return loginPATHResult{lookupNote: lookupNote, problem: withNote(fmt.Sprintf(
			"%s -l -i exited %d with no PATH between its markers (stdout %q): the launch PATH is kept as it is",
			shell, result.ExitCode, clipOutput(output),
		), lookupNote)}
	}
	return loginPATHResult{entries: strings.Split(value, string(os.PathListSeparator)), lookupNote: lookupNote}
}

// withNote is problem followed by the account-lookup note, when there is one.
func withNote(problem, note string) string {
	if note == "" {
		return problem
	}
	return problem + " (" + note + ")"
}

// clipOutput is output cut to the length a warning carries.
func clipOutput(output string) string {
	const limit = 200
	if len(output) <= limit {
		return output
	}
	return output[:limit] + "…"
}

// mergePATH is launch, its entries verbatim and in their order, with each
// absolute entry of login it lacks added once: placed just before the first
// launch entry that follows it in login, else appended, so a folder the login
// shell puts ahead of a launch folder stays ahead of it. A relative or empty
// login entry is dropped: it would search the chat's working directory. added
// is the entries added, in login's order.
func mergePATH(launch string, login []string) (merged string, added []string) {
	separator := string(os.PathListSeparator)
	launchEntries := []string{}
	if launch != "" {
		launchEntries = strings.Split(launch, separator)
	}
	inLaunch := map[string]bool{}
	for _, entry := range launchEntries {
		inLaunch[entry] = true
	}
	before := map[string][]string{}
	pending := []string{}
	for _, entry := range login {
		if inLaunch[entry] {
			before[entry] = append(before[entry], pending...)
			pending = nil
			continue
		}
		if !filepath.IsAbs(entry) || slices.Contains(added, entry) {
			continue
		}
		pending = append(pending, entry)
		added = append(added, entry)
	}
	parts := make([]string, 0, len(launchEntries)+len(added))
	for _, entry := range launchEntries {
		parts = append(parts, before[entry]...)
		delete(before, entry)
		parts = append(parts, entry)
	}
	return strings.Join(append(parts, pending...), separator), added
}
