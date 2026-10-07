package fleet

import (
	"regexp"
	"sort"
)

// ChatIDPattern matches one chat id: a Claude session UUID or a Codex thread
// UUID, in the canonical 8-4-4-4-12 form.
var ChatIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// A Codex pane's identity has one trustworthy source and one treacherous one.
//
// The trustworthy source is a bare thread id on the pane's own status line.
// Codex renders the raw id while a thread is UNNAMED. A named /clear or a
// quickly auto-titled successor can skip that frame; the forward-name path
// below handles those observations without assuming an unnamed interval.
//
// The treacherous source is the display NAME. It is a lagging mirror of
// Codex's own index: after a clear pfm re-applies the chat's name to the new
// thread, and until that rename reaches cx_names the name still resolves to
// exactly ONE thread — the dead pre-clear one. A pass that let a name move a
// binding onto whatever it resolves to, unconditionally, therefore walked the
// pane BACKWARD onto the thread the clear had just killed, and then
// clear-killed the live thread that had replaced it. Both panes named
// ENGINE_BUILDER in a real fleet ended up bound to one already-killed thread
// that way, and `pfm chat resolve` answered with the corpse.
//
// A human who clears and keeps typing before the reconcile pass catches the
// bare id is a second gap in the same shape: Codex titles the new thread from
// the first reply, the status line shows that title before pfm ever sees the
// id, and a NAME-only rule that never moves a binding leaves that pane stuck
// on the dead thread forever. Closing it safely means a name may move a
// binding too — but only in the one direction a lagging index can never fake:
// FORWARD, onto a thread strictly newer than the one it would replace. The
// dead pre-clear thread from the paragraph above is never newer than what
// replaced it, so the backward walk stays impossible.
//
// Hence the law this file exists to enforce:
//
//	A NAME MAY SEED AN UNBOUND PANE. A NAME MAY MOVE A BINDING ONLY FORWARD —
//	ONTO A SINGLE, UNCLAIMED, STRICTLY NEWER, DIFFERENTLY-ROOTED THREAD.
//	NOTHING EVER MOVES A BINDING BACKWARD.
//
// A name can confirm a binding, seed one, or advance one forward. It can
// never walk one backward — that is the ENGINE_BUILDER failure, closed for
// good.
//
// DecideCodexPanes is deliberately pure: no tmux, no store, no clock. The
// reconcile pass and `pfm doctor` both run it over the same observations, so
// the health report cannot disagree with the decision it is reporting on.

// The reasons one pass declined to act on a pane. They are constants because
// two surfaces quote them — the reconcile pass on stderr and the doctor
// table — and a diagnostic that paraphrases the decision is a second source
// of truth.
const (
	CodexPaneCaptureFailed    = "capture failed (the pane was not read; this is not an idle pane)"
	CodexPaneNoThreadNamed    = "status line named no thread"
	CodexPaneNameUnknown      = "status line name matches no indexed thread"
	CodexPaneNameAmbiguous    = "status line name matches several threads"
	CodexPaneNameTaken        = "status line name matches a thread another pane is already bound to"
	CodexPaneNameCannotMove   = "status line shows a name that resolves to no newer, differently-rooted thread; a name never moves a binding backwards"
	CodexPaneLineageUnknown   = "lineage could not be read, so a clear cannot be told from a resume"
	CodexPaneSameLineage      = "the new thread continues the bound thread's lineage; a resume is not a clear"
	CodexPaneBindingContested = "another pane was bound to this pane's own thread; that binding was stale"
	CodexPaneBindingRetired   = "the bound thread was retired by a /clear, so this binding is impossible"
	CodexPaneNameRetired      = "status line name matches only threads a /clear already retired"
)

// CodexThreadRetired reports whether a thread was retired by a /clear, and
// whether that could be determined AT ALL. The second return is the whole
// point: a kill table that could not be read must never let a live binding be
// dropped, because "we failed to look" is not "this thread is dead".
type CodexThreadRetired func(id string) (retired, known bool)

// CodexPaneObservation is one live Codex pane as a reconcile pass found it:
// what the pane's own status line said, and what pfm believed it was running.
type CodexPaneObservation struct {
	Socket   string
	PaneID   string
	Name     string
	ThreadID string
	// Failed records that the capture itself did not run. It is NOT "the pane
	// named no thread": one says we failed to look, the other says we looked
	// and the screen was silent, and a kill decision must never confuse them.
	Failed bool
	// Bound is the thread pfm currently believes this pane runs, empty when
	// the pane has no binding yet.
	Bound string
}

// CodexPaneAction is what one pass decided about one pane. Exactly one of
// Bind and Skip is set; ClearKill only ever accompanies a Bind.
type CodexPaneAction struct {
	Socket   string
	PaneID   string
	Observed CodexPaneObservation
	// Bind is the thread this pane must be bound to.
	Bind string
	// ClearKill is the thread a /clear just replaced — the one to retire with
	// a prompt baseline. Empty on a seed, because a pane pfm was not already
	// following cannot have been observed to clear.
	ClearKill string
	// BindRoot is the lineage root of Bind, set beside ClearKill: the id the
	// chat's row carries once the pane moved, where the reminders of the
	// cleared chat follow it.
	BindRoot string
	// Skip names why the pane was left alone. Never empty when Bind is.
	Skip string
	// Forget erases this pane's binding outright. It is the repair for a
	// binding that cannot possibly be true and would otherwise never heal.
	Forget bool
	// Loud marks a skip that must reach stderr on this pass rather than
	// waiting for someone to run `pfm doctor`. Ordinary, self-healing states
	// (a name lagging one index refresh behind a rename) are quiet; a pane pfm
	// structurally cannot follow is not.
	Loud bool
}

// DecideCodexPanes rules on every observed pane at once. It must see the whole
// pass, not one pane at a time: whether a name may seed a pane depends on what
// every OTHER pane is already bound to, and a per-pane loop cannot know that.
//
// cxNames maps thread id to display name (the shape store.CxNames returns).
// titleThreads maps a Codex thread's own title to the thread ids that carry
// it exactly (the shape ObserveCodexPanes builds from the state store); it is
// merged into the same name index AFTER cxNames, so a title-only match is
// exactly as good as a cx_names match once merged. A name can now move a
// binding, not just confirm or seed one — forward only, see decideCodexPane.
// lineageRoot returns the lineage root of a thread id, or "" when the lineage
// could not be read at all — "" therefore means "we failed to look" and never
// "no lineage", so a failed read can never be mistaken for a clear.
func DecideCodexPanes(
	observations []CodexPaneObservation,
	cxNames map[string]string,
	titleThreads map[string][]string,
	lineageRoot func(string) string,
	retired CodexThreadRetired,
) []CodexPaneAction {
	// A binding pointing at a thread a /clear already retired is not merely
	// suspicious, it is IMPOSSIBLE: the clear that retired it is the same
	// event that moved the pane onto its replacement. A fleet that reached
	// that state is following the pane into a chat nobody is in, and cannot
	// recover through the forward-name-move law below either — the pane shows
	// a NAME from then on, and the only thread that name can resolve to here
	// is the one just retired, which is never newer than what would replace
	// it. So the impossible binding is dropped here, which frees the pane to
	// be re-seated from its own screen.
	dropped := make(map[string]bool, len(observations))
	for index, observation := range observations {
		if observation.Failed || observation.Bound == "" {
			continue
		}
		if isRetired, known := retired(observation.Bound); known && isRetired {
			dropped[observation.Socket+"\x00"+observation.PaneID] = true
			observations[index].Bound = ""
		}
	}
	threadsByName := make(map[string][]string, len(cxNames))
	for id, name := range cxNames {
		if name == "" {
			continue
		}
		threadsByName[name] = append(threadsByName[name], id)
	}
	for title, ids := range titleThreads {
		if title == "" {
			continue
		}
		for _, id := range ids {
			if id == "" {
				continue
			}
			duplicate := false
			for _, existing := range threadsByName[title] {
				if existing == id {
					duplicate = true
					break
				}
			}
			if !duplicate {
				threadsByName[title] = append(threadsByName[title], id)
			}
		}
	}
	for name := range threadsByName {
		sort.Strings(threadsByName[name])
	}

	// Every thread a pane is ALREADY bound to is claimed for this pass. A name
	// may not seed a second pane onto a claimed thread: two panes are two
	// chats, and one thread cannot be running in both.
	claimedBy := make(map[string]string, len(observations))
	for _, observation := range observations {
		if observation.Failed || observation.Bound == "" {
			continue
		}
		claimedBy[observation.Bound] = observation.Socket + "\x00" + observation.PaneID
	}

	// Ruling in a stable order matters: two unbound panes carrying the same
	// display name are decided by which one is looked at first, and a fleet
	// that reshuffles its own answer every pass would hand the thread back and
	// forth forever.
	ordered := make([]CodexPaneObservation, len(observations))
	copy(ordered, observations)
	sort.Slice(ordered, func(first, second int) bool {
		if ordered[first].Socket != ordered[second].Socket {
			return ordered[first].Socket < ordered[second].Socket
		}
		return ordered[first].PaneID < ordered[second].PaneID
	})

	byPane := make(map[string]CodexPaneAction, len(ordered))
	for _, observation := range ordered {
		action := decideCodexPane(observation, threadsByName, claimedBy, lineageRoot, retired)
		if dropped[observation.Socket+"\x00"+observation.PaneID] {
			// Say it either way, but only ERASE the key when nothing replaced
			// it: a fresh Bind overwrites the same key, and deleting it after
			// would undo the repair.
			action.Skip, action.Loud = CodexPaneBindingRetired, true
			action.Forget = action.Bind == ""
		}
		// A binding handed out in THIS pass claims its thread too. Without
		// this, two unbound panes sharing one display name both seed onto the
		// same thread — which is the state a real fleet was found in.
		if action.Bind != "" {
			claimedBy[action.Bind] = observation.Socket + "\x00" + observation.PaneID
		}
		byPane[observation.Socket+"\x00"+observation.PaneID] = action
	}

	// Return in the caller's original order; only the ruling order is sorted.
	actions := make([]CodexPaneAction, 0, len(observations))
	for _, observation := range observations {
		actions = append(actions, byPane[observation.Socket+"\x00"+observation.PaneID])
	}
	return actions
}

func decideCodexPane(
	observation CodexPaneObservation,
	threadsByName map[string][]string,
	claimedBy map[string]string,
	lineageRoot func(string) string,
	retired CodexThreadRetired,
) CodexPaneAction {
	action := CodexPaneAction{
		Socket: observation.Socket, PaneID: observation.PaneID, Observed: observation,
	}
	self := observation.Socket + "\x00" + observation.PaneID

	if observation.Failed {
		action.Skip, action.Loud = CodexPaneCaptureFailed, true
		return action
	}

	// The pane named its own thread outright, without a lagging title lookup.
	if observation.ThreadID != "" {
		if observation.ThreadID == observation.Bound {
			return action
		}
		action.Bind = observation.ThreadID
		// A pane claiming a thread another pane is bound to means that other
		// binding is stale — the pane's own screen outranks it — but a fleet
		// that reached that state has already mis-followed something, so say so.
		if owner, taken := claimedBy[observation.ThreadID]; taken && owner != self {
			action.Skip, action.Loud = CodexPaneBindingContested, true
			return action
		}
		if observation.Bound == "" {
			// Seeding a pane pfm was not following. Nothing was observed to be
			// replaced, so nothing is retired.
			return action
		}
		previousRoot, currentRoot := lineageRoot(observation.Bound), lineageRoot(observation.ThreadID)
		if previousRoot == "" || currentRoot == "" {
			// Keep the old binding until retirement can be decided. Advancing
			// here would forget the only evidence of the missed clear forever.
			action.Bind = ""
			action.Skip, action.Loud = CodexPaneLineageUnknown, true
			return action
		}
		if previousRoot == currentRoot {
			// Codex resumes and forks land a CHILD rollout in the same pane.
			// Retiring the lineage root there would hide the live chat itself.
			action.Skip = CodexPaneSameLineage
			return action
		}
		action.ClearKill, action.BindRoot = observation.Bound, currentRoot
		return action
	}

	if observation.Name == "" {
		action.Skip = CodexPaneNoThreadNamed
		// A pane whose screen never names a thread is a pane pfm can never
		// follow through a clear. That is a standing blind spot, not a quiet
		// no-op — but it is also what an ordinary modal, picker, or startup
		// frame looks like for one pass, so the doctor carries it, not stderr.
		return action
	}

	matches := threadsByName[observation.Name]
	if observation.Bound != "" {
		for _, match := range matches {
			if match == observation.Bound {
				// The name confirms what pfm already believed. Nothing to do,
				// nothing to report.
				return action
			}
		}
		// The name resolves elsewhere — or nowhere. It may still move the
		// binding FORWARD, the one direction a lagging index can never fake:
		// exactly one candidate, unclaimed, provably born after the current
		// binding, in a different lineage. Anything less certain leaves the
		// binding exactly where it was — this is the exact input that used to
		// walk a pane backwards onto a dead thread.
		if len(matches) != 1 {
			action.Skip = CodexPaneNameCannotMove
			return action
		}
		target := matches[0]
		if owner, taken := claimedBy[target]; taken && owner != self {
			action.Skip, action.Loud = CodexPaneNameTaken, true
			return action
		}
		previousRoot, currentRoot := lineageRoot(observation.Bound), lineageRoot(target)
		if previousRoot == "" || currentRoot == "" {
			// The name still might be right, but a forward move that kills the
			// old binding must never run on a guess.
			action.Skip, action.Loud = CodexPaneLineageUnknown, true
			return action
		}
		if previousRoot == currentRoot {
			// Same lineage: a resume or fork, not a clear. The name did not
			// witness a new chat, so it must not act like one.
			action.Skip = CodexPaneSameLineage
			return action
		}
		if !codexThreadNewer(target, observation.Bound) {
			// The exact input that once walked a pane backwards onto a dead
			// thread: the name resolves to a real, differently-rooted thread,
			// but not one born after the binding it would replace.
			action.Skip = CodexPaneNameCannotMove
			return action
		}
		action.Bind = target
		action.ClearKill, action.BindRoot = observation.Bound, currentRoot
		return action
	}

	free := make([]string, 0, len(matches))
	retiredMatches := 0
	for _, match := range matches {
		if owner, taken := claimedBy[match]; taken && owner != self {
			continue
		}
		// Seeding onto a retired thread would immediately re-create the
		// impossible binding the drop above just repaired, once per pass,
		// forever.
		if isRetired, known := retired(match); known && isRetired {
			retiredMatches++
			continue
		}
		free = append(free, match)
	}
	switch {
	case len(matches) != 0 && len(free) == 0 && retiredMatches != 0:
		// This is a standing structural condition, not an event, and it
		// cannot self-heal: Codex renders a bare thread id only while a
		// thread is UNNAMED, so a named pane can never reveal the id that
		// would re-seat it, and its name resolves only to threads a /clear
		// already retired. There is no operator action available on THIS
		// pass — `pfm doctor` already carries it as codex_pane=unfollowable
		// with this same reason string. Making this Loud would print an
		// identical, unactionable line on every reconcile pass of every pfm
		// invocation, forever — precisely the "warning on every pass for an
		// ordinary one-refresh lag" this file's own header warns against.
		action.Skip = CodexPaneNameRetired
	case len(matches) == 0:
		// Ordinary and self-healing: a chat named a moment ago is on screen
		// before Codex's index has been re-read. It is quiet HERE because this
		// runs behind an interactive picker on every refresh — but it is a
		// pane pfm cannot follow through a clear while it lasts, so `pfm
		// doctor` names it. Quiet on the hot path is not the same as invisible.
		action.Skip = CodexPaneNameUnknown
	case len(free) == 0:
		action.Skip, action.Loud = CodexPaneNameTaken, true
	case len(free) == 1:
		action.Bind = free[0]
	default:
		action.Skip = CodexPaneNameAmbiguous
	}
	return action
}

// codexThreadNewer reports whether a was born strictly after b, provably —
// never a guess. Codex thread ids are UUIDv7: the version nibble at index 14
// is '7', and the first 48 bits (the first 13 characters, "xxxxxxxx-xxxx")
// are a millisecond creation timestamp, so once both ids are confirmed v7 a
// lexical compare of those 13 characters is a creation-order compare. Either
// id failing ChatIDPattern, or failing the version check, answers false: a
// name that cannot prove it is newer is never allowed to move a binding.
func codexThreadNewer(a, b string) bool {
	if !ChatIDPattern.MatchString(a) || !ChatIDPattern.MatchString(b) {
		return false
	}
	if a[14] != '7' || b[14] != '7' {
		return false
	}
	return a[:13] > b[:13]
}
