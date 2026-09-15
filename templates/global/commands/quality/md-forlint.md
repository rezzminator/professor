---
name: quality:md-forlint
description: Lint/format markdown — `check [path]` reports, `fmt [path]` rewrites, `prompt-safe <file>` keeps machine-read markers intact, `audit` prices the policy, `profile <path>` names a path's category. Route every markdown lint/format/style ask here; load before changing `.rumdl.toml`. Prose quality lives in /quality:prompt and /quality:doc.
argument-hint: "[check [path] | fmt [path] | prompt-safe <file> | audit | profile <path>]"
---

# Markdown lint & format

One config at the repo root (`.rumdl.toml`) decides every rule for every path; `rumdl` (single static binary, provisioned by `pfm install`, row in `pfm doctor`) is the only engine. This command owns the mechanics — whitespace, markers, tables, link integrity. Prose quality is `/quality:prompt` (prompts) and `/quality:doc` (reference docs).

## `check [path]` — report only

```bash
rumdl check .                    # default: the whole repo, policy applied per path
rumdl check <path> --output json
```

Changes nothing. Exit 1 when anything is reported.

## `fmt [path]` — rewrite in place

```bash
rumdl fmt --diff <path>     # preview
rumdl fmt <path>
rumdl fmt --check <path>    # CI gate: exit 1 if formatting would change anything
```

Two passes must produce identical bytes. Verify idempotence after any config change: format twice, diff the two outputs, expect no change.

## `audit` — price the policy before adopting it

Copy the tracked markdown into a scratch tree, format it there, and report the delta — never measure by formatting the live checkout:

```bash
W=$(mktemp -d); git ls-files '*.md' > /tmp/corpus.txt
tar -cf - -T /tmp/corpus.txt | (cd "$W" && tar -xf -); cp .rumdl.toml "$W/"
(cd "$W" && rumdl fmt . >/dev/null; git ls-files '*.md' 2>/dev/null)
# per-file: bytes/lines before vs after, and the word-level digest below
```

Report bytes, lines, files touched, and the word-damage count — in that order.

## The word-damage gate — the one check that matters

Formatting may change whitespace and markers; it must never change words. Compare the alphanumeric-token digest of every file before and after:

```bash
grep -o '[A-Za-z0-9]\+' FILE | sha256sum
```

A changed digest is damage until proven otherwise, and there is exactly one benign cause: MD029 renumbers a mis-numbered ordered list. Any other digest change is a rule rewriting content — read the diff, then add the rule to `unfixable` in `.rumdl.toml`.

## `profile <path>` — which category a path lands in

```bash
rumdl config -c .rumdl.toml get per-file-ignores
rumdl check -c .rumdl.toml <path>            # the applied policy, one path
```

The five categories and the reader each serves:

- prompt: `.claude/**`, `CLAUDE.md`, any shipped prompt template, and every injected prompt asset (`**/*.prompt.md`, an engine's staged prompts). An LLM reads it whole at runtime, so bytes are the cost. Exempt from the reader-facing rules (repeated headings, inline HTML, leading H1, link text) and from heading-level rewriting — a prompt's heading levels are addressing.
- doc: `docs/**`, engine specs, child-project docs. An agent greps, then reads one file. Table de-padding and link integrity (dangling relative links, dead anchors) earn the most here.
- public: `README.md`, `INSTALL.md`, `CHANGELOG.md`. Rendered for a person; repeated version headings and dated entries are the format, not a smell.
- generated: `AGENTS.md`, `.codex/**`, `.opencode/**`. Excluded. Format the Claude source and recompile (`pfm codex build .`, `build-opencode.mjs`); formatting a mirror is drift its own check will flag.
- record: `releases/**`, `.professor/**` ledgers, `**/testdata/**`. Excluded. Published history, machine-parsed ledgers, and byte-exact fixtures — a reformat is a falsified record or a red suite.

## What formatting buys — and what it does not

Buy it for reviewable diffs, mechanical consistency, and the token savings below. It does not improve comprehension: measured across three arms of one word-identical prompt — as-is, formatted, deliberately mangled — quality differed by 1.00 point against 34–45 points of draw-to-draw noise (p = 0.91), with zero schema failures in any arm. State the hygiene and cost case; leave comprehension claims out.

Document STRUCTURE — heading hierarchy, explicit delimiters, section order — is a separate question with real measured effects, and a formatter does not touch it.

## Never wrap

Reflow collapses each paragraph onto ONE line (`reflow-mode = "normalize"`, `line-length` past any real line). Measured over one framework's 23 prompt files: collapse −242 bytes / −161 lines; sentence-per-line +1,048 / +690; wrap at 100 columns +2,289 / +1,602. A wrapped paragraph pays a newline token per line and reflows its whole tail on a one-word edit. Collapse is also the cheapest diff: one edited paragraph is one changed line.

The tokens are in the tables, not the prose: stripping column padding (`MD060.style = "compact"`) took 7.4% off one `docs/` tree. Prose already written one-paragraph-per-line has nothing left to give — the gain there is consistency and the check rules.

## `prompt-safe <file>` — machine-read markers

A prompt template holds lines that code parses, not prose: layer sentinels, template slots, untrusted-data fences, and line-anchored ledger bullets. A marker sitting directly against the next line of prose is ONE CommonMark paragraph, so a reflowing formatter correctly merges the two and destroys the marker.

1. Fix the source: a blank line above and below every machine-read marker. That is the fix — not a formatter flag, not an exemption, not a wrapper.
2. Format. Stock `rumdl fmt` then preserves every marker and stays idempotent.
3. Prove the consumer still parses it. Run the file through its real reader and assert the markers survive, alone and in order.

Trailing whitespace is load-bearing here: two or more trailing spaces is CommonMark's hard line break, which `MD009.br-spaces = 2` preserves while stripping one or three. Stripping a hard break is a style decision, not noise removal.

Step 3 is the one that matters. A merged sentinel fails loudly; a dissolved untrusted-data fence leaves a file that still renders and still runs with its injection guard gone. Every guard test here carries a negative control — a known-broken file it must still reject — so a suite that stopped testing anything fails instead of passing.

## When the gate itself is broken

- Run rumdl FROM the repo root. `[per-file-ignores]` globs resolve against the current directory, not against the config's own location: with `-c /repo/.rumdl.toml` from elsewhere, every category exemption silently fails to match and a prompt file gets judged as a doc.
- An unknown rule option does NOT fail the run: rumdl prints `Using default values for rule MDxxx` on stderr and formats with that rule's default, so a typo in a config value silently pads every table it was told to compact. Validate the config before trusting any run: `rumdl config -c .rumdl.toml --no-defaults --output toml 2>&1 | grep -iE 'invalid|using default'` must print nothing.
- Results are cached in `.rumdl_cache/` (gitignored). A repeated run answers from cache: `rumdl clean` before any measurement whose number you will report.
- A rule that never fires proves nothing. Any new gate is watched failing on a purpose-built control file first, then watched passing on the real tree.

## The hook

`format-md.sh` (PostToolUse on Edit/Write, Professor-owned paths only) runs `rumdl fmt` on the one file just written, so hand formatting is rarely needed. It exits 0 for every path it does not own, and says so on stderr when `rumdl` is missing rather than skipping silently. A Bash-driven write bypasses the hook — run `rumdl fmt <file>` yourself after one.

## Changing the policy

`.rumdl.toml` is a prompt-adjacent framework file: it routes through `/pcm`, and its twin ships as `templates/project/rumdl-policy.toml` (scaffolded to an adopter's `.rumdl.toml`; the shipped copy must NOT be named `rumdl.toml`, a name rumdl discovers as the governing config for everything beneath it). Every added or removed rule arrives with the three numbers from `audit` — bytes, lines, word-damage — and the control file that proves the new rule fires.
