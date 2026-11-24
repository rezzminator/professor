# Composer transport boundaries

The measured sizes `ClaudeInlineMax` and `CodexInlineMax` rest on: where each engine's composer collapses typed or pasted text into a paste block, and what pfm does on each side of that edge.

The current boundaries are `ClaudeInlineMax=720` and `CodexInlineMax=900` in `types.go`. Fixture tests check pfm's transport choice and delivery receipts; actual engine composer thresholds remain UNPLAYED. An engine-side probe uses a scratch working directory and a fresh `probe-*` tmux socket, sends payloads with distinct head/tail markers, compares the transcript or rollout with pane capture, and checks `#{pane_dead}`. A failure edge is the first payload that collapses into a paste block and fails to reach the transcript or rollout on one Enter.

| engine | transport | boundary input | next size | composer symptom to probe |
| ------ | --------------------------- | ------------------------- | ------------- | -------------------------------------------------------------------- |
| Claude | literal `send-keys -l` | 1,024 chars | 1,025 chars | `[Pasted text #N]`; one Enter left the block in the composer |
| Claude | bracketed `paste-buffer -p` | 800 chars | 801 chars | `[Pasted text #N]`; one Enter left the block in the composer |
| Codex | literal `send-keys -l` | 1,000 chars | 1,001 chars | `[Pasted Content N chars]`; one Enter left the block in the composer |
| Codex | bracketed `paste-buffer -p` | 1,000 chars | 1,001 chars | `[Pasted Content N chars]`; one Enter left the block in the composer |

The per-engine inline boundary uses the smaller transport edge and rounds down at 90%: Claude `floor(801 × 0.9) = 720` runes; Codex `floor(1001 × 0.9) = 900` runes. The comparison is against the complete signed wire message, so a signature consumes part of the safety margin.

On live delivery this is the inline-`SendLiteral`-vs-bracketed-`SendPaste` boundary: a message over it reaches the pane through tmux's bracketed paste (`load-buffer` + `paste-buffer -p`), checked by a tail match in the capture or the fixture composer's collapsed-paste placeholder (`[Pasted text #N]` / `[Pasted Content N chars]`). The auto-file store (`~/.local/state/pfm/call-bodies`, mode 0600, pruned past seven days) is a rescue when paste delivery cannot be proven, reported through the receipt and `Result.AutoFilePath`. On the dormant/resume path (`PrepareForResume` writes into a transcript, with no live composer) the boundary remains inline-vs-file: an 8 KiB body becomes a byte-exact pointer. Slash commands travel in locked 512-rune literal chunks, with Enter after the final chunk; the `/compact` fixture checks the complete command.
