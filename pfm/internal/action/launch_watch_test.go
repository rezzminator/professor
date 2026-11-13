package action

import (
	"context"
	"errors"
	"testing"
)

// recordRetires replaces retireLaunchSeat for one test and returns the sockets it was asked to retire.
func recordRetires(t *testing.T) *[]string {
	t.Helper()
	var retired []string
	previous := retireLaunchSeat
	retireLaunchSeat = func(_ context.Context, socket string) error {
		retired = append(retired, socket)
		return nil
	}
	t.Cleanup(func() { retireLaunchSeat = previous })
	return &retired
}

// fakeLaunch is one created pane as the launch watch reads it: its screen,
// its foreground command, whether its process exited, and whether its whole
// server is already gone.
type fakeLaunch struct {
	screen  string
	command string
	tty     string
	dead    bool
	gone    bool
	// unreadable: the server runs but its pane cannot be read.
	unreadable bool
	// goneAfterReads: the pane shows screen for this many reads, then its
	// server is gone, as a launcher that prints and exits between two polls.
	goneAfterReads int
	// then: what the pane shows on the reads after the first, one each, its
	// last repeating.
	then []fakeLaunch
}

func (tmux *fakeActionTmux) LaunchPane(
	ctx context.Context,
	socket string,
) (LaunchPaneState, error) {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		// The real reads are context-bound tmux commands: a cancelled caller
		// kills them before they answer.
		return LaunchPaneState{}, err
	}
	tmux.launchReads++
	if tmux.paneReads == nil {
		tmux.paneReads = map[string]int{}
	}
	tmux.paneReads[socket]++
	reads := tmux.paneReads[socket]
	launch, found := tmux.launches[socket]
	if launch.goneAfterReads > 0 && tmux.launchReads > launch.goneAfterReads {
		launch.gone = true
		tmux.launches[socket] = launch
	}
	if !tmux.alive[socket] || launch.gone {
		return LaunchPaneState{}, errors.New("no server running on " + socket)
	}
	if !found {
		// An engine runs before it paints: the first read finds it on a blank pane.
		if reads == 1 {
			return LaunchPaneState{Command: "claude"}, nil
		}
		return LaunchPaneState{Screen: "fake chat ready", Command: "claude"}, nil
	}
	if reads > 1 && len(launch.then) > 0 {
		launch = launch.then[min(reads, len(launch.then)+1)-2]
	}
	if launch.unreadable {
		return LaunchPaneState{}, errors.New("tmux socket " + socket + " returned 1 launch pane fields")
	}
	return LaunchPaneState{Screen: launch.screen, Command: launch.command, TTY: launch.tty, Dead: launch.dead}, nil
}

func (tmux *fakeActionTmux) SetRemainOnExit(
	_ context.Context,
	socket string,
	on bool,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	state := "off"
	if on {
		state = "on"
	}
	tmux.remain = append(tmux.remain, socket+"\t"+state)
	return nil
}
