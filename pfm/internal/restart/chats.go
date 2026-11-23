package restart

import (
	"context"
	"fmt"
	"sort"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// chatBackWithin bounds the wait for every chat the restart brings back: one
// deadline all of them share, counted from the first wait, which starts once
// the Herdr server is up again. Each resume starts its engine afresh.
const chatBackWithin = 90 * time.Second

// Fleet is the chat roster the restart ends or brings back: every live chat,
// the sockets the scan could not read (with why), the end of one chat,
// confirmed, why a Herdr chat's seat could not resume after its server
// restarts ("" when it can) with the seat host its seat names now, and
// whether a chat is live again on the Herdr server at socket under a seat
// host other than before, with what was seen.
type Fleet interface {
	Live(ctx context.Context) (chats []headless.Chat, unreadable map[string]string, err error)
	End(ctx context.Context, chat headless.Chat) error
	Restorable(ctx context.Context, chat headless.Chat) (why string, host pfmchat.RestartSeatHost, err error)
	Back(ctx context.Context, chat headless.Chat, socket string, before pfmchat.RestartSeatHost) (
		back bool, seen string, err error)
}

// findChats is the chats family: every live Claude, Codex and OpenCode chat
// the fleet scan sees, the launching chat last. A socket the scan could not
// read is a failed find, named.
func (finder *discoverer) findChats(ctx context.Context) {
	if finder.deps.Fleet == nil {
		finder.failed(FamilyChats, fmt.Errorf("no fleet to scan"))
		return
	}
	chats, unreadable, err := finder.deps.Fleet.Live(ctx)
	if err != nil {
		finder.failed(FamilyChats, fmt.Errorf("fleet scan: %w", err))
		return
	}
	sort.SliceStable(chats, func(left, right int) bool {
		return !finder.deps.LaunchedFrom.holds(chats[left]) && finder.deps.LaunchedFrom.holds(chats[right])
	})
	finder.found(
		FamilyChats,
		fmt.Sprintf("%d live chat(s) — fleet scan of every tmux socket and Herdr seat", len(chats)),
	)
	sockets := make([]string, 0, len(unreadable))
	for socket := range unreadable {
		sockets = append(sockets, socket)
	}
	sort.Strings(sockets)
	for _, socket := range sockets {
		finder.failed(FamilyChats, fmt.Errorf("tmux socket %s could not be read, its chats are unknown: %s",
			socket, unreadable[socket]))
	}
	wait := &chatWait{
		tools: serviceTools{runner: finder.deps.Runner, clock: finder.deps.Clock},
		fleet: finder.deps.Fleet,
		herdr: &finder.herdrStarted,
	}
	for index := range chats {
		finder.plan.Units = append(finder.plan.Units, finder.chatUnit(ctx, chats[index], wait))
	}
}

// chatUnit is one chat's step. Unless the restart runs --no-restore, a chat
// Herdr can restore is left running: the Herdr server stop hangs up its pane,
// which Herdr keeps, with the resume pfm registered, in its saved session, and
// its start verifies the restored pane resumed it. Every other chat is ended,
// named with why it does not come back.
func (finder *discoverer) chatUnit(ctx context.Context, chat headless.Chat, wait *chatWait) Unit {
	engine := string(chat.Engine)
	if descriptor, err := pfmengine.Lookup(chat.Engine); err == nil {
		engine = descriptor.LongName
	}
	name := chat.Name
	if name == "" {
		name = "(unnamed)"
	}
	found := "fleet scan: tmux socket " + chat.Socket + " pane " + chat.Pane
	if chat.Backend == spawn.BackendHerdr {
		found = "fleet scan: Herdr seat " + chat.SeatID
	}
	if finder.deps.LaunchedFrom.holds(chat) {
		found += " (the chat that typed pfm restart)"
	}
	fleet := finder.deps.Fleet
	report := &ChatReport{Engine: engine, Name: name, SessionID: chat.ID}
	unit := Unit{
		Family:  FamilyChats,
		Name:    fmt.Sprintf("%s %s session %s", engine, name, chat.ID),
		FoundBy: found,
		StopHow: pfmchat.EndHow(chat) + "; not brought back",
		Stop: func(ctx context.Context) (string, error) {
			if err := fleet.End(ctx, chat); err != nil {
				return "", err
			}
			return "ended", nil
		},
		Chat: report,
	}
	if finder.deps.NoRestore {
		return unit
	}
	var before pfmchat.RestartSeatHost
	if report.NotBack, before = finder.notBack(ctx, chat); report.NotBack != "" {
		unit.StopHow += ": " + report.NotBack
		return unit
	}
	socket := chat.Socket
	unit.Before = "live in Herdr pane " + chat.Pane
	unit.StopHow = "left running: the Herdr server stop hangs up its pane, which Herdr keeps in its saved session " +
		"with the resume pfm registered"
	unit.StartHow = fmt.Sprintf("Herdr restores pane %s and runs its resume (pfm chat open --restored); "+
		"verified: seat %s reads live on %s with a new running seat host", chat.Pane, chat.SeatID, socket)
	unit.Stop = func(context.Context) (string, error) { return "left running for the Herdr server stop", nil }
	unit.Start = func(ctx context.Context) (string, error) { return wait.back(ctx, chat, socket, before) }
	return unit
}

// notBack is why chat would not come back with Herdr's restore, "" when it
// would, with the seat host its seat names now: the one the Herdr server stop
// ends. A read that failed is named as the reason: the chat is then ended,
// never left to a restore no one proved.
func (finder *discoverer) notBack(ctx context.Context, chat headless.Chat) (string, pfmchat.RestartSeatHost) {
	none := pfmchat.RestartSeatHost{}
	if chat.Backend != spawn.BackendHerdr {
		return "it runs in tmux, and only a chat in a Herdr pane comes back with Herdr's restore", none
	}
	look := finder.lookHerdr(ctx)
	switch {
	case look.err != nil:
		return "the Herdr server could not be read: " + stepLine(look.err.Error()), none
	case look.binary == "":
		return look.name + " is not on PATH, so no Herdr server restarts", none
	case !look.status.Running:
		return "no Herdr server is running, so none restarts", none
	case chat.Socket != look.status.Socket:
		return "it runs on Herdr server " + chat.Socket + ", not " + look.status.Socket +
			", the one this restart restarts", none
	}
	if why := finder.resumeWhy(); why != "" {
		return why, none
	}
	why, host, err := finder.deps.Fleet.Restorable(ctx, chat)
	if err != nil {
		return "whether its seat can resume could not be read: " + stepLine(err.Error()), none
	}
	return why, host
}

// resumeWhy reads Herdr's resume_agents_on_restore once per plan: why no
// Herdr chat comes back, "" when Herdr resumes each restored pane's agent.
func (finder *discoverer) resumeWhy() string {
	if finder.resume != nil {
		return *finder.resume
	}
	on, err := false, fmt.Errorf("no reader of Herdr's config")
	if finder.deps.ResumeOnRestore != nil {
		on, err = finder.deps.ResumeOnRestore()
	}
	why := ""
	switch {
	case err != nil:
		why = "whether Herdr resumes agents on restore could not be read: " + stepLine(err.Error())
	case !on:
		why = "Herdr's resume_agents_on_restore is off, so its pane would come back without the chat"
	}
	finder.resume = &why
	return why
}

// chatWait is the one deadline every brought-back chat shares, and how the
// Herdr server start they all wait on went.
type chatWait struct {
	tools    serviceTools
	fleet    Fleet
	herdr    *herdrStarted
	deadline time.Time
}

// back polls until chat is live again on socket under a seat host other than
// before, within what is left of the shared deadline. A read that fails is
// kept as what was last seen and the poll goes on: the resume may still be
// writing the seat. A Herdr server that did not start again restores nothing,
// so no poll waits on it.
func (wait *chatWait) back(ctx context.Context, chat headless.Chat, socket string, before pfmchat.RestartSeatHost,
) (string, error) {
	if wait.herdr != nil && wait.herdr.err != nil {
		return "", fmt.Errorf("the Herdr server did not start again, so Herdr restored nothing; "+
			"reopen it by hand: pfm chat open %s", chat.ID)
	}
	if wait.deadline.IsZero() {
		wait.deadline = wait.tools.clock.Now().Add(chatBackWithin)
	}
	seen := "nothing read"
	up, err := wait.tools.waitFor(ctx, wait.deadline.Sub(wait.tools.clock.Now()), func() (bool, error) {
		back, detail, err := wait.fleet.Back(ctx, chat, socket, before)
		if err != nil {
			seen = err.Error()
			return false, nil
		}
		seen = detail
		return back, nil
	})
	if err != nil {
		return "", err
	}
	if !up {
		return "", fmt.Errorf("not live again within %s of the Herdr server start (last seen: %s); "+
			"reopen it by hand: pfm chat open %s", chatBackWithin, seen, chat.ID)
	}
	return "live again: " + seen, nil
}

// herdrStarted is how the restart's Herdr server start went: its error once it failed.
type herdrStarted struct {
	err error
}

// track wraps the Herdr server's start so every brought-back chat's start,
// which runs after it, reads how it went.
func (started *herdrStarted) track(start func(context.Context) (string, error)) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		detail, err := start(ctx)
		started.err = err
		return detail, err
	}
}
