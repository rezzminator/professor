package doctor

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The two states a live tmux server can be in for the tmux.titles concept.
// They are stated as values, not inferred at each print site, so the check and
// the config key describe the same two things in the same words.
const (
	titlesPfmOwned  = "pfm-owned"
	titlesHostOwned = "host-owned"
	titlesDivergent = "divergent"
)

// printTmuxTitlesDoctor REPORTS which side owns the outer terminal's title on
// every live socket. It never fails and never modifies.
//
// pfm takes over a host-level surface here: `set-titles on` plus its own
// set-titles-string. A host that emits its own OSC title on the outer pty
// before tmux starts keeps it only while `set-titles off` stands, so a
// framework that flips it must at minimum record that it did — otherwise the
// next reader cannot tell "pfm owns this" from "the host owns this and pfm has
// not stomped it yet", and reads a deliberate setting as drift. That
// misreading is exactly how eight live servers had their tab badges destroyed
// by a well-meaning fix.
//
// Both states are legitimate — INFO, never a warning — ONLY when a server's
// actual ownership matches what the config key intends. When it does not, the
// line says so explicitly as a DIVERGENCE and the summary counts it: a server
// left behind by a policy change, or by a scheduler outage that never reached
// it, is not a deliberate opt-out, and reporting it as one is exactly how five
// live servers went unnoticed for weeks with the wrong OSC title never
// emitted at all.
func printTmuxTitlesDoctor(
	ctx context.Context,
	stdout io.Writer,
	resolved paths.Values,
	machine config.Config,
) {
	printTmuxTitlesDoctorWithClock(ctx, stdout, resolved, machine, clock.Real)
}

func printTmuxTitlesDoctorWithClock(
	ctx context.Context,
	stdout io.Writer,
	resolved paths.Values,
	machine config.Config,
	clk clock.Clock,
) {
	intended := titlesHostOwned
	if machine.Tmux.Titles.Enabled {
		intended = titlesPfmOwned
	}
	fmt.Fprintf(
		stdout,
		"doctor: tmux titles policy=%s (config tmux.titles.enabled=%t %s)\n",
		intended, machine.Tmux.Titles.Enabled, machine.Source("tmux.titles.enabled"),
	)

	client := gather.TmuxProbe{TmuxTmpDir: filepath.Dir(resolved.TmuxDir)}
	if clk == nil {
		clk = clock.Real
	}
	probe, err := gather.ProbeTmuxReadOnly(ctx, resolved.TmuxDir, client, clk.Now())
	if err != nil {
		fmt.Fprintf(stdout, "doctor: tmux titles sockets=unprobed error=%v\n", err)
		return
	}
	seen := make(map[string]bool, len(probe.Panes))
	sockets := make([]string, 0, len(probe.Panes))
	for index := range probe.Panes {
		pane := &probe.Panes[index]
		if seen[pane.Socket] {
			continue
		}
		seen[pane.Socket] = true
		sockets = append(sockets, pane.Socket)
	}
	sort.Strings(sockets)
	if len(sockets) == 0 {
		fmt.Fprintln(stdout, "doctor: tmux titles sockets=none live")
		return
	}
	divergent := 0
	for _, socket := range sockets {
		state, detail := readTmuxTitlesState(ctx, client, socket, machine.Tmux.Titles.Enabled)
		// A server whose state could not be read is neither a match nor a
		// divergence — it is an unanswered question, and claiming either
		// answer for it would be a guess reported as a fact.
		if state != titlesPfmOwned && state != titlesHostOwned && state != titlesDivergent {
			fmt.Fprintf(stdout, "doctor: tmux titles %s=%s (%s)\n", socket, state, detail)
			continue
		}
		if state == intended {
			fmt.Fprintf(stdout, "doctor: tmux titles %s=%s (%s)\n", socket, state, detail)
			continue
		}
		divergent++
		expected := "off"
		if intended == titlesPfmOwned {
			expected = "on"
		}
		fmt.Fprintf(
			stdout,
			"doctor: tmux titles %s=%s (%s) DIVERGES from policy=%s: expected set-titles %s\n",
			socket, state, detail, intended, expected,
		)
	}
	fmt.Fprintf(stdout, "doctor: tmux titles divergent=%d\n", divergent)
}

// readTmuxTitlesState asks one live server for the options that make up the
// title policy. A PFM-owned policy includes both set-titles and the canonical
// set-titles-string. A host-owned policy intentionally checks only set-titles:
// disabled mode leaves the host's string untouched, so a stale PFM string is
// not evidence that pfm has taken ownership.
func readTmuxTitlesState(
	ctx context.Context,
	tmux gather.TmuxProbe,
	socket string,
	pfmEnabled bool,
) (state, detail string) {
	commandContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	actualTitles, err := tmux.ShowGlobalOption(commandContext, socket, "set-titles")
	if err != nil {
		return StateUnknown, fmt.Sprintf("show-options failed: %v", err)
	}
	if !pfmEnabled {
		if actualTitles == "off" {
			return titlesHostOwned, "set-titles off"
		}
		return titlesDivergent, fmt.Sprintf("set-titles %s; expected set-titles off", actualTitles)
	}
	actualString, err := tmux.ShowGlobalOption(commandContext, socket, "set-titles-string")
	if err != nil {
		return StateUnknown, fmt.Sprintf("show-options failed: %v", err)
	}
	if actualTitles == "on" && actualString == config.TmuxTitlesString {
		// Keep the ownership row compact; the string is still read and compared
		// above, and any drift is exposed in the divergent detail below.
		return titlesPfmOwned, "set-titles on"
	}
	return titlesDivergent, fmt.Sprintf(
		"set-titles %s; set-titles-string %q; expected set-titles on and set-titles-string %q",
		actualTitles, actualString, config.TmuxTitlesString,
	)
}
