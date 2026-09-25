# harness-prompts — the fleet's system-prompt layer

One prompt per engine, composed from three parts, plus the Claude drift baseline.

## The composition

Every engine reads the same Professor: a shared head (identity, voice, stance, work rhythm),
an engine middle (the mechanics only that harness has), and a shared tail (orchestration and
the standing laws). `pfm install` joins the three at stage time — never at launch time — so
what an engine reads is one file on disk, not an assembly the launcher has to get right.

| part | file |
| --- | --- |
| head | `share/head.md` |
| middle | `claude/professor.md`, `codex/professor.md`, `opencode/professor.md` |
| tail | `share/tail.md` |

Each part's trailing newlines are trimmed and the three are joined by one blank line, the
result ending in a single newline. Nothing else is added: the staged file is the three parts
and the two seams.

## The three staged files

`pfm install` writes them under the managed root, beside the parts they were composed from:

- `harness-prompts/claude.md` — head + `claude/professor.md` + tail. `"systemPrompt": "professor"`
  makes every managed Claude launch inject it via `--system-prompt-file`, replacing the harness's
  built-in prose (tool schemas and CLAUDE.md are separate request lanes and are unaffected).
- `harness-prompts/codex.md` — head + `codex/professor.md` + tail. Codex takes only an appendix to
  its own prompt, so the composed file IS the appendix, delivered through `developer_instructions`
  in each configured Codex home's `config.toml`, inside a marked
  `# BEGIN pfm developer_instructions — installer-owned` / `# END pfm developer_instructions —
  installer-owned` fence that `pfm install` writes and owns; Codex reads the key as the first
  developer item of every thread and rebuilds it verbatim after compaction. A hand-written
  `developer_instructions` outside the fence is preserved untouched and the fleet prompt is not
  installed there. `pfm doctor`'s `codex developer_instructions=` row reports `ok`, `MISSING` or
  `CHECK FAILED` for each configured account. Full-history children inherit context; fresh/custom
  children need the coordination briefing specified in the appendix. These instructions guide tool
  selection; they do not remove the professor MCP's chat_* tools.
- `harness-prompts/opencode.md` — head + `opencode/professor.md` + tail. OpenCode has no
  system-prompt replacement flag; its machine-scope `opencode.jsonc` carries an `instructions`
  array of files whose content it appends to the system prompt, and `pfm install` names the staged
  file there, preserving every other key and every entry the operator wrote.

This tree is the ONLY copy. `harnessprompts.go` beside it embeds it into the binary at build
time (`//go:embed`), the installer composes and stages from that embedded tree, and a Go test
enforces that each staged file equals the composition of its three parts. Edit a part here, then
rebuild and install to deploy — and until you do, `pfm doctor`'s `harness-prompts embed=` row
hashes the binary's copy against this directory and reports `MISMATCH`, naming every file that
differs, so a binary carrying other prompts than this tree is never silent. The row states both
directions and prescribes neither: a hash difference says the two disagree, not which one is
newer — a clone checked out to an older revision than the binary is the binary being ahead.

## The Claude drift baseline

- `claude/baselines/harness-original-v2.1.280.md` and `claude/baselines/harness-opus-v2.1.280.md`
  are reviewed Sonnet and Opus built-in prompt baselines, captured in print mode with dynamic
  sections excluded. Each has a `.sha256` pin and `.model` provenance file under its
  `harness-original` or `harness-opus` stem; they are embedded with the parts and staged beside
  the composed prompts.
- `pfm doctor` checks both stable aliases, `sonnet` and `opus`, against their respective baselines.
  It records the requested alias, resolved model ID, CLI version, baseline filename, and original
  model ID. Model names and versions are informational: changing those alone never reports drift.
  A changed prompt behind an alias still requires review, even if the resolved model name also changed.
- Normalization masks `cc_version` only in the leading billing system block and removes only complete known
  model-identity and month/year knowledge-cutoff lines inside `# Environment`. On every line, fenced
  examples included, it then masks three token kinds: model IDs (`claude-opus-5-5`,
  `claude-haiku-4-5-20251001`; `claude-code` stays) to `<model-id>`, display names (`Opus 5.5`, `Claude 5`)
  to `<model-name>`, and dotted versions (`2.1.280`) to `<version>`. The model-catalog line
  (` - The most recent Claude models are … Model IDs — …`) is replaced whole by ` - <model-catalog>`,
  its trailing sentence included, so a catalog entry added or dropped is never drift; the line
  disappearing still is. A change in a masked token alone — a CLI release number — is never drift. Instructions appended to the
  identity lines, similar text elsewhere, fenced examples, and model-specific behavioral sections
  remain checked. Recognizing a text pattern alone is insufficient reason to discard it.
- `DRIFT` means normalized instruction text changed: review the upstream additions, deletions, or
  rewording before re-pinning. Below the verdict, doctor names each `section removed:`,
  `section changed:` and `section added:` heading (at most 20, then `… and N more`; when no section's
  text differs, `section order changed` or `blank lines changed (no section text differs)`), and a
  `model changed <alias>: <baseline> → <resolved>` line whenever the resolved model differs from
  `.model` — that line alone never warns. Failed capture and missing or inconsistent baseline files report
  separate coverage warnings; they never count as drift or a match. Failure of one model's check
  does not suppress the other. These checks do not validate the active chat, Fable, Codex, or the
  Professor replacement.

Captures use a localhost sink with dummy credentials that rejects the API request with HTTP 400;
no model inference occurs. Render system blocks with
`jq -r '.system | map(.text) | join("\n\n=== SYSTEM BLOCK ===\n\n")'` before normalization.
Re-pinning requires human review of instruction differences, followed by updating the prompt,
SHA256, and model provenance together. Never automatically accept a newly captured prompt.

`claude.systemPrompt` values: `production` (default — the CLI's own prompt, untouched), `lean` (the CLI's
built-in minimal prompt via `CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1`), `professor` (inject the staged
`harness-prompts/claude.md`).
