//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// claudeTyped: the owner leaves the picker for the pane's shell, `cd`s into the demo project and types `claude`;
// the fake Claude draws in the same pane, pfm owns its seat (one live Claude row on the branch's
// socket, its seat live on this pane), Herdr names the workspace for the project, and Herdr's own
// status for the pane goes working through a turn, then idle.
func claudeTyped(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Press(harness.Esc)
	term.WaitFor(prompt, 20*time.Second)
	s.Project = filepath.Join(s.Home, "work", "demo")
	term.Type("cd " + s.Project + " && claude\r")
	term.WaitFor("❯", 20*time.Second)
	s.Chat = oneLiveChat(c, s, "live-claude")
	s.Chat.Engine = engineOf(c, s)
	if got := workspaceLabel(c, s); got != "demo" {
		c.Failf("Herdr workspace label of the typed chat = %q, want %q (the project, not home)", got, "demo")
	}
	// Herdr reads `unknown` for a fresh agent until its startup grace ends: a turn typed sooner is never seen.
	c.Poll("Herdr status idle for "+s.Pane, 20*time.Second, func() (bool, string) {
		got := herdrStatus(c, s, s.Pane)
		return got == "idle", "status " + got
	})
	term.Type(`a turn mock-engine: {"type":"turn","reply":"native-reply","busy_ms":2000}` + "\r")
	var trail []string
	c.Poll("the reply and Herdr status idle again", 20*time.Second, func() (bool, string) {
		got := herdrStatus(c, s, s.Pane)
		if len(trail) == 0 || trail[len(trail)-1] != got {
			trail = append(trail, got)
		}
		return strings.Contains(term.Screen(), "⏺ native-reply") && got == "idle", fmt.Sprintf("trail %v", trail)
	})
	if want := []string{"idle", "working", "idle"}; strings.Join(trail, " ") != strings.Join(want, " ") {
		c.Failf("Herdr status of the native pane through a turn = %v, want %v", trail, want)
	}
	// escape herdr/failed-turn-paints-idle: a turn the model server refuses leaves the chat in state error,
	// and the native pane's tab shows the error mark, not idle.
	launch := tabName(c, s)
	term.Type(`a refused turn mock-engine: {"type":"api_error","reply":"API Error: 500 refused"}` + "\r")
	term.WaitFor("⎿ API Error: 500 refused", 20*time.Second)
	c.Poll("pfm reads the refused chat error, its tab marked "+strings.Join(tabMarks["error"], "/"), stepBound,
		func() (bool, string) {
			state, label := chatAttention(c, s), tabLabel(c, s)
			return state == "error" &&
					labelFrame(label, "error", launch) != "", fmt.Sprintf(
					"state %q, tab %q",
					state,
					label,
				)
		})
	// escape herdr/ai-title-before-paint: the automatic title Claude writes after a turn names the chat in
	// pfm's row and on its tab, and the turn after the refused one settles it idle again.
	title := c.Unique("ai-title")
	term.Type(
		`a titled turn mock-engine: [{"type":"ai_title","name":"` + title + `"},{"type":"turn","reply":"titled"}]` +
			"\r",
	)
	term.WaitFor("⏺ titled", 20*time.Second)
	c.Poll("row and tab named by the AI title "+title, stepBound, func() (bool, string) {
		got := [2]string{lsName(c, s), tabLabel(c, s)}
		return got == [2]string{title, markedLabel("idle", title)}, fmt.Sprintf("row, tab = %q", got)
	})
	fleetConsistent(c, s)
	return s
}

// rename: the owner names the chat from a second terminal (`pfm chat name {session} {name}`), then
// inside the engine (`/rename {name}`, named before any turn, then a turn), then clears the conversation (`/clear`) and
// names the successor by the chat's name. After each door pfm's row name, Herdr's tab label and the
// seat's name are the new name; the cleared chat keeps its seat, pane and engine under a new session.
func rename(c *harness.Ctx, s State) State {
	cli := c.Terminal(s.Term + "-cli")
	cli.WaitFor(prompt)
	term := c.Terminal(s.Term)
	s.Name = c.Unique("native")
	nameFromCLI(c, cli, s, s.Chat.Session)
	namesAgree(c, s, "pfm chat name")

	s.Name = c.Unique("typed")
	term.Type("/rename " + s.Name + "\r")
	// Claude fires no hook on /rename: its statusline's next paced render names the seat and the tab,
	// before any turn; the turn's hooks after it keep the name.
	namesAgree(c, s, "/rename")
	term.Type(`after the rename mock-engine: {"type":"turn","reply":"renamed-reply"}` + "\r")
	term.WaitFor("⏺ renamed-reply", 20*time.Second)
	namesAgree(c, s, "/rename and a turn")
	fleetConsistent(c, s)

	// /clear keeps the engine process, whose argv still names the old session: the fleet is judged
	// again once reload-cli has replaced it.
	before := s.Chat
	term.Type("/clear\r")
	c.Poll("a new session on seat "+before.Seat, 20*time.Second, func() (bool, string) {
		seats := liveSeats(c, herdrSocket(s))
		for session, seat := range seats {
			if session != before.Session && seat == [2]string{before.Seat, s.Pane} && len(seats) == 1 {
				s.Chat.Session = session
				return true, ""
			}
		}
		return false, fmt.Sprintf("live seats %v", seats)
	})
	// The successor carries its own name, the cleared one's numbered; the cleared conversation, named or
	// not, is listed nowhere.
	s.Name += " #2"
	namesAgree(c, s, "/clear")
	if rows := listRows(c, before.Session); len(rows) != 0 {
		c.Failf("the cleared named conversation %s is still listed:\n%s", before.Session, strings.Join(rows, "\n"))
	}
	s.Name = c.Unique("cleared")
	nameFromCLI(c, cli, s, s.Chat.Session)
	namesAgree(c, s, "/clear and pfm chat name")
	if p, ok := processes(c)[before.Engine]; !ok || p.Engine != "claude" {
		c.Failf("the engine %d is gone after /clear, want it kept", before.Engine)
	}
	return s
}

// nameFromCLI types `pfm chat name {target} {s.Name}` in cli and requires its confirm line alone: a plain
// terminal is no chat, and a name is a harness command pfm never signs, so there is no sender to report.
func nameFromCLI(c *harness.Ctx, cli *harness.Terminal, s State, target string) {
	out, errs, code := typedRun(c, cli, "name-"+s.Name, "pfm chat name "+target+" "+s.Name)
	if want := "named " + s.Chat.Session + " -> " + s.Name + "\n"; code != 0 || out != want || errs != "" {
		c.Failf("pfm chat name %s %s: exit %d, stdout %q, stderr %q; want exit 0, stdout %q, no stderr",
			target, s.Name, code, out, errs, want)
	}
}

// namesAgree waits for pfm's row name and the live seat's name to read s.Name, and the Herdr tab label
// to read s.Name under the idle mark: every door to it leaves the chat at rest.
func namesAgree(c *harness.Ctx, s State, after string) {
	c.Poll("row, tab and seat named "+s.Name+" after "+after, stepBound, func() (bool, string) {
		seat := strings.TrimSpace(
			stateSQL(c, "select coalesce(name, '') from seat where state = 'live'"+
				" and session_id = '"+s.Chat.Session+"'"),
		)
		got := [3]string{lsName(c, s), tabLabel(c, s), seat}
		return got == [3]string{s.Name, markedLabel("idle", s.Name), s.Name}, fmt.Sprintf("row, tab, seat = %q", got)
	})
}

// reloadCLI: with a draft left in the chat's composer, the owner aims `pfm chat reload` at the chat's Herdr
// socket and pane from a second terminal, with a follow-up prompt (--then); the chat comes back in the same
// pane with the same seat, session and name under a new engine, the follow-up submitted once and answered,
// and the draft, parked before the reload's /exit, never reaches the chat as a prompt. Before the reload
// Claude's updater has installed a newer build and version retention has pruned the one the seat
// recorded: the chat comes back on the newer build (escape herdr/pruned-claude-build). Every lane under
// this HOME launches claude from the same versions directory, so no build a launch may resolve is ever
// removed: the newer build lands first, the recorded one goes only once no launch that resolved it
// before is still on its way to exec it, and the newer one stays, as after a real update; the recorded
// build comes back for the paths later lanes recorded, and retention's keep-two drops the oldest, which
// no launch resolves while a newer build is on disk. prepare.sh resets the directory for the next run.
func reloadCLI(c *harness.Ctx, s State) State {
	cli := c.Terminal(s.Term + "-cli")
	term := c.Terminal(s.Term)
	before := s.Chat.Engine
	builds := filepath.Join(s.Home, ".local", "share", "claude", "versions")
	// prepare.sh links 2.1.269 and 2.1.270 to the fake engine; this step alone stages 2.1.271 from it.
	engine := filepath.Join(s.Home, ".local", "share", "journey", "bin", "mock-engine")
	oldest, recorded, newer := filepath.Join(builds, "2.1.269"), filepath.Join(builds, "2.1.270"),
		filepath.Join(builds, "2.1.271")
	if got := engineBuild(c, before); got != recorded {
		c.Failf("engine %d of %s runs %q, want the newest Claude build %q", before, s.Chat.Session, got, recorded)
	}
	// restore links the recorded build back only when it is gone (ln -f unlinks first, and the recorded
	// build is the newest until the newer one lands), then drops the oldest, never the newest.
	restore := func() []string {
		var failed []string
		for _, argv := range [][]string{
			{"sh", "-c", `[ -e "$2" ] || ln "$1" "$2"`, "sh", engine, recorded},
			{"rm", "-f", oldest},
		} {
			if r := c.Read(argv...); r.Code != 0 {
				failed = append(failed, fmt.Sprintf("%s exited %d: %s", strings.Join(argv, " "), r.Code, r.Stderr))
			}
		}
		return failed
	}
	c.Cleanup(func() {
		for _, failure := range restore() {
			c.Logf("teardown: %s", failure)
		}
	})
	read(c, "ln", engine, newer)
	c.Poll("no Claude launch still on its way to exec "+recorded, 20*time.Second, func() (bool, string) {
		var launching []string
		for pid, p := range processes(c) {
			if strings.Contains(p.Args, "internal claude-launch") ||
				strings.Contains(p.Args, recorded) && p.Args != recorded && !strings.HasPrefix(p.Args, recorded+" ") {
				launching = append(launching, fmt.Sprintf("%d %s", pid, p.Args))
			}
		}
		return len(launching) == 0, strings.Join(launching, "\n")
	})
	read(c, "rm", recorded)
	draft := c.Unique("draft")
	term.Type(draft)
	term.WaitFor("❯ " + draft)
	reply := c.Unique("then-pong")
	then := "the reborn chat answers this follow-up at once " + directive(turnStep(reply))
	command := "pfm chat reload --sock " + herdrSocket(s) + " --pane " + s.Pane + " --then '" + then + "'"
	if code := shellRun(cli, "reload", command, 30*time.Second); code != 0 {
		c.Failf("%s exited %d, want 0", command, code)
	}
	c.Poll("a new engine for session "+s.Chat.Session, 20*time.Second, func() (bool, string) {
		for pid, session := range engines(processes(c)) {
			if session == s.Chat.Session && pid != before {
				return true, ""
			}
		}
		return false, fmt.Sprintf("engines %v", engines(processes(c)))
	})
	reloadRunDone(c, s)
	term.WaitFor("⏺ "+reply, 20*time.Second)
	s.Chat.Engine = engineOf(c, s)
	if got := engineBuild(c, s.Chat.Engine); got != newer {
		c.Failf("engine %d of %s after the reload runs %q, want the newer build the updater installed %q",
			s.Chat.Engine, s.Chat.Session, got, newer)
	}
	if failed := restore(); len(failed) != 0 {
		c.Failf("restoring the Claude builds in %s: %s", builds, strings.Join(failed, "; "))
	}
	path := transcriptOf(c, s)
	var got []string
	for _, text := range userTexts(c, path) {
		if text == then || strings.Contains(text, draft) {
			got = append(got, text)
		}
	}
	if !slices.Equal(got, []string{then}) {
		c.Failf("prompts in %s holding the --then text or the draft = %q, want the --then text once: %q",
			path, got, then)
	}
	seats := liveSeats(c, herdrSocket(s))
	if got, want := seats[s.Chat.Session], [2]string{s.Chat.Seat, s.Pane}; len(seats) != 1 || got != want {
		c.Failf("live seats after the reload = %v, want only %s on seat %v", seats, s.Chat.Session, want)
	}
	namesAgree(c, s, "pfm chat reload")
	fleetConsistent(c, s)
	return s
}

// paneLost: the owner closes the chat's pane with Herdr's close key (prefix, x) and confirms; Herdr
// closes the workspace and opens a fresh one on the picker. The engine dies with the pane; pfm still
// answers: `pfm chat ls --all` exits 0 and lists the chat dead on a lost Herdr seat, `pfm ls --tsv`
// lists it resumable in its project, the seat reads lost, the picker in the new pane draws its row, and
// that pane's tab is Herdr's fresh one, no mark of the chat on it.
func paneLost(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Press(harness.Ctrl('b'))
	term.Type("x")
	term.WaitFor("↵ confirm")
	term.Press(harness.Enter)
	c.Poll("pane "+s.Pane+" gone and its engine with it", stepBound, func() (bool, string) {
		_, pane := herdrPanes(c, s)[s.Pane]
		_, engine := processes(c)[s.Chat.Engine]
		return !pane && !engine, fmt.Sprintf("pane listed %t, engine %d alive %t", pane, s.Chat.Engine, engine)
	})
	sock := herdrSocket(s)
	c.Poll("the chat listed lost", stepBound, func() (bool, string) {
		r := c.Read("pfm", "chat", "ls", "--all")
		if r.Code != 0 {
			c.Failf("pfm chat ls --all exited %d with the pane gone: %s%s", r.Code, r.Stderr, r.Stdout)
		}
		row := chatLsRow(r.Stdout, s.Name)
		want := []string{s.Name, s.Name, "dead", "HERDR", "LOST", sock + ":"}
		return len(row) >= len(want) &&
				slices.Equal(row[:len(want)], want), fmt.Sprintf(
				"row %q, want it to open %q",
				row,
				want,
			)
	})
	row := tsvRow(c, s.Chat.Session)
	if want := []string{
		"resume-claude",
		s.Chat.Session,
		"demo",
		s.Project,
		s.Name,
	}; len(row) < 11 || !slices.Equal(row[:5], want) ||
		row[10] != sock {
		c.Failf("pfm ls --tsv row of the lost chat = %q, want it to open %q and end on %s", row, want, sock)
	}
	if seats := seatsWhere(
		c,
		"state = 'lost'"+onSocket(sock),
	); seats[s.Chat.Session] != [2]string{
		s.Chat.Seat,
		s.Pane,
	} {
		c.Failf("lost seats on %s = %v, want %s on %v", sock, seats, s.Chat.Session, [2]string{s.Chat.Seat, s.Pane})
	}
	term.WaitFor("↻ "+s.Name, 20*time.Second)
	chatTabGone(c, s)
	return s
}

// chatLsRow is the fields of `pfm chat ls`'s row for the chat named name.
func chatLsRow(out, name string) []string {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == name {
			return f
		}
	}
	return nil
}

// tsvRow is `pfm ls --tsv`'s row for session.
func tsvRow(c *harness.Ctx, session string) []string {
	for _, line := range strings.Split(read(c, "pfm", "ls", "--tsv"), "\n") {
		if f := strings.Split(line, "\t"); len(f) > 1 && f[1] == session {
			return f
		}
	}
	return nil
}

// killedFooter is the line `pfm chat ls --all` ends on once a chat is killed. Its count spans every
// branch of the tree, and a sibling branch kills chats of its own, so a step reads only that it is there.
var killedFooter = regexp.MustCompile(`\n {2}\(\+[1-9]\d* killed — pfm ls --killed to manage\)\n`)

// killFromPicker: in the picker the new pane opened, the owner types the lost chat's name to find its
// row among every branch's chats, selects it and presses Ctrl-X; the row leaves the picker with no tmux
// error naming its Herdr socket on the screen, the chat is killed in state (`pfm chat ls --all` counts
// it killed, `pfm ls --tsv` lists it no more), its seat is left lost and the pane's tab stays Herdr's
// fresh one.
func killFromPicker(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.WaitFor("↻ " + s.Name)
	term.Type(s.Name)
	term.WaitScreen("the picker narrowed to "+s.Name, stepBound, func(screen string) bool {
		return strings.Contains(screen, "find › "+s.Name)
	})
	if !strings.HasPrefix(selectedRow(term.Screen()), "↻ "+s.Name) {
		term.Press(harness.Down)
	}
	term.WaitScreen("the lost chat "+s.Name+" selected", stepBound, func(screen string) bool {
		return strings.HasPrefix(selectedRow(screen), "↻ "+s.Name)
	})
	term.Press(harness.Ctrl('x'))
	term.WaitGone("↻ "+s.Name, stepBound)
	// The picker lists every branch's chats, and a tmux-hosted one's dossier names tmux: the broken state
	// is the lost chat's own Herdr socket read as a tmux socket, so its path on the screen.
	if screen := term.Screen(); strings.Contains(unwrapped(screen), herdrSocket(s)) {
		c.Failf("Ctrl-X on the lost chat put its Herdr socket %s on the screen:\n%s", herdrSocket(s), screen)
	}
	if got := strings.TrimSpace(
		stateSQL(c, "select count(*) from hidden where uuid = '"+s.Chat.Session+"'"),
	); got != "1" {
		c.Failf("killed rows for %s = %s, want 1", s.Chat.Session, got)
	}
	out := read(c, "pfm", "chat", "ls", "--all")
	if !killedFooter.MatchString(out) || chatLsRow(out, s.Name) != nil {
		c.Failf("pfm chat ls --all after the kill lists %s or counts no killed chat:\n%s", s.Name, out)
	}
	if row := tsvRow(c, s.Chat.Session); row != nil {
		c.Failf("pfm ls --tsv still lists the killed chat: %q", row)
	}
	if seats := seatsWhere(
		c,
		"state = 'lost'"+onSocket(herdrSocket(s)),
	); seats[s.Chat.Session] != [2]string{
		s.Chat.Seat,
		s.Pane,
	} {
		c.Failf(
			"lost seats after the kill = %v, want %s left on %v",
			seats,
			s.Chat.Session,
			[2]string{s.Chat.Seat, s.Pane},
		)
	}
	chatTabGone(c, s)
	return s
}

// unkill: from the second terminal the owner types `pfm chat unkill {name}`; it confirms with the
// chat's session, and a second unkill says the chat is not killed and exits 1. The chat is no longer
// killed, `pfm ls --tsv` lists it resumable again under its name, and the picker, reopened in the pane,
// draws its row.
func unkill(c *harness.Ctx, s State) State {
	cli := c.Terminal(s.Term + "-cli")
	c.Expect(`"err":"chat unkill exited 1"`) // the second unkill, of a chat no longer killed
	for _, run := range []struct {
		label, command, stdout, stderr string
		code                           int
	}{
		{"unkill-name", "pfm chat unkill " + s.Name, "unkilled " + s.Chat.Session + "\n", "", 0},
		{
			"unkill-again", "pfm chat unkill " + s.Chat.Session, "",
			"pfm chat unkill: " + s.Chat.Session + " is not killed; nothing was unkilled\n", 1,
		},
	} {
		out, errs, code := typedRunWithin(c, cli, run.label, run.command, 20*time.Second)
		if code != run.code || out != run.stdout || errs != run.stderr {
			c.Failf("%s: exit %d, stdout %q, stderr %q; want exit %d, stdout %q, stderr %q",
				run.command, code, out, errs, run.code, run.stdout, run.stderr)
		}
	}
	if got := strings.TrimSpace(
		stateSQL(c, "select count(*) from hidden where uuid = '"+s.Chat.Session+"'"),
	); got != "0" {
		c.Failf("killed rows for %s after the unkill = %s, want 0", s.Chat.Session, got)
	}
	if want, row := []string{
		"resume-claude",
		s.Chat.Session,
		"demo",
		s.Project,
		s.Name,
	}, tsvRow(
		c,
		s.Chat.Session,
	); len(row) < 5 ||
		!slices.Equal(row[:5], want) {
		c.Failf("pfm ls --tsv row after the unkill = %q, want it to open %q", row, want)
	}
	// The open picker keeps its own Ctrl-X over every refresh (model.killChanges): the owner reopens it.
	term := c.Terminal(s.Term)
	term.Press(harness.Esc)
	term.WaitFor(prompt, stepBound)
	term.Type("pfm\r")
	term.WaitFor("↻ "+s.Name, 20*time.Second)
	return s
}

// herdrStatus is Herdr's own agent status for pane in the branch's session.
func herdrStatus(c *harness.Ctx, s State, pane string) string {
	var list struct {
		Result struct {
			Panes []struct {
				PaneID      string `json:"pane_id"`
				AgentStatus string `json:"agent_status"`
			} `json:"panes"`
		} `json:"result"`
	}
	out := read(c, herdrCLI(s, "pane", "list")...)
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		c.Failf("herdr pane list: %v:\n%s", err, out)
	}
	for _, p := range list.Result.Panes {
		if p.PaneID == pane {
			return p.AgentStatus
		}
	}
	return "(no pane " + pane + ")"
}

// workspaceLabel is the label of the branch's one Herdr workspace.
func workspaceLabel(c *harness.Ctx, s State) string {
	var list struct {
		Result struct {
			Workspaces []struct {
				Label string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	out := read(c, herdrCLI(s, "workspace", "list")...)
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Result.Workspaces) != 1 {
		c.Failf("herdr workspace list: want one workspace (%v):\n%s", err, out)
	}
	return list.Result.Workspaces[0].Label
}

// tabLabel is the label of the branch's one Herdr tab.
func tabLabel(c *harness.Ctx, s State) string {
	var list struct {
		Result struct {
			Tabs []struct {
				Label string `json:"label"`
			} `json:"tabs"`
		} `json:"result"`
	}
	out := read(c, herdrCLI(s, "tab", "list")...)
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Result.Tabs) != 1 {
		c.Failf("herdr tab list: want one tab (%v):\n%s", err, out)
	}
	return list.Result.Tabs[0].Label
}

// freshTabLabel is the label Herdr gives the first tab of a workspace it opens: its number.
const freshTabLabel = "1"

// chatTabGone requires the branch's one Herdr tab to be the fresh tab Herdr opened on pfm's picker once
// the chat's pane went (after its end, its lost pane, its kill): no mark of the chat, nor its name, is
// left on it.
func chatTabGone(c *harness.Ctx, s State) {
	if got := tabLabel(c, s); got != freshTabLabel {
		c.Failf("the Herdr tab after chat %s left its pane is labelled %q, want Herdr's fresh tab %q",
			s.Chat.Session, got, freshTabLabel)
	}
}

// lsName is `pfm ls --tsv`'s name of the branch's chat.
func lsName(c *harness.Ctx, s State) string {
	for _, line := range strings.Split(read(c, "pfm", "ls", "--tsv"), "\n") {
		if f := strings.Split(line, "\t"); len(f) > 4 && f[1] == s.Chat.Session {
			return f[4]
		}
	}
	return "(no row " + s.Chat.Session + ")"
}

// userTexts is the text of every prompt the owner's side put in the transcript at path, meta records left out.
func userTexts(c *harness.Ctx, path string) []string {
	var texts []string
	for _, line := range strings.Split(read(c, "cat", path), "\n") {
		var record struct {
			Type    string `json:"type"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &record) != nil || record.Type != "user" || record.IsMeta {
			continue
		}
		var text string
		if json.Unmarshal(record.Message.Content, &text) == nil {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		c.Failf("the transcript %s holds no prompt", path)
	}
	return texts
}

// engineBuild is the executable engine pid runs, as pfm's process reader names it.
func engineBuild(c *harness.Ctx, pid int) string {
	executable, ok := procTable(c).(gather.ProcExecutable)
	if !ok {
		c.Failf("the process table reports no executables")
	}
	path, err := executable.Executable(pid)
	if err != nil {
		c.Failf("read engine %d's executable: %v", pid, err)
	}
	return path
}
