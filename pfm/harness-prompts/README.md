# harness-prompts — the fleet's system-prompt layer

One prompt per engine, assembled at install from three source parts, plus the Claude drift baseline.

## The assembly

Every engine reads the same Professor: a shared head (identity, voice, stance, work rhythm),
an engine middle (the mechanics only that harness has), and a shared tail (orchestration and
the standing laws). `pfm install` joins the three from the parts embedded in the binary and
writes one file per engine, so what an engine reads is one file on disk, not an assembly the
launcher has to get right. Nothing is assembled at build time and no output is tracked.

| part | file |
| --- | --- |
| head | `share/head.md` |
| middle | `claude/professor.md`, `codex/professor.md`, `opencode/professor.md` |
| tail | `share/tail.md` |

Each part's trailing newlines are trimmed and the three are joined by one blank line, the
result ending in a single newline. Nothing else is added: a generated file is the three parts
and the two seams. The Codex mapping is applied after assembly, to the full Codex prompt rather than the middle alone; the
in-memory Codex fleet prompt that agent roles carry (`codexprompt.FleetPrompt`) is that same
assembly and mapping from the same source parts.

## The three generated files

`pfm install` writes the files below under `{clone}/pfm/harness-prompts/generated/` before any
consumer runs; the directory is gitignored and never embedded. The assembly tests compare each
output with the expected join of the source parts, and the install lifecycle tests prove the files
exist after install.

- `generated/claude.md` — head + `claude/professor.md` + tail. `"systemPrompt": "professor"`
  makes every managed Claude launch inject it via `--system-prompt-file`, replacing the harness's
  built-in prose (tool schemas and CLAUDE.md are separate request lanes and are unaffected).
- `generated/codex.md` — head + `codex/professor.md` + tail. Codex takes only an appendix to
  its own prompt, so `generated/codex.md` IS the appendix, which `pfm install` reads into `developer_instructions`
  in each configured Codex home's `config.toml`, inside a marked
  `# BEGIN pfm developer_instructions — installer-owned` / `# END pfm developer_instructions —
  installer-owned` fence that `pfm install` writes and owns; Codex reads the key as the first
  developer item of every thread and rebuilds it verbatim after compaction. A hand-written
  `developer_instructions` outside the fence is preserved untouched and the fleet prompt is not
  installed there. `pfm doctor`'s `codex developer_instructions=` row reports `ok`, `MISSING`,
  `CHECK FAILED` and related states (e.g. `no-accounts`, `MISMATCH`) for each configured account. Full-history children inherit context; fresh/partial-history
  children whose role or custom instruction replaces the prompt need the native coordination and waiting
  rules plus the two orchestration sections in their briefing. These instructions guide tool
  selection; they do not remove the professor MCP's chat_* tools.
  Ordinary child completions queue mailbox messages with `trigger_turn=false`; they do not start an idle
  parent turn. The Codex appendix keeps the parent turn active and calls `wait_agent` with its configured
  default timeout while delegated work remains, waiting again after progress or a timeout and reporting
  timeouts in commentary. The shared tail defers waiting to each engine; Claude and OpenCode keep
  their engine-specific end-turn behavior. Rebuild pfm and run `pfm install` to regenerate and deploy prompt
  changes; already-running chats need to reload their instructions.
- `generated/opencode.md` — head + `opencode/professor.md` + tail. OpenCode has no
  system-prompt replacement flag; its machine-scope `opencode.jsonc` carries an `instructions`
  array of files whose content it appends to the system prompt, and `pfm install` names the clone's
  `generated/opencode.md` there, preserving every other key and every entry the operator wrote.

The clone holds the source parts, the generated prompts and the Claude baselines; install stages
no copy of them. `harnessprompts.go` embeds the source parts and baselines into the binary for
assembly and the doctor's `harness-prompts embed=` comparison. That row compares the source tree
and excludes the `generated/` outputs;
it reports `MISMATCH` when an embedded source differs from the clone. The row states both
directions and prescribes neither: a hash difference says the two disagree, not which one is
newer — a clone checked out to an older revision than the binary is the binary being ahead.

## The Claude drift baseline

- `claude/baselines/harness-original-v2.1.300.md` and `claude/baselines/harness-opus-v2.1.300.md`
  are reviewed Sonnet and Opus built-in prompt baselines, captured in print mode with dynamic
  sections excluded. These are frozen captures: no test or suite re-captures or re-verifies them
  against a real Claude CLI. The only live capture is `pfm doctor` on a host; re-pinning follows
  the human review described below. Each has a `.sha256` pin and `.model` provenance file under its
  `harness-original` or `harness-opus` stem; the doctor reads them from the recorded clone.
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
no model inference occurs. Each runs with `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, so remote
feature flags keep their shipped defaults: fetched afresh into each throwaway config dir, they
would switch prompt variants between identical captures. A flag-gated variant a networked session
receives is therefore outside the baseline; review the CLI's gated prompt text when re-pinning. Render system blocks with
`jq -r '.system | map(.text) | join("\n\n=== SYSTEM BLOCK ===\n\n")'` before normalization.
Re-pinning requires human review of instruction differences, followed by updating the prompt,
SHA256, and model provenance together. Never automatically accept a newly captured prompt.
`pfm doctor --verbose` retains each successful request as a private
`harness-prompt-<alias>.request.json` in its printed verbose directory for that review.

`claude.systemPrompt` values: `production` (default — the CLI's own prompt, untouched), `lean` (the CLI's
built-in minimal prompt via `CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1`), `professor` (inject the clone's
`generated/claude.md`).
