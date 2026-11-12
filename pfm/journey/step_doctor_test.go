//go:build journey

package journey

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// doctorRows are the rows pfm doctor prints on this machine after the day's chats ended and were
// killed: the plugins it installed read OK, Herdr's ping reads OK at the path the owner's PATH finds
// ({herdr}: /usr/local/bin/herdr on the Linux machine, the pinned bin directory on a CI runner), the
// indicator painter is not stale and the crumb audit finds no invalid entry.
var doctorRows = []string{
	"doctor: claude_plugins ok",
	"doctor: dep herdr path={herdr} ok",
	"doctor: herdr-indicators: ok process enumeration confirms no stale painter",
	"doctor: pre-push gate=armed core.hooksPath=.githooks",
}

// doctorMachineFails are the failures the journey machine itself causes, the only ones doctor may
// print: with no network the professor marketplace's plugins (a release build's GitHub source) are
// recorded with no files, so the flights plugin holds no tracetree kit. {home} is the owner's home.
var doctorMachineFails = []string{
	`doctor: tracetree-kit: FAIL need=kit checked={home}/.cc/1/plugins/cache/flights@professor/skills/tracetree/tracetree.py ` +
		`error="stat {home}/.cc/1/plugins/cache/flights@professor/skills/tracetree/tracetree.py: no such file or directory" ` +
		`— fix: run pfm install --yes to refresh the flights plugin copy`,
	`doctor: tracetree-kit: FAIL need=run checked=python3 {home}/.cc/1/plugins/cache/flights@professor/skills/tracetree/tracetree.py ` +
		`error="not run: python3 or the kit failed above" — fix: fix the rows above, then rerun pfm doctor`,
}

// doctor: an abandoned empty pfm tmux server is listed by `pfm chat ls --all` and ended by `pfm chat ls
// --reap`; then the owner types pfm doctor in the clone; it prints no failure beyond the machine's own
// (doctorMachineFails), the rows of the day's plugins, Herdr and its painter read OK, the clone's
// pre-push gate reads armed, the Codex pane-binding audit finds nothing contested, the crumb audit
// finds every entry valid, the host check leaves only the owner's own file unclassified, and it exits 3
// for the machine's failures alone.
func doctor(c *harness.Ctx, s State) State {
	c.Expect(`"host":"platform.claude.com"`)
	term := c.Terminal("doctor")
	term.WaitFor(prompt)
	emptyTmuxReaped(c, term)
	out := filepath.Join(s.Home, "doctor.out")
	// The clone's local settings send Claude's API traffic to a gateway that is down, as an owner's may:
	// the harness-prompt capture must still reach its own sink.
	local := filepath.Join(s.Home, ".professor", ".claude", "settings.local.json")
	read(c, "sh", "-c", `test ! -e "$1" && printf '%s' '{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9"}}' >"$1"`,
		"sh", local)
	c.Cleanup(func() {
		if r := c.Read("rm", "-f", local); r.Code != 0 {
			c.Logf("teardown: rm %s exited %d: %s", local, r.Code, r.Stderr)
		}
	})
	before := read(c, "sh", "-c", claudeStartState)
	code := shellRun(term, "doctor", "clear && (cd ~/.professor && pfm doctor) > ~/doctor.out 2>&1", 120*time.Second)
	read(c, "rm", local)
	// Its Claude probes ran in a throwaway config dir: no start of theirs wrote the owner's.
	if after := read(c, "sh", "-c", claudeStartState); after != before {
		c.Failf("pfm doctor changed Claude's global config or backups:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	lines := strings.Split(read(c, "cat", out), "\n")
	var failed, unclassified, crumbs, bindings, harnessRows []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "doctor: harness-prompt: ") && !strings.HasPrefix(line, "doctor: harness-prompt:  "):
			harnessRows = append(harnessRows, line)
		case strings.HasPrefix(line, "doctor: codex_pane_bindings "), strings.Contains(line, "codex_pane_binding="):
			bindings = append(bindings, line)
		case strings.Contains(line, " FAIL"):
			failed = append(failed, line)
		case strings.HasPrefix(line, "host-check: WARN unclassified "):
			unclassified = append(unclassified, line)
		case strings.HasPrefix(line, "doctor: crumbs "):
			crumbs = append(crumbs, line)
		}
	}
	want := strings.ReplaceAll(strings.Join(doctorMachineFails, "\n"), "{home}", s.Home)
	if got := strings.Join(failed, "\n"); got != want {
		c.Failf("pfm doctor failure rows (%s):\n%s\nwant only the machine's own:\n%s", out, got, want)
	}
	// pfm logs in the clone the install recorded, never falling back to the home's state folder.
	if row := filepath.Join(logRoot(s.Home), "pfm.jsonl"); !slices.ContainsFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, "doctor: log path="+row+" bytes=")
	}) || slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, "doctor: log WARN ") }) {
		c.Failf("pfm doctor names no log row for %s, or a log WARN row (%s)", row, out)
	}
	// The fake Claude's harness prompt differs from both pinned baselines, and both captures, each from
	// a fresh config dir, hash alike: the remote flags that once switched the prompt run to run are off.
	c.Logf("harness-prompt rows: %q", harnessRows)
	if len(harnessRows) != 2 || !harnessDrift.MatchString(harnessRows[0]) ||
		!harnessDrift.MatchString(harnessRows[1]) ||
		harnessRows[0][:len(harnessDriftHead)] != harnessRows[1][:len(harnessDriftHead)] {
		c.Failf("pfm doctor harness-prompt rows %q, want two matching %s (%s)", harnessRows, harnessDrift, out)
	}
	herdr := strings.TrimSpace(read(c, "sh", "-c", "command -v herdr"))
	for _, row := range append(doctorRows, "doctor: failures=2") {
		row = strings.ReplaceAll(row, "{herdr}", herdr)
		if !slices.Contains(lines, row) {
			c.Failf("pfm doctor printed no row %q (%s)", row, out)
		}
	}
	keep := "host-check: WARN unclassified " + filepath.Join(s.Home, ".claude", "owner-keep.txt") +
		" — UNCLASSIFIED — on neither the shared nor the per-account list"
	if !slices.Equal(unclassified, []string{keep}) {
		c.Failf("pfm doctor unclassified rows %q, want only the owner's own file %q (%s)", unclassified, keep, out)
	}
	if len(crumbs) != 1 || !strings.HasSuffix(crumbs[0], " invalid=0") {
		c.Failf("pfm doctor crumb rows %q, want one ending invalid=0 (%s)", crumbs, out)
	}
	c.Logf("Codex pane-binding audit: %q", bindings)
	if len(bindings) != 1 || !codexBindingsClean.MatchString(bindings[0]) {
		c.Failf("pfm doctor Codex pane-binding rows %q, want one audit row with contested=0 and no warning (%s)",
			bindings, out)
	}
	if code != 3 {
		c.Failf("pfm doctor exited %d, want 3 (the machine's failures alone) (%s)", code, out)
	}
	return s
}

// claudeStartState lists every global config and backup a Claude start writes, in the home and in each
// account's config dir, with each file's checksum.
const claudeStartState = `for p in ~/.claude.json ~/.claude/backups ~/.cc/*/.claude.json ~/.cc/*/backups; do ` +
	`test -e "$p" && find "$p" -type f -exec cksum {} +; done | sort; true`

// harnessDriftHead is a harness-prompt verdict's head carrying the hash of the fake Claude's prompt
// (mockengine claudeHarnessInstructions) as doctor masks it.
const harnessDriftHead = "doctor: harness-prompt: DRIFT live=7c8faf54e65c79a9 "

// harnessDrift is the harness-prompt verdict on the fake Claude's prompt against a pinned baseline.
var harnessDrift = regexp.MustCompile(`^` + regexp.QuoteMeta(harnessDriftHead) +
	`baseline=[0-9a-f]{16} \(harness-(?:opus|original)-v[0-9.]+\.md\) — harness instructions changed; ` +
	`review before re-pinning$`)

// codexBindingsClean is the Codex pane-binding audit's row with nothing contested.
var codexBindingsClean = regexp.MustCompile(
	`^doctor: codex_pane_bindings total=\d+ live=\d+ stale=\d+ contested=0 retired=\d+ undecodable=0$`)

// emptyTmuxReaped: a tmux server is left on a pfm chat socket with no session (exit-empty off, its
// only session killed); `pfm chat ls --all` names it abandoned with its fix, `pfm chat ls --reap` ends
// it by SIGTERM, and no tmux process names the socket after.
func emptyTmuxReaped(c *harness.Ctx, term *harness.Terminal) {
	socket := "cc-" + c.Unique("empty")
	if r := c.Read("sh", "-c", `tmux -L "$1" new-session -d -s held 'sleep 600' && `+
		`tmux -L "$1" set-option -g exit-empty off && tmux -L "$1" kill-session -t held`, "sh", socket); r.Code != 0 {
		c.Failf("leaving an empty tmux server on %s exited %d: %s%s", socket, r.Code, r.Stdout, r.Stderr)
	}
	c.Cleanup(func() { c.Read("tmux", "-L", socket, "kill-server") })
	_, errs, code := typedRun(c, term, "ls-empty", "pfm chat ls --all")
	warning := socket + ": abandoned empty tmux server — it runs with no session and no pane yet holds this " +
		"pfm chat socket; reap it: pfm chat ls --reap"
	if code != 0 || !slices.Contains(strings.Split(errs, "\n"), "pfm: tmux probe warning: "+warning) {
		c.Failf("pfm chat ls --all exited %d, stderr %q; want exit 0 and the warning %q", code, errs, warning)
	}
	out, errs, code := typedRun(c, term, "reap", "pfm chat ls --reap")
	ended := regexp.MustCompile(`^` + regexp.QuoteMeta(socket) + ` \(pid \d+\): ended by SIGTERM\n$`)
	if code != 0 || errs != "" || !ended.MatchString(out) {
		c.Failf("pfm chat ls --reap exited %d, stdout %q, stderr %q; want exit 0 and only %q",
			code, out, errs, ended)
	}
	for pid, p := range processes(c) {
		if strings.Contains(p.Args, socket) {
			c.Failf("process %d still names the reaped socket %s: %s", pid, socket, p.Args)
		}
	}
}
