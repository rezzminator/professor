package doctor

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestPrintGitPushDoctor(t *testing.T) {
	const https = "https://github.com/acme/professor.git"
	type answer struct {
		stdout string
		exit   int
		err    error
	}
	ok := func(stdout string) answer { return answer{stdout: stdout} }
	hint := testWarningHint(warnGitPush)
	for _, test := range []struct {
		name                       string
		origin                     string
		helper, fill, ghUser, push answer
		want                       string
		warnings                   int
	}{
		{
			"not on github", "https://example.invalid/acme/professor.git",
			answer{},
			answer{},
			answer{},
			answer{},
			"doctor: git_push does not apply: origin https://example.invalid/acme/professor.git is not on github.com\n", 0,
		},
		{
			"ssh origin", "git@github.com:acme/professor.git",
			answer{},
			answer{},
			answer{},
			answer{},
			"doctor: git_push origin git@github.com:acme/professor.git is ssh: git pushes with an ssh key, which " +
				"this row does not check\n", 0,
		},
		{
			"no helper serves github.com", https,
			answer{exit: 1},
			answer{},
			answer{},
			answer{},
			"doctor: git_push WARN no credential helper serves github.com — git falls back to GIT_ASKPASS and may " +
				"push acme/professor as another account — fix: gh auth setup-git --hostname github.com" + hint + "\n",
			1,
		},
		{
			"helper lookup could not run", https,
			answer{exit: 128},
			answer{},
			answer{},
			answer{},
			"doctor: git_push UNVERIFIED could not read github.com's credential.helper: exit 128: " + hint + "\n",
			1,
		},
		{
			"helper answers nothing without a prompt", https, ok("store\n"),
			answer{exit: 128, stdout: "", err: nil},
			answer{},
			answer{},
			"doctor: git_push WARN credential helper \"store\" gave no github.com user without prompting (exit 128: ) — " +
				"fix: gh auth setup-git --hostname github.com" + hint + "\n", 1,
		},
		{
			"git and gh are different accounts", https, ok("store"),
			ok("protocol=https\nhost=github.com\nusername=editor-bot\npassword=secret\n"), ok("owner\n"), ok("true\n"),
			"doctor: git_push WARN git authenticates to github.com as editor-bot, gh as owner (push=true on " +
				"acme/professor) — fix: gh auth setup-git --hostname github.com" + hint + "\n", 1,
		},
		{
			"gh account without push", https, ok("store"), ok("username=owner\npassword=secret\n"), ok("owner"),
			ok("false"),
			"doctor: git_push WARN owner has no push permission on acme/professor (push=false) — fix: gh auth login " +
				"as an account with push, then gh auth setup-git --hostname github.com" + hint + "\n", 1,
		},
		{
			"gh's own helper", https, ok("!/usr/bin/gh auth git-credential"),
			ok("username=x-access-token\npassword=secret\n"), ok("owner"), ok("true"),
			"doctor: git_push ok acme/professor user=owner push=true\n", 0,
		},
		{
			"gh's helper behind another helper that answers", https, ok("!/usr/bin/gh auth git-credential"),
			ok("username=editor-bot\npassword=secret\n"), ok("owner"), ok("true"),
			"doctor: git_push WARN git authenticates to github.com as editor-bot, gh as owner (push=true on " +
				"acme/professor) — fix: gh auth setup-git --hostname github.com" + hint + "\n", 1,
		},
		{
			"gh could not answer", https, ok("store"), ok("username=owner\n"),
			answer{exit: -1, err: errors.New("gh: not found")},
			answer{},
			"doctor: git_push UNVERIFIED git user=owner; gh could not name its own user: gh: not found" + hint + "\n", 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			clone := t.TempDir()
			runner := &deps.FakeRunner{}
			script := func(prefix []string, value answer) {
				runner.Script(prefix, deps.RunResult{Stdout: []byte(value.stdout), ExitCode: value.exit}, value.err)
			}
			script([]string{"git", "-C", clone, "remote", "get-url", "origin"}, ok(test.origin+"\n"))
			script([]string{"git", "-C", clone, "config", "--get-urlmatch"}, test.helper)
			script([]string{"git", "-C", clone, "-c", "credential.interactive=false", "credential", "fill"}, test.fill)
			script([]string{"gh", "api", "user"}, test.ghUser)
			script([]string{"gh", "api", "repos/acme/professor"}, test.push)
			var out bytes.Buffer
			filter := warningFilter{ignored: map[warningID]bool{}, configPath: config.FileName}
			warnings := printGitPushDoctor(context.Background(), &out, clone, runner, filter)
			if out.String() != test.want || warnings != test.warnings {
				t.Fatalf("got %q warnings=%d\nwant %q warnings=%d", out.String(), warnings, test.want, test.warnings)
			}
			for _, call := range runner.Calls() {
				if !slices.Contains(call.Argv, "credential") && call.Argv[0] != "gh" {
					continue
				}
				for _, pinned := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GCM_INTERACTIVE=never"} {
					if !slices.Contains(call.Opts.Env, pinned) {
						t.Fatalf("%s ran without %s: %v", strings.Join(call.Argv, " "), pinned, call.Opts.Env)
					}
				}
			}
		})
	}
}
