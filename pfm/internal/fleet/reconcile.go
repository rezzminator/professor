package fleet

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/spawn"
	"hostops/pfm/internal/store"
)

// CodexRolloutFingerprint is one Codex PID's FDLinks-observed rollout
// identity as of the last time it was checked. streamFleetRefreshesWith
// caches one of these per PID across parked polls so it can tell whether
// gather.RefreshCodexHeldRollouts' procfs probe actually changed anything
// before paying for ReconcileCodexPanes' tmux capture-pane over every live
// Codex pane (2026-09-08 measurement — see fleetRefreshParkPollInterval).
type CodexRolloutFingerprint struct {
	rolloutPath   string
	rolloutHeld   bool
	identityError string
}

// CodexRolloutFingerprints snapshots the comparable identity of every PID in
// codex. Two snapshots with the same fingerprints for the same PID set mean
// procfs saw no change worth a capture-pane.
func CodexRolloutFingerprints(codex []gather.LiveCodex) map[int]CodexRolloutFingerprint {
	fingerprints := make(map[int]CodexRolloutFingerprint, len(codex))
	for _, process := range codex {
		fingerprints[process.PID] = CodexRolloutFingerprint{
			rolloutPath:   process.RolloutPath,
			rolloutHeld:   process.RolloutHeld,
			identityError: process.IdentityError,
		}
	}
	return fingerprints
}

// CodexRolloutFingerprintsEqual reports whether two fingerprint snapshots
// name the exact same PIDs holding the exact same rollout identities. A
// PID appearing, disappearing, or changing what it holds all count as a
// change — only bit-for-bit agreement counts as "nothing moved".
func CodexRolloutFingerprintsEqual(a, b map[int]CodexRolloutFingerprint) bool {
	if len(a) != len(b) {
		return false
	}
	for pid, fingerprint := range a {
		other, found := b[pid]
		if !found || other != fingerprint {
			return false
		}
	}
	return true
}

// CodexRolloutFingerprintsSkippable reports whether procfs alone already
// pins the resolved identity for EVERY live Codex process, making a
// capture-pane over their panes redundant when combined with an unchanged
// fingerprint. ObserveCodexPanes overrides whatever a pane's screen shows in
// exactly two cases: a process holding its own rollout open (RolloutHeld —
// the live process wins outright) and a process carrying an IdentityError
// (processConflicts forces identity.Failed regardless of pane text). Every
// OTHER live Codex process — RolloutHeld false with no error, the shape
// DetectCodexThreads' own doc calls "the normal shape of a paginated thread
// since Codex 0.146.1" — has no procfs-derived opinion at all: the pane's
// own screen is the only identity signal that exists for it, so it is never
// skippable no matter how long its (nonexistent) rollout stays the same.
func CodexRolloutFingerprintsSkippable(codex []gather.LiveCodex) bool {
	for _, process := range codex {
		if !process.RolloutHeld && process.IdentityError == "" {
			return false
		}
	}
	return true
}

// ReconcileCodexPanes is the clear-detection pass itself, run every gather
// pass: for each live Codex pane it reads the pane's own status line, hands
// every pane at once to DecideCodexPanes, and applies that ruling — advance
// the binding, retire the thread a /clear replaced with the same
// prompt-baseline kill Claude's SessionEnd hook gets, and re-apply the chat's
// established name to the new thread. Codex has no launch flag for a thread
// name, so without that last step the pane runs the new thread unnamed
// forever.
//
// The ruling itself lives in codexpanes.go, pure and shared with
// `pfm doctor`, so the health report cannot disagree with the decision it
// reports on. Read the law at the top of that file before changing anything
// here: a name may SEED an unbound pane, and only an observed thread id may
// MOVE one.
//
// Every pane this pass declines to act on carries a reason. The loud ones
// reach stderr now; the rest wait in `pfm doctor --verbose`, because this
// runs behind an interactive picker and a warning on every pass for an
// ordinary one-refresh lag is how a real signal gets tuned out.
func ReconcileCodexPanes(
	ctx context.Context,
	database *store.Store,
	live gather.Snapshot,
	runtime pfmconfig.Runtime,
	warn Warn,
) bool {
	return ReconcileCodexPanesWith(
		ctx,
		database,
		live,
		runtime,
		spawn.CommandTmux{TmuxDir: runtime.Paths.TmuxDir},
		warn,
	)
}

// ReconcileCodexPanesWith is ReconcileCodexPanes re-applying names through
// renamer. Renaming is the one step of the pass that has to talk to a live
// Codex composer, so a test hands its own driver here: before this seam the
// step had NO automated coverage — and neither did anything sequenced after
// it. That is exactly where the missing cx_names record hid.
func ReconcileCodexPanesWith(
	ctx context.Context,
	database *store.Store,
	live gather.Snapshot,
	runtime pfmconfig.Runtime,
	renamer spawn.Tmux,
	warn Warn,
) bool {
	changed := false
	manager, err := kill.New(database, KillDependencies(runtime))
	if err != nil {
		warn(fmt.Sprintf("Codex pane reconcile: %v", err))
		return changed
	}
	cxNames, err := database.CxNames(ctx)
	if err != nil {
		warn(fmt.Sprintf("Codex pane reconcile: read thread names: %v", err))
		return changed
	}
	capturer := gather.CommandTmux{TmuxTmpDir: filepath.Dir(runtime.Paths.TmuxDir)}

	_, actions := ObserveCodexPanes(ctx, database, manager, capturer, live, runtime, cxNames, warn)
	for index := range actions {
		action := actions[index]
		if action.Skip != "" && action.Bind == "" {
			switch {
			case action.Forget:
				// One event, one line. The reason and the repair are the same
				// event, and reporting them as two separate warnings taught an
				// operator that a SUCCESSFUL self-repair looks like a pair of
				// failures. Only the pass that WRITES may claim the write —
				// the same reason string reaches read-only `pfm doctor`, and a
				// report claiming a repair it never performed is the failure
				// mode this whole wave is about.
				if err := manager.ForgetCodexPane(ctx, action.Socket, action.PaneID); err != nil {
					warn(fmt.Sprintf(
						"codex pane %s %s: %s — drop failed: %v",
						action.Socket, action.PaneID, action.Skip, err,
					))
				} else {
					changed = true
					warn(fmt.Sprintf(
						"codex pane %s %s: repaired — %s; binding dropped",
						action.Socket, action.PaneID, action.Skip,
					))
				}
			case action.Loud:
				warn(fmt.Sprintf(
					"codex pane %s %s: %s",
					action.Socket, action.PaneID, action.Skip,
				))
			}
			continue
		}
		if action.Skip != "" && action.Loud {
			warn(fmt.Sprintf(
				"codex pane %s %s: %s",
				action.Socket, action.PaneID, action.Skip,
			))
		}
		var target kill.Target
		if action.ClearKill != "" {
			var recorded bool
			target, recorded, err = manager.KillClearedCodex(ctx, action.ClearKill)
			if err != nil {
				warn(
					fmt.Sprintf(
						"codex pane %s %s: record clear kill (binding retained for retry): %v",
						action.Socket,
						action.PaneID,
						err,
					),
				)
				continue
			}
			if !recorded {
				warn(
					fmt.Sprintf(
						"codex pane %s %s: clear lineage %s unavailable; binding retained for retry",
						action.Socket,
						action.PaneID,
						action.ClearKill,
					),
				)
				continue
			}
			changed = true
		}
		_, moved, err := manager.AdvanceCodexPane(
			ctx, action.Socket, action.PaneID, action.Bind,
		)
		if err != nil {
			warn(fmt.Sprintf(
				"codex pane %s %s: advance binding: %v", action.Socket, action.PaneID, err,
			))
			continue
		}
		changed = changed || moved
		if action.ClearKill == "" {
			continue
		}
		// The name is stored per THREAD, so the cleared thread's own row is
		// the one that carries it. The lineage root is the fallback for a
		// cleared thread that was itself a resumed child: its row can be
		// absent while the root's is not.
		name := cxNames[action.ClearKill]
		if name == "" {
			name = cxNames[target.ID]
		}
		if name == "" {
			continue
		}
		warning, renameErr := spawn.RenameCodex(
			ctx, renamer, action.Socket, action.PaneID, name, spawn.Defaults(), spawn.Trace{},
		)
		if renameErr != nil {
			warn(
				fmt.Sprintf(
					"codex pane %s %s: re-apply chat name after clear: %v",
					action.Socket,
					action.PaneID,
					renameErr,
				),
			)
			continue
		}
		if warning != "" {
			warn(
				fmt.Sprintf(
					"codex pane %s %s: chat name was not re-applied after clear: %s",
					action.Socket,
					action.PaneID,
					warning,
				),
			)
			continue
		}
		// Record the rename pfm just performed, rather than waiting for it to
		// come back around through Codex's session index.
		//
		// Without this the pass leaves a trap it set itself. cx_names is only
		// ever refreshed by an index pass, so between the rename and the next
		// one the chat's name resolves to exactly ONE thread — the retired
		// pre-clear one. A pane that then loses its binding for any reason is
		// unbindable: a name may seed an unbound pane, but every thread that
		// name matches is retired, so nothing may seed it. It warns on every
		// refresh and never recovers. pfm authored this rename; it does not
		// need a mirror to tell it what it just did.
		if err := database.UpsertCxName(ctx, store.CxName{
			ID:         action.Bind,
			ThreadName: name,
			Source:     store.CxNameSourceSessionIndex,
			RenamedAt:  time.Now().UnixNano(),
		}); err != nil {
			warn(fmt.Sprintf(
				"codex pane %s %s: record re-applied chat name: %v",
				action.Socket, action.PaneID, err,
			))
		}
	}
	return changed
}

// ObserveCodexPanes captures every live Codex pane, pairs each with the
// binding pfm currently holds for it, and returns both the observations and
// the ruling DecideCodexPanes made over them. `pfm doctor` calls it for the
// observations alone, which is why it never writes: the health report must be
// able to describe this pass without performing it.
func ObserveCodexPanes(
	ctx context.Context,
	database *store.Store,
	manager *kill.Manager,
	capturer gather.PaneCapturer,
	live gather.Snapshot,
	runtime pfmconfig.Runtime,
	cxNames map[string]string,
	warn Warn,
) ([]CodexPaneObservation, []CodexPaneAction) {
	// A rollout held open by the pane's live Codex process is its current
	// conversation even when the TUI status line has already returned to the
	// display name. Compact/reset continuations can rotate that rollout without
	// restarting the process, which makes argv, inherited CODEX_THREAD_ID, and
	// an existing pane binding birth records rather than current identity.
	processThreads := make(map[string]string, len(live.Codex))
	processConflicts := make(map[string]bool)
	for _, process := range live.Codex {
		if process.IdentityError != "" {
			processConflicts[process.Socket+"\x00"+process.PaneID] = true
			warn(
				fmt.Sprintf(
					"codex pane %s %s: %s; binding not guessed",
					process.Socket,
					process.PaneID,
					process.IdentityError,
				),
			)
			continue
		}

		// Only a rollout the process itself has open right now may override
		// the screen. A resolver-derived identity (binding/argv/env guess,
		// RolloutHeld false) never enters this map, so it can never overrule
		// what the pane's own status line just said.
		if !process.RolloutHeld {
			continue
		}
		id := gather.CodexRolloutID(process.RolloutPath)
		if id == "" {
			continue
		}
		key := process.Socket + "\x00" + process.PaneID
		if previous := processThreads[key]; previous != "" && previous != id {
			processConflicts[key] = true
			warn(fmt.Sprintf(
				"codex pane %s %s: live processes hold conflicting rollouts %s and %s; binding not guessed",
				process.Socket, process.PaneID, previous, id,
			))
			continue
		}
		processThreads[key] = id
	}
	identities := gather.CaptureCodexIdentity(ctx, capturer, live.Panes)
	observations := make([]CodexPaneObservation, 0, len(identities))
	for _, identity := range identities {
		key := identity.Socket + "\x00" + identity.PaneID
		if processID := processThreads[key]; processID != "" && !processConflicts[key] {
			if identity.Failed {
				warn(fmt.Sprintf(
					"codex pane %s %s: capture failed, but its live process holds rollout %s",
					identity.Socket, identity.PaneID, processID,
				))
			} else if identity.ThreadID != "" && identity.ThreadID != processID {
				warn(fmt.Sprintf(
					"codex pane %s %s: status thread %s disagrees with live process rollout %s; using the live rollout",
					identity.Socket, identity.PaneID, identity.ThreadID, processID,
				))
			}
			identity.Name = ""
			identity.ThreadID = processID
			identity.Failed = false
		}
		if processConflicts[key] {
			identity.Failed = true
			identity.ThreadID = ""
			identity.Name = ""
		}
		observation := CodexPaneObservation{
			Socket: identity.Socket, PaneID: identity.PaneID,
			Name: identity.Name, ThreadID: identity.ThreadID, Failed: identity.Failed,
		}
		if !identity.Failed {
			bound, found, bindErr := manager.CodexPaneBinding(ctx, identity.Socket, identity.PaneID)
			if bindErr != nil {
				// A store read that failed is not an unbound pane. Treating it
				// as one would let a name seed a binding over a live one.
				warn(fmt.Sprintf(
					"codex pane %s %s: read binding: %v",
					identity.Socket, identity.PaneID, bindErr,
				))
				continue
			}
			if found {
				observation.Bound = bound
			}
		}
		observations = append(observations, observation)
	}
	return observations, DecideCodexPanes(
		observations, cxNames, CodexTitleThreads(ctx, runtime, warn),
		codexLineageRoots(ctx, database), codexRetiredThreads(ctx, database),
	)
}

// CodexTitleThreads builds the title index a Codex status-line NAME can move
// a binding through: exact thread title (threads.title, trimmed) to thread
// ids, sorted. It reads the same state-store lister
// store.NewCodexThreadResolverRoots already reads — store.CodexStateFiles then
// store.ReadCodexThreads, over the runtime's own Codex roots — never a second
// walker over the same stores. A lister failure is WARNed by name and leaves
// the index empty for this pass: an empty title index only means "no title
// can move anything this pass", and it never blocks the bare-id path this
// file's own header describes.
func CodexTitleThreads(
	ctx context.Context,
	runtime pfmconfig.Runtime,
	warn Warn,
) map[string][]string {
	titleThreads := make(map[string][]string)
	codexRoots := runtime.Paths.Roots[pfmengine.Codex]
	files := make([]string, 0, len(codexRoots))
	for _, codexRoot := range codexRoots {
		rootFiles, err := store.CodexStateFiles(codexRoot)
		if err != nil {
			warn(fmt.Sprintf(
				"codex pane reconcile: list Codex state store %q: %v", codexRoot, err,
			))
			continue
		}
		files = append(files, rootFiles...)
	}
	threads, err := store.ReadCodexThreads(ctx, files)
	if err != nil {
		warn(fmt.Sprintf("codex pane reconcile: read Codex state stores: %v", err))
		return titleThreads
	}
	for index := range threads {
		thread := threads[index]
		if thread.Title == "" {
			continue
		}
		titleThreads[thread.Title] = append(titleThreads[thread.Title], thread.ID)
	}
	for title := range titleThreads {
		sort.Strings(titleThreads[title])
	}
	return titleThreads
}

// codexRetiredThreads answers whether a thread was retired by a /clear,
// reading the kill table at most once per pass and only when asked.
//
// The "known" return is what keeps this honest. A kill table that could not be
// read answers (false, false), and every caller treats that as "do not act" —
// so a store outage can never be mistaken for proof that a thread is alive,
// nor let a live binding be dropped. Only a CLEAR retirement counts: an
// explicit `pfm chat kill` hides a chat that is still perfectly alive in its
// pane, and dropping that pane's binding would be wrong.
func codexRetiredThreads(ctx context.Context, database *store.Store) CodexThreadRetired {
	var (
		loaded  bool
		broken  bool
		records map[string]store.Killed
	)
	return func(id string) (bool, bool) {
		if id == "" {
			return false, false
		}
		if !loaded {
			loaded = true
			killed, err := database.KilledChats(ctx)
			if err != nil {
				broken = true
			} else {
				records = make(map[string]store.Killed, len(killed))
				for _, record := range killed {
					records[record.ID] = record
				}
			}
		}
		if broken {
			return false, false
		}
		record, found := records[id]
		return found && record.BaselinePrompts != nil, true
	}
}

// codexLineageRoots resolves thread ids to lineage roots, loading the rollout
// table at most once and only when a caller actually asks — the common pass
// moves no binding and never needs it.
//
// It returns "" for one reason only: the rollouts could not be read. A thread
// that is simply its own root answers with its own id. That distinction is
// load-bearing — DecideCodexPanes reads "" as "we failed to look" and refuses
// to call a clear on it.
func codexLineageRoots(ctx context.Context, database *store.Store) func(string) string {
	var (
		loaded bool
		broken bool
		roots  map[string]string
	)
	return func(id string) string {
		if id == "" {
			return ""
		}
		if !loaded {
			loaded = true
			rollouts, err := database.Rollouts(ctx)
			if err != nil {
				broken = true
			} else {
				_, roots = store.ResolveCodexLineages(rollouts)
			}
		}
		if broken {
			return ""
		}
		if root := roots[id]; root != "" {
			return root
		}
		return id
	}
}
