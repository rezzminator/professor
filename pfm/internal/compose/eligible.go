package compose

// defaultEligible answers whether a row belongs in the default listing.
//
// THE KILL IS TESTED FIRST, before any liveness short-circuit: killed is dead,
// live or not. A split row is the one row with no single id to kill — compose
// never marks it — so it short-circuits above the test rather than around it.
func defaultEligible(row Row) bool {
	if row.Killed {
		return false
	}
	if row.BackendError != "" {
		return !row.BG
	}
	if row.Kind == LiveSplit {
		return true
	}
	// A live agent is exempt from the emptiness tests below and from those
	// ALONE: it is rowed from its running process, so its transcript is often
	// still zero bytes with no prompt parsed out of it.
	if row.Kind == Agent {
		return true
	}
	// A booting row has no transcript at all — it exists BECAUSE the crumb
	// that would normally lead compose to one has not been written yet — so
	// the emptiness test below would suppress every one of them from the
	// default view and silently defeat this entire fix.
	if row.Kind == Booting {
		return true
	}
	// A live Codex row is exempt from the SIZE/PROMPT half of the test below
	// for the same reason: Codex >=0.146.1 keeps a paginated thread's content
	// in its own sqlite state store, so the rollout file gather and the index
	// parse from can carry prompt_count=0 and size=0 even while the chat is
	// genuinely running in tmux right now. The index enriches such a row from
	// the state store once it catches up (applyCodexThread); until then, a
	// row this self-evidently real must never be judged by a test built to
	// catch an abandoned zero-byte spawn.
	//
	// The BG half is NOT waived: a machine-spawned thread (codex exec, never
	// renamed) caught live in a pane is still background work, not a chat,
	// exactly like its resume-shape twin — the exemption is for genuinely
	// empty content, never for who started the conversation.
	// A live Claude seat before its first prompt (a `pfm chat branch` fork)
	// has a crumb but no transcript yet: the same exemption, the same BG half.
	if row.Kind == LiveCodex || row.Kind == LiveClaude {
		return !row.BG
	}
	// A LIVE OpenCode row is exempt for the same reason and one stronger: a
	// seat no indexed session could be pinned to carries NO counters at all
	// (liveOpenCodeRows), so every emptiness test below reads a running TUI
	// the user is typing into as an abandoned spawn. The whole point of this
	// Kind is that such a chat stops being reported as absent.
	if row.Kind == LiveOpenCode {
		return !row.BG
	}
	// An OpenCode session has no file size at all — it lives entirely inside
	// its engine's SQLite store, so the size half of this test would suppress
	// every one of them forever. Its reality signal is prompts AND an answer:
	// a session with no admitted input was opened and never used, and one
	// with prompts but zero assistant messages was opened and never
	// answered — exactly as empty as a Claude transcript with no visible
	// turns. The displayed prompt count is never fudged to fake either case.
	if row.Kind == ResumeOpenCode {
		return !row.BG && row.PromptCount > 0 && row.AssistantCount > 0
	}
	// A resumable Claude transcript with a file but no parsed prompts is a
	// spawn that was never used, so the default view suppresses it on purpose.
	// The all view remains the way to reach that row.
	return !row.BG && row.Size > 0 && row.PromptCount > 0
}

func visibleInView(row Row, view View) bool {
	switch view {
	case AllView:
		return true
	case KilledView:
		return row.Killed
	default:
		return defaultEligible(row)
	}
}

func defaultEligibleCount(rows []Row) int {
	count := 0
	for index := range rows {
		row := rows[index]
		if defaultEligible(row) {
			count++
		}
	}
	return count
}
