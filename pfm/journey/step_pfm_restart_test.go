//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// pfmRestart: with a second workspace split in two beside the chain's two chats and one of pfm's
// services stopped by hand, the owner previews `pfm restart --dry-run` on the chain's Herdr session,
// which plans to leave both chats to the Herdr server stop and bring them back, to keep that service
// stopped, and changes nothing; then runs `pfm restart`: Herdr comes back with every workspace, tab and
// split as before, both chats live again on their own panes and session ids, every step verified, the
// stopped service still stopped and the others as the install left them.
func pfmRestart(c *harness.Ctx, s State) State {
	term := c.Terminal("restart-ctl")
	created := read(c, herdrCLI(s, "workspace", "create", "--label", c.Unique("split"), "--cwd", s.Project,
		"--no-focus")...)
	var workspace struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(created), &workspace); err != nil || workspace.Result.RootPane.PaneID == "" {
		c.Failf("herdr workspace create printed no root pane (%v):\n%s", err, created)
	}
	read(c, herdrCLI(s, "pane", "split", workspace.Result.RootPane.PaneID, "--direction", "right",
		"--cwd", s.Project)...)
	before, workspaces, split := herdrLayout(c, s)
	if workspaces < 2 || !split {
		c.Failf("Herdr session %s before the restart holds %d workspace(s), split %v, want 2 and a split:\n%s",
			s.Session, workspaces, split, before)
	}
	c.Logf("Herdr layout before pfm restart:\n%s", before)
	pauseService(c)
	c.Cleanup(func() { resumeService(c) })
	session := "HERDR_SESSION=" + s.Session
	plan := c.Read("env", session, "pfm", "restart", "--dry-run")
	c.Logf("pfm restart --dry-run:\n%s%s", plan.Stdout, plan.Stderr)
	if plan.Code != 0 {
		c.Failf("pfm restart --dry-run exited %d:\n%s%s", plan.Code, plan.Stdout, plan.Stderr)
	}
	for _, chat := range s.Chats {
		unit := `chats +claude ` + regexp.QuoteMeta(chat.Name) + ` session ` + regexp.QuoteMeta(chat.Session)
		stop := regexp.MustCompile(`(?m)^stop +\d+ +` + unit + `\n.*\n +stop: +left running: the Herdr server ` +
			`stop hangs up its pane, which Herdr keeps in its saved session with the resume pfm registered$`)
		start := regexp.MustCompile(`(?m)^start +\d+ +` + unit + `  restart \(was up: live in Herdr pane ` +
			regexp.QuoteMeta(chat.Pane) + `\)$`)
		if !stop.MatchString(plan.Stdout) || !start.MatchString(plan.Stdout) {
			c.Failf("pfm restart --dry-run plans no stop left to Herdr and start back of %s (%s):\n%s",
				chat.Name, chat.Session, plan.Stdout)
		}
	}
	running := engines(processes(c))
	for _, chat := range s.Chats {
		if running[chat.Engine] != chat.Session {
			c.Failf("after the dry run engine %d of %s is gone: engines %v", chat.Engine, chat.Name, running)
		}
	}
	keep := fmt.Sprintf("%-14s %s  stays stopped (was not running before: %s)", "services", pausedService, pausedBefore)
	if !slices.Contains(strings.Split(plan.Stdout, "\n"), "keep      "+keep) {
		c.Failf("pfm restart --dry-run plans no %q for the unit stopped before it:\n%s", "keep      "+keep, plan.Stdout)
	}
	if got := services(c); got != pausedServices {
		c.Failf("pfm's services after the dry run:\n%s\nwant:\n%s", got, pausedServices)
	}
	if code := shellRun(term, "restart-run", "clear && "+session+" pfm restart"); code != 0 {
		c.Failf("pfm restart exited %d:\n%s", code, term.Screen())
	}
	log := restartLog(c)
	want := []string{
		"restart: ended 0 chat(s)", fmt.Sprintf("restart: brought back %d chat(s)", len(s.Chats)),
		"restart: every step verified", "keep   " + keep,
	}
	for _, chat := range s.Chats {
		want = append(want, fmt.Sprintf("  back   %-8s %-24s %s", "claude", chat.Name, chat.Session))
	}
	for _, line := range want {
		if !slices.Contains(log, line) {
			c.Failf("pfm restart logged no %q:\n%s", line, strings.Join(log, "\n"))
		}
	}
	if got := services(c); got != pausedServices {
		c.Failf("pfm's services after the restart, which leaves stopped what was stopped:\n%s\nwant:\n%s",
			got, pausedServices)
	}
	resumeService(c)
	if got := services(c); got != installedServices {
		c.Failf("pfm's services after the owner starts %s again:\n%s\nwant:\n%s", pausedService, got, installedServices)
	}
	sock := herdrSocket(s)
	c.Poll("every chat live again on its own pane and session, its engine running", func() (bool, string) {
		rows, _ := liveChats(c, sock)
		seats := liveSeats(c, sock)
		running := engines(processes(c))
		for _, chat := range s.Chats {
			engine := false
			for _, session := range running {
				engine = engine || session == chat.Session
			}
			if seats[chat.Session][1] != chat.Pane || rows[chat.Session][0] != "live-claude" || !engine {
				return false, fmt.Sprintf("%s not yet: chats %v, seats %v, engines %v", chat.Name, rows, seats, running)
			}
		}
		return true, ""
	})
	var after string
	c.Poll("Herdr's workspaces, tabs and splits as before the restart", func() (bool, string) {
		after, _, _ = herdrLayout(c, s)
		return after == before, "now:\n" + after + "\nbefore:\n" + before
	})
	c.Logf("Herdr layout after pfm restart:\n%s", after)
	c.Logf("live seats after pfm restart (session id: seat id, pane): %v", liveSeats(c, sock))
	for i, chat := range s.Chats {
		for pid, session := range engines(processes(c)) {
			if session == chat.Session {
				s.Chats[i].Engine = pid
			}
		}
		s.Chats[i].Seat = liveSeats(c, sock)[chat.Session][0]
	}
	return s
}

// pfmRestartNoRestore: `pfm restart --no-restore` is the restart that brings no chat back: its dry run
// plans to end both chats, and the run ends them, verifies every step and leaves each chat resumable,
// no engine of it running, while the chain's Herdr server runs again.
func pfmRestartNoRestore(c *harness.Ctx, s State) State {
	term := c.Terminal("restart-ctl")
	session := "HERDR_SESSION=" + s.Session
	plan := c.Read("env", session, "pfm", "restart", "--dry-run", "--no-restore")
	c.Logf("pfm restart --dry-run --no-restore:\n%s%s", plan.Stdout, plan.Stderr)
	lines := strings.Split(plan.Stdout, "\n")
	header := "restart plan: stop downstream first, then start upstream first only what was running before; " +
		"chats are not brought back"
	if plan.Code != 0 || lines[0] != header {
		c.Failf("pfm restart --dry-run --no-restore exited %d, header %q, want 0, %q:\n%s%s",
			plan.Code, lines[0], header, plan.Stdout, plan.Stderr)
	}
	for _, chat := range s.Chats {
		stop := regexp.MustCompile(`(?m)^stop +\d+ +chats +claude ` + regexp.QuoteMeta(chat.Name) + ` session ` +
			regexp.QuoteMeta(chat.Session) + `\n.*\n +stop: +Herdr pane.close of the proved seat; verified: ` +
			`the seat is retired; not brought back$`)
		if !stop.MatchString(plan.Stdout) {
			c.Failf(
				"pfm restart --dry-run --no-restore plans no end of %s (%s):\n%s",
				chat.Name,
				chat.Session,
				plan.Stdout,
			)
		}
	}
	if regexp.MustCompile(`(?m)^start +\d+ +chats `).MatchString(plan.Stdout) {
		c.Failf("pfm restart --dry-run --no-restore plans a chat back:\n%s", plan.Stdout)
	}
	if code := shellRun(term, "restart-no-restore", "clear && "+session+" pfm restart --no-restore"); code != 0 {
		c.Failf("pfm restart --no-restore exited %d:\n%s", code, term.Screen())
	}
	log := restartLog(c)
	want := []string{fmt.Sprintf("restart: ended %d chat(s)", len(s.Chats)), "restart: every step verified"}
	for _, chat := range s.Chats {
		want = append(want, fmt.Sprintf("  ended  %-8s %-24s %s", "claude", chat.Name, chat.Session))
	}
	for _, line := range want {
		if !slices.Contains(log, line) {
			c.Failf("pfm restart --no-restore logged no %q:\n%s", line, strings.Join(log, "\n"))
		}
	}
	if slices.ContainsFunc(log, func(line string) bool { return strings.HasPrefix(line, "restart: brought back") }) {
		c.Failf("pfm restart --no-restore reports a chat brought back:\n%s", strings.Join(log, "\n"))
	}
	for _, chat := range s.Chats {
		c.Poll(chat.Name+" ended and resumable, no engine of it left", func() (bool, string) {
			rows := listRows(c, chat.Session)
			for pid, session := range engines(processes(c)) {
				if session == chat.Session {
					return false, fmt.Sprintf("engine %d", pid)
				}
			}
			return len(rows) == 1 && strings.HasPrefix(rows[0], "resume-claude\t"), strings.Join(rows, "\n")
		})
	}
	status := read(c, herdrCLI(s, "status", "--json")...)
	if !strings.Contains(status, `"running":true`) {
		c.Failf("Herdr session %s is not running after pfm restart --no-restore:\n%s", s.Session, status)
	}
	return s
}

// restartLog is the newest restart log's lines, the worker's whole plan and result, added to the
// step's log.
func restartLog(c *harness.Ctx) []string {
	dir := filepath.Join(logRoot(ownerHome), "restart")
	text := read(c, "sh", "-c", `cat "$(ls -1 "$1"/restart-*.log | tail -n 1)"`, "sh", dir)
	c.Logf("restart log:\n%s", text)
	return strings.Split(text, "\n")
}

// herdrLayout is the branch's Herdr session as its owner lays it out: each workspace by id and label,
// each tab with its panes by id, sorted, with the number of workspaces and whether a tab is split. Tab
// labels are left out: each chat's state mark changes them.
func herdrLayout(c *harness.Ctx, s State) (layout string, workspaces int, split bool) {
	var listed struct {
		Result struct {
			Workspaces []struct {
				ID    string `json:"workspace_id"`
				Label string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	var panes struct {
		Result struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				TabID  string `json:"tab_id"`
			} `json:"panes"`
		} `json:"result"`
	}
	workspaceOut, paneOut := read(c, herdrCLI(s, "workspace", "list")...), read(c, herdrCLI(s, "pane", "list")...)
	if err := json.Unmarshal([]byte(workspaceOut), &listed); err != nil {
		c.Failf("herdr workspace list: %v:\n%s", err, workspaceOut)
	}
	if err := json.Unmarshal([]byte(paneOut), &panes); err != nil {
		c.Failf("herdr pane list: %v:\n%s", err, paneOut)
	}
	var lines []string
	for _, workspace := range listed.Result.Workspaces {
		lines = append(lines, "workspace "+workspace.ID+" "+workspace.Label)
	}
	tabs := map[string][]string{}
	for _, pane := range panes.Result.Panes {
		tabs[pane.TabID] = append(tabs[pane.TabID], pane.PaneID)
	}
	for tab, ids := range tabs {
		sort.Strings(ids)
		split = split || len(ids) > 1
		lines = append(lines, "tab "+tab+" panes "+strings.Join(ids, " "))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n", len(listed.Result.Workspaces), split
}
