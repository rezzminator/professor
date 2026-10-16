package themesync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// tmuxOption is one global tmux option and the value the theme gives it.
type tmuxOption struct{ Name, Value string }

const (
	tmuxPriorPrefix   = "@pfm-theme-prior-"
	tmuxGenerationKey = "@pfm-theme-generation"
	tmuxLivenessProbe = "status-style"
	tmuxPriorSentinel = "v:"
)

// tmuxStyleNames are the built-in options the theme styles; each one's prior
// value is kept on the same server before the first set.
var tmuxStyleNames = []string{
	"status-style", "window-status-style", "window-status-current-style", "pane-border-style",
	"pane-active-border-style", "message-style", "message-command-style", "mode-style",
}

// tmuxPriorName is the user option that keeps an option's value from before the theme.
func tmuxPriorName(option string) string { return tmuxPriorPrefix + option }

// tmuxPriorValue stores a prior with a sentinel so an empty prior still reads as captured.
func tmuxPriorValue(value string) string { return tmuxPriorSentinel + value }

// tmuxPriorParse is the captured prior, or false when none was captured.
func tmuxPriorParse(raw string) (string, bool) {
	value, ok := strings.CutPrefix(raw, tmuxPriorSentinel)
	return value, ok
}

// tmuxStateFormat is a tmux conditional format that picks the state colour
// from the @pfm-state option the agent-state hook sets on its own server.
func tmuxStateFormat(state StateColors) string {
	return "#{?#{==:#{@pfm-state},blocked}," + state.Blocked +
		",#{?#{==:#{@pfm-state},working}," + state.Working +
		",#{?#{==:#{@pfm-state},done}," + state.Done + "," + state.Idle + "}}}"
}

// tmuxOptions renders the 8 styles then the 13 @pfm-* user options for one
// appearance at one generation.
func tmuxOptions(applied Applied, appearance Appearance, generation int) []tmuxOption {
	variant := applied.variant(appearance)
	format := tmuxStateFormat(variant.State)
	options := []tmuxOption{
		{"status-style", "bg=" + variant.Surface + ",fg=" + variant.Foreground},
		{"window-status-style", "fg=" + variant.Muted},
		{"window-status-current-style", "fg=" + format + ",bold"},
		{"pane-border-style", "fg=" + variant.Muted},
		{"pane-active-border-style", "fg=" + format},
		{"message-style", "bg=" + variant.Overlay + ",fg=" + variant.Foreground},
		{"message-command-style", "bg=" + variant.Surface + ",fg=" + variant.Accent},
		{"mode-style", "bg=" + variant.SelectionBackground + ",fg=" + variant.SelectionForeground},
		{"@pfm-theme", applied.Value},
		{tmuxGenerationKey, strconv.Itoa(generation)},
		{"@pfm-c-bg", variant.Background},
		{"@pfm-c-fg", variant.Foreground},
		{"@pfm-c-surface", variant.Surface},
		{"@pfm-c-overlay", variant.Overlay},
		{"@pfm-c-muted", variant.Muted},
		{"@pfm-c-accent", variant.Accent},
		{"@pfm-state-blocked", variant.State.Blocked},
		{"@pfm-state-working", variant.State.Working},
		{"@pfm-state-done", variant.State.Done},
		{"@pfm-state-idle", variant.State.Idle},
		{"@pfm-state-colour", format},
	}
	return options
}

// tmuxApplySocket themes one chat server: liveness first (a dead socket is an
// error naming it), each style's prior captured once — on a server not yet
// themed, so a failed read of a captured prior never overwrites it with a
// themed value — then every option set in one apply.
func (s *session) tmuxApplySocket(ctx context.Context, socket string) error {
	if _, err := s.servers.Show(ctx, socket, tmuxLivenessProbe); err != nil {
		return fmt.Errorf("tmux %s: %w", socket, err)
	}
	mark, markErr := s.servers.Show(ctx, socket, tmuxGenerationKey)
	themed := markErr == nil && mark != ""
	var commands [][]string
	options := tmuxOptions(s.theme(), s.state.Appearance, s.state.Generation)
	for _, option := range options {
		if themed || strings.HasPrefix(option.Name, "@") {
			continue
		}
		current, err := s.servers.Show(ctx, socket, option.Name)
		if err != nil {
			return fmt.Errorf("tmux %s: read %s: %w", socket, option.Name, err)
		}
		// An unset prior reads as an error once the server proved alive.
		if prior, priorErr := s.servers.Show(ctx, socket, tmuxPriorName(option.Name)); priorErr != nil || prior == "" {
			commands = append(
				commands,
				[]string{tmuxSetOption, tmuxGlobal, tmuxPriorName(option.Name), tmuxPriorValue(current)},
			)
		}
	}
	for _, option := range options {
		commands = append(commands, []string{tmuxSetOption, tmuxGlobal, option.Name, option.Value})
	}
	if err := s.servers.Apply(ctx, socket, commands); err != nil {
		return fmt.Errorf("tmux %s: apply: %w", socket, err)
	}
	return nil
}

const (
	tmuxSetOption = "set-option"
	tmuxGlobal    = "-g"
	tmuxUnset     = "-gu"
)

func (s *session) tmuxSockets(ctx context.Context) ([]string, error) {
	if s.servers == nil {
		return nil, nil
	}
	sockets, err := s.servers.Sockets(ctx)
	if err != nil {
		return nil, fmt.Errorf("themesync: list tmux servers: %w", err)
	}
	sort.Strings(sockets)
	return sockets, nil
}

func tmuxServerCount(count int) string {
	if count == 1 {
		return "1 server"
	}
	return fmt.Sprintf("%d servers", count)
}

func (s *session) applyTmux(ctx context.Context) SurfaceResult {
	sockets, err := s.tmuxSockets(ctx)
	if err != nil {
		return s.failed(surfaceTmux, "", err)
	}
	if len(sockets) == 0 {
		return s.absent(surfaceTmux, "", "no live pfm chat servers")
	}
	s.serverCount = len(sockets)
	var failures []string
	for _, socket := range sockets {
		if err := s.tmuxApplySocket(ctx, socket); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return s.failed(surfaceTmux, "", fmt.Errorf("%d of %s failed: %s",
			len(failures), tmuxServerCount(len(sockets)), strings.Join(failures, "; ")))
	}
	return s.result(surfaceTmux, VerdictApplied, "", "")
}

func (s *session) readTmux(ctx context.Context) SurfaceResult {
	sockets, err := s.tmuxSockets(ctx)
	if err != nil {
		return s.failed(surfaceTmux, "", err)
	}
	if len(sockets) == 0 {
		return s.absent(surfaceTmux, "", "no live pfm chat servers")
	}
	s.serverCount = len(sockets)
	options := tmuxOptions(s.theme(), s.state.Appearance, s.state.Generation)
	var errs, drift []string
	generation := 0
	for _, socket := range sockets {
		for _, option := range options {
			got, err := s.servers.Show(ctx, socket, option.Name)
			if err != nil {
				errs = append(errs, fmt.Sprintf("tmux %s: read %s: %v", socket, option.Name, err))
				break
			}
			if option.Name == tmuxGenerationKey && generation == 0 {
				generation, _ = strconv.Atoi(got)
			}
			if got != option.Value {
				drift = append(drift, fmt.Sprintf("tmux %s: %s = %q, want %q", socket, option.Name, got, option.Value))
				break
			}
		}
	}
	switch {
	case len(errs) > 0:
		return s.in(surfaceTmux, VerdictError, "", strings.Join(append(errs, drift...), "; "), 0)
	case len(drift) > 0:
		return s.in(surfaceTmux, VerdictDrift, "", strings.Join(drift, "; "), generation)
	default:
		return s.in(surfaceTmux, VerdictInSync, "", tmuxServerCount(len(sockets)), generation)
	}
}

// tmuxRestoreSocket puts each style's prior back (or, on a server the theme
// reached, unsets it when none was captured) and removes every @pfm-* theme
// option from one server. A server the theme never reached — one born after an
// earlier off — keeps its own styles.
func (s *session) tmuxRestoreSocket(ctx context.Context, socket string) error {
	if _, err := s.servers.Show(ctx, socket, tmuxLivenessProbe); err != nil {
		return fmt.Errorf("tmux %s: %w", socket, err)
	}
	mark, markErr := s.servers.Show(ctx, socket, tmuxGenerationKey)
	themed := markErr == nil && mark != ""
	var commands [][]string
	for _, name := range tmuxStyleNames {
		raw, err := s.servers.Show(ctx, socket, tmuxPriorName(name))
		if value, ok := tmuxPriorParse(raw); err == nil && ok {
			commands = append(commands, []string{tmuxSetOption, tmuxGlobal, name, value})
		} else if themed {
			commands = append(commands, []string{tmuxSetOption, tmuxUnset, name})
		}
		commands = append(commands, []string{tmuxSetOption, tmuxUnset, tmuxPriorName(name)})
	}
	for _, option := range tmuxOptions(s.theme(), s.state.Appearance, s.state.Generation) {
		if strings.HasPrefix(option.Name, "@") {
			commands = append(commands, []string{tmuxSetOption, tmuxUnset, option.Name})
		}
	}
	commands = append(commands, []string{tmuxSetOption, tmuxUnset, tmuxStateName})
	if err := s.servers.Apply(ctx, socket, commands); err != nil {
		return fmt.Errorf("tmux %s: restore: %w", socket, err)
	}
	return nil
}

func (s *session) restoreTmux(ctx context.Context) SurfaceResult {
	sockets, err := s.tmuxSockets(ctx)
	if err != nil {
		return s.failed(surfaceTmux, "", err)
	}
	if len(sockets) == 0 {
		return s.absent(surfaceTmux, "", "no live pfm chat servers")
	}
	var failures []string
	for _, socket := range sockets {
		if err := s.tmuxRestoreSocket(ctx, socket); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return s.failed(surfaceTmux, "", errors.New(strings.Join(failures, "; ")))
	}
	return s.result(surfaceTmux, VerdictRestored, "", tmuxServerCount(len(sockets)))
}
