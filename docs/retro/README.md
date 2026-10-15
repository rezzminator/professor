# Retro — the steering ledger

A session writes one entry per lesson when a steering correction or a run's friction shows something a framework file should have said; `/pcm retro [owner]` folds them. Entries are records: they keep their date and story.

## Where an entry goes

`docs/retro/{owner}/{YYYY-MM-DD}-{slug}.md`, one lesson per file.

- Owner: the agent, command, skill or mechanism whose file the fix lands in, spelled as its folder — `flights` for the `flights-*` agents and `/flights:*`, `claude-md` for root `CLAUDE.md`, `fleet-prompt` for `pfm/harness-prompts/`, `pfm` for the Go engine.
- A lesson naming several owners lives with the first.
- `ls docs/retro/` lists the owners; a new owner is a new folder.

## Entry

```markdown
# {YYYY-MM-DD} — {one-line subject}

Observed: what actually happened, concretely.

Amend: {file}#{section} — what the file should say instead, or `judgment` when no text fix applies.
```

Each field is its own paragraph, since the markdown formatter joins adjacent lines. An entry with no `Resolved:` line is open: `grep -L '^Resolved:' docs/retro/*/*.md` lists the queue.

## Fold

`/pcm retro [owner]` folds each open entry's `Amend:` and appends `Resolved: {YYYY-MM-DD} — {where it landed}` as the entry's last paragraph; the entry stays as the record.
