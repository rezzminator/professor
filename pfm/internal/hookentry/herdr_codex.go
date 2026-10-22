package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	"github.com/rezzminator/professor/pfm/internal/costledger"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// A Codex hook never names its pane through its own environment: a bare
// `codex` TUI hands its thread to the shared app-server daemon, whose hooks
// run with the daemon's environment (the $TMUX of whichever chat started it,
// no Herdr pane). The pane is registered instead, keyed by the payload's
// session_id, by a SessionStart whose hook runs under an interactive Codex
// TUI: the TUI's own environment, set by the pane that started it, names the
// pane. Every other event of that session reports there; an event of an
// unregistered session (the daemon's, another thread's) reports nothing.

const (
	codexPanePrefix = "codex-"
	codexPaneSuffix = ".pane"
	// codexAppServer is the subcommand of the shared daemon a bare TUI hands
	// its thread to.
	codexAppServer = "app-server"
)

// errCodexUnregistered is a lookup of a session with no registry record: the
// session is not one a TUI of this host registered.
var errCodexUnregistered = errors.New("no pane is registered for the Codex session")

// codexPane is one registered Codex session: its TUI process, and the tmux
// pane or the Herdr pane it reports to. A record with neither is a run with
// nowhere to report (a plain terminal, `codex exec`): its events stay silent.
type codexPane struct {
	PID         int    `json:"pid"`
	Command     string `json:"command"`
	TmuxSocket  string `json:"tmux_socket,omitempty"`
	TmuxPane    string `json:"tmux_pane,omitempty"`
	HerdrSocket string `json:"herdr_socket,omitempty"`
	HerdrPane   string `json:"herdr_pane,omitempty"`
}

// codexPanePath is the registry record of one Codex session's pane.
func codexPanePath(lockDir, session string) string {
	return filepath.Join(lockDir, codexPanePrefix+session+codexPaneSuffix)
}

// runCodex reports one Codex event to its session's registered pane. A
// session's end releases the pane and drops the record, unless the session
// swaps inside the same TUI (clear, resume), whose next SessionStart
// registers again. A host with neither Herdr nor the theme sync has nothing
// to report to: it registers nothing and logs nothing about a session it
// cannot map.
func (reporter *herdrReporter) runCodex(ctx context.Context, payload herdrPayload) (err error) {
	if !reporter.hostHasHerdr() && !reporter.themeSyncOn() {
		return nil
	}
	pane, path, err := reporter.codexPane(payload)
	if err != nil {
		// A session no TUI registered (the shared daemon's thread, another
		// chat's) is the normal case, not a fault: it reports nothing and logs
		// nothing. Every other failure keeps its row on a host with Herdr.
		if errors.Is(err, errCodexUnregistered) || !reporter.hostHasHerdr() {
			return nil
		}
		return err
	}
	if reporter.options.event == herdr.EventEnd &&
		payload.Reason != herdrReasonClear && payload.Reason != herdrReasonResume {
		defer func() { err = errors.Join(err, removeCodexPane(path)) }()
	}
	switch {
	case pane.TmuxSocket != "":
		reporter.options.socket, reporter.tmuxPane = pane.TmuxSocket, pane.TmuxPane
		return reporter.runTmux(ctx, payload)
	case pane.HerdrPane != "":
		return reporter.apply(ctx, payload, herdrTarget{pane: pane.HerdrPane, socket: pane.HerdrSocket})
	}
	return nil
}

// codexPane is the pane of the payload's session: registered now by a
// SessionStart, looked up by every other event.
func (reporter *herdrReporter) codexPane(payload herdrPayload) (codexPane, string, error) {
	session := payload.SessionID
	if !costledger.ValidSessionID(session) {
		return codexPane{}, "", fmt.Errorf("the Codex session id %q is not a plain name (unmapped, skipped)", session)
	}
	if reporter.deps.lockDir == "" {
		return codexPane{}, "", fmt.Errorf("no pfm state directory for the pane of Codex session %s (skipped)", session)
	}
	path := codexPanePath(reporter.deps.lockDir, session)
	if reporter.options.event == herdr.EventStart {
		pane, err := reporter.registerCodexPane(session, path)
		return pane, path, err
	}
	pane, err := reporter.lookupCodexPane(session, path)
	return pane, path, err
}

// registerCodexPane records the pane of the Codex process the hook runs
// under. A TUI's pane comes from its own environment: tmux's TMUX and
// TMUX_PANE, else Herdr's pane. A hook under no Codex process (an unlisted
// shell or launcher) and the shared daemon's `codex app-server` carry no pane
// and register nothing. A run that is no interactive chat (exec,
// review) keeps a record with no pane. The process's other sessions and every
// record whose process is gone are dropped: one pane, one session. A sweep
// that fails is logged on a host with Herdr, never a reason to drop this
// session's report.
func (reporter *herdrReporter) registerCodexPane(session, path string) (codexPane, error) {
	deps := reporter.deps
	if deps.procInfo == nil || deps.getpid == nil || deps.cmdline == nil || deps.environ == nil {
		return codexPane{}, fmt.Errorf(
			"no process table to find the TUI of Codex session %s (unmapped, skipped)",
			session,
		)
	}
	pid, command, err := reporter.codexProcess()
	if err != nil {
		return codexPane{}, fmt.Errorf("find the TUI of Codex session %s (unmapped, skipped): %w", session, err)
	}
	argv, err := deps.cmdline(pid)
	if err == nil && len(argv) == 0 {
		err = errors.New("no argv")
	}
	if err != nil {
		return codexPane{}, fmt.Errorf(
			"read the command line of pid %d, the Codex process of session %s (unmapped, skipped): %w",
			pid,
			session,
			err,
		)
	}
	if !pfmengine.MatchCommand(pfmengine.Codex, argv, false) &&
		!pfmengine.MatchCommand(pfmengine.Codex, []string{command}, false) {
		return codexPane{}, fmt.Errorf(
			"the hook of Codex session %s runs under %q (pid %d), which is no Codex process (unmapped, skipped)",
			session, strings.Join(argv, " "), pid,
		)
	}
	pane := codexPane{PID: pid, Command: command}
	if subcommand, _ := codexSubcommand(argv[1:]); subcommand == codexAppServer {
		return codexPane{}, fmt.Errorf(
			"the hook of Codex session %s runs under %q, not an interactive Codex TUI: the shared app-server daemon "+
				"carries no pane (unmapped, skipped)", session, strings.Join(argv, " "),
		)
	}
	if codexRunsAsInteractiveChat(argv[1:]) {
		environment, err := deps.environ(pid)
		if err != nil {
			return codexPane{}, fmt.Errorf(
				"read the environment of Codex TUI pid %d, session %s (unmapped, skipped): %w", pid, session, err,
			)
		}
		pane.setTarget(environment)
	}
	encoded, err := json.Marshal(pane)
	if err != nil {
		return codexPane{}, fmt.Errorf("encode the pane of Codex session %s: %w", session, err)
	}
	if err := os.MkdirAll(deps.lockDir, 0o700); err != nil {
		return codexPane{}, fmt.Errorf(
			"register the pane of Codex session %s: create %s: %w",
			session,
			deps.lockDir,
			err,
		)
	}
	if err := atomicfile.Write(path, encoded, 0o600); err != nil {
		return codexPane{}, fmt.Errorf("register the pane of Codex session %s: %w", session, err)
	}
	if err := reporter.sweepCodexPanes(path, pid); err != nil && reporter.hostHasHerdr() {
		reporter.warn("pfm internal herdr-state %s: sweep the Codex pane registry for session %s (fail-open): %v",
			reporter.options.event, session, err)
	}
	return pane, nil
}

// setTarget names the pane a TUI's environment was started in: a tmux pane
// when both TMUX and TMUX_PANE are set, else a Herdr pane, else none.
func (pane *codexPane) setTarget(environment map[string]string) {
	socket, _, _ := strings.Cut(environment["TMUX"], ",")
	if socket != "" && environment["TMUX_PANE"] != "" {
		pane.TmuxSocket, pane.TmuxPane = socket, environment["TMUX_PANE"]
		return
	}
	if environment[herdr.EnvMarker] != "1" || environment[herdr.EnvSocket] == "" || environment[herdr.EnvPane] == "" {
		return
	}
	pane.HerdrSocket, pane.HerdrPane = environment[herdr.EnvSocket], environment[herdr.EnvPane]
}

// codexProcess is the Codex process a hook runs under: the first process
// above the hook that is no shell, pfm or command wrapper.
func (reporter *herdrReporter) codexProcess() (pid int, command string, err error) {
	pid = reporter.deps.getpid()
	for depth := 0; depth < herdrMaxProcessDepth; depth++ {
		parent, name, err := reporter.deps.procInfo(pid)
		if err != nil {
			return 0, "", fmt.Errorf("walk up from the hook: %w", err)
		}
		if !isShellOrPfm(name) && !isCommandWrapper(name) {
			return pid, name, nil
		}
		if parent <= 1 || parent == pid {
			return 0, "", errors.New("walk up from the hook: no process above it but shells and pfm")
		}
		pid = parent
	}
	return 0, "", fmt.Errorf("walk up from the hook: deeper than %d processes", herdrMaxProcessDepth)
}

// lookupCodexPane reads the registered pane of a session. A record whose
// process is gone, or is now another command, is removed: its pid may name an
// unrelated process now. A process that cannot be read keeps its record.
func (reporter *herdrReporter) lookupCodexPane(session, path string) (codexPane, error) {
	pane, err := readCodexPane(path)
	if errors.Is(err, os.ErrNotExist) {
		return codexPane{}, fmt.Errorf("%w %s (unmapped, skipped)", errCodexUnregistered, session)
	}
	if err != nil {
		return codexPane{}, fmt.Errorf("read the pane of Codex session %s (skipped): %w", session, err)
	}
	alive, err := reporter.codexProcessAlive(pane)
	if err != nil {
		return codexPane{}, fmt.Errorf(
			"could not read the TUI process %d of Codex session %s (registration kept, skipped): %w",
			pane.PID, session, err,
		)
	}
	if alive {
		return pane, nil
	}
	return codexPane{}, errors.Join(
		fmt.Errorf(
			"the TUI process %d of Codex session %s is gone or runs another command (registration removed, skipped)",
			pane.PID, session,
		),
		removeCodexPane(path),
	)
}

// codexProcessAlive reports whether a record's process still runs its
// recorded command; without a process table it is believed. A process table
// entry that cannot be read for any reason but the process being gone is an
// error: the process may well be running.
func (reporter *herdrReporter) codexProcessAlive(pane codexPane) (bool, error) {
	if reporter.deps.procInfo == nil {
		return true, nil
	}
	_, command, err := reporter.deps.procInfo(pane.PID)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return command == pane.Command, nil
}

// sweepCodexPanes removes every record but kept that names process pid or a
// process that is gone; a record that cannot be read is removed too, a record
// whose process cannot be read is kept.
func (reporter *herdrReporter) sweepCodexPanes(kept string, pid int) error {
	records, err := filepath.Glob(filepath.Join(reporter.deps.lockDir, codexPanePrefix+"*"+codexPaneSuffix))
	if err != nil {
		return fmt.Errorf("list the Codex pane registrations: %w", err)
	}
	var errs []error
	for _, record := range records {
		if record == kept {
			continue
		}
		pane, err := readCodexPane(record)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil && pane.PID != pid {
			if alive, aliveErr := reporter.codexProcessAlive(pane); alive || aliveErr != nil {
				continue
			}
		}
		errs = append(errs, removeCodexPane(record))
	}
	return errors.Join(errs...)
}

func readCodexPane(path string) (codexPane, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return codexPane{}, err
	}
	var pane codexPane
	if err := json.Unmarshal(data, &pane); err != nil {
		return codexPane{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if pane.PID <= 0 {
		return codexPane{}, fmt.Errorf("decode %s: no process id", path)
	}
	return pane, nil
}

func removeCodexPane(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the Codex pane registration %s: %w", path, err)
	}
	return nil
}

// Codex subagents share the TUI's ancestry, never its state or restore identity.
// Missing/newborn rollouts are not proof of a child: preserve root hooks on doubt.
func (reporter *herdrReporter) codexSubagent(ctx context.Context, payload herdrPayload) bool {
	if reporter.options.engine != pfmengine.Codex || reporter.options.event.FiredByTmux() {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	if payload.TranscriptPath != "" {
		header, err := codexmeta.ReadHeader(payload.TranscriptPath)
		switch {
		case err != nil:
			reporter.warn("Codex hook %s session %s: read lineage from %s (report retained unless child proven): %v",
				reporter.options.event, payload.SessionID, payload.TranscriptPath, err)
		case header.ID != "" && header.ID != payload.SessionID:
			reporter.warn("Codex hook %s session %s: rollout %s belongs to %s (identity ignored, report retained)",
				reporter.options.event, payload.SessionID, payload.TranscriptPath, header.ID)
		case codexChildIdentity(header):
			return true
		case header.Kind == codexmeta.User:
			return false
		}
	}
	descriptor := pfmengine.MustLookup(pfmengine.Codex)
	// The launcher's selected account is authoritative; RootEnv is a discovery
	// search list, not necessarily the home this Codex process uses.
	homes := []string{reporter.deps.getenv(descriptor.HomeEnv)}
	if homes[0] == "" {
		homes = filepath.SplitList(reporter.deps.getenv(descriptor.RootEnv))
		if len(homes) == 0 {
			homes = []string{reporter.deps.codexHome}
		}
	}
	for _, home := range homes {
		files, err := store.CodexStateFiles(home)
		if err != nil {
			reporter.warn("Codex hook %s session %s: list lineage stores in %s (report retained): %v",
				reporter.options.event, payload.SessionID, home, err)
			continue
		}
		for _, file := range files {
			identity, found, err := store.ReadCodexThreadIdentity(ctx, file, payload.SessionID)
			if err != nil {
				reporter.warn(
					"Codex hook %s session %s: inspect lineage store %s (report retained unless child proven): %v",
					reporter.options.event,
					payload.SessionID,
					file,
					err,
				)
				continue
			}
			if found {
				return codexChildIdentity(codexmeta.Header{Fields: identity})
			}
		}
	}
	return false
}

func codexChildIdentity(header codexmeta.Header) bool {
	return header.Kind == codexmeta.Subagent || header.ThreadSource == "subagent" ||
		header.ParentThreadID != "" || strings.HasPrefix(header.AgentPath, "/root/")
}
