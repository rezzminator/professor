# /quality:claude-md

`/quality:claude-md` is the law for a project's orientation file — `CLAUDE.md`, `AGENTS.md`, `GEMINI.md` or any engine's equivalent: the one text every session and every sub-agent reads before anything else. The law itself — spine, admission, budget, checks, write steps, the truth script — lives in `templates/global/commands/quality/claude-md.md`; this doc holds why each part is shaped as it is, the evidence behind it, and the decisions taken. Any project, any stack.

## Contents

- [What it is for](#what-it-is-for)
- [The two metrics](#the-two-metrics)
- [Evidence](#evidence)
- [Engine facts](#engine-facts)
- [Why each part](#why-each-part)
- [The truth script](#the-truth-script)
- [Relations](#relations)
- [Decisions](#decisions)

## What it is for

The orientation file is paid on every load: each main session and each sub-agent spawn reads it whole before its first tool call, so one line costs its bytes times every load for the life of the project. And it is the reader's only map: when it cannot say where a thing lives or how to run it, every reader spends round-trips — `ls`, grep, open, reject — rediscovering the same fact. The law designs for both at once.

## The two metrics

- Compact: bytes per load, measured with `wc -c` against the budget.
- Useful: round-trips from a spoken term to its source. Target one — the reader finds the term in the file, and its next call opens the source. Measured over a sample of real asks (commit subjects, session transcripts): the reads spent before the first read of the right source file, the orientation-hops count `/quality:llm-codebase` defines.

Every rule serves one of the two. A rule that serves neither is cut.

## Evidence

Each item is labeled by strength: measured (a controlled or factorial study), descriptive (a census, no intervention), vendor opinion (a product's guidance, not tested), corpus tally (40 root orientation files from large repositories, coded by a model on a 19-item checklist), or house style (this law's own choice, unmeasured). Quotes are from the abstract page unless marked.

- Measured — Xu and Wu, [arXiv 2610.30571](https://arxiv.org/abs/2610.30571), 40 Python projects, 320 runs, Claude Code and Codex CLI: "Without the sentence, 90.1% of the agents' choices followed the stale file." "The sentence raised the share of choices that follow the code from 4.2% to 91.4%, stopped the rewrites and raised the test pass rate from 89.4% to 100%." The sentence sat in "the user-level instruction file"; a repository-file placement is untested. Its wording, quoted from the full text by the research run and not re-checked here: "When a repository's documentation and its existing code disagree, follow the conventions used in the existing code, and in your final reply point out each inconsistency you found between the documentation and the code."
- Measured — McMillan, [arXiv 2605.10039](https://arxiv.org/abs/2605.10039), factorial, 1,650 Claude Code sessions, one trivial target annotation: "None of the four structural variables or three two-way interactions produces a detectable contrast after multiple-testing correction." The variables are file size, instruction position, file architecture and contradictions in adjacent files; size and conflict nulls carry affirmative-null Bayes factors, position and architecture nulls are "failures to reject". The largest effect is within-session: "approximately 5.6% lower odds of compliance per step (OR = 0.944)".
- Measured — Gloaguen et al. (ETH Zurich), [arXiv 2602.11988](https://arxiv.org/abs/2602.11988): "providing context files does not generally improve task success rates, while increasing inference cost by over 20% on average", holding "for both LLM-generated and developer-committed context files"; "repository overviews, although popular and recommended by model providers, are not helpful"; "context files are useful for specifying non-standard coding practices".
- Measured — Lulla et al., [arXiv 2601.20404](https://arxiv.org/abs/2601.20404), 10 repositories, 124 pull requests: `AGENTS.md` "is associated with a lower median runtime (Δ 28.64%) and reduced output token consumption (Δ 16.58%), while maintaining a comparable task completion behavior".
- Measured — [arXiv 2608.11095](https://arxiv.org/abs/2608.11095), 247,694 instruction lifetimes in 1,867 repositories: agentic prompts "grow without bound, more than tripling over their lifetime (+226%)"; "prompt comments can improve real-world agentic instruction-following by up to 23.1%" (a WildIFEval-derived setting scored by an LLM judge).
- Descriptive — Chatlatanagulchai et al., [arXiv 2511.12884](https://arxiv.org/abs/2511.12884), 2,303 files from 1,925 repositories: developers prioritize "test procedures (75.9%), implementation details (70.8%), and architecture (68.1%)"; "security (14.8%) and performance (14.5%) are rarely specified"; the files "evolve like configuration code through frequent, small additions".
- Vendor opinion — Claude Code [memory docs](https://code.claude.com/docs/en/memory): "target under 200 lines per CLAUDE.md file. Longer files consume more context and reduce adherence." (the adherence half is contradicted by the measured size null above) · "if two instructions contradict each other, Claude may pick one arbitrarily".
- Vendor opinion — Anthropic prompting guidance: "Where you might have said "CRITICAL: You MUST use this tool when...," you can use more normal prompting such as "Use this tool when..." instead."
- Corpus tally, 40 roots: "read X before Y" triggers 31/40; links to docs read on demand 32/40; a commands block 34/40; a testing section 37/40; a directory tour 25/40; rationale text 30/40; child orientation files 14/40; a glossary or vocabulary 5/40; `@path` imports in the root body 0/40; compressed `{a|b}` / `·` notation 0/40; path-scoped rule files 2/15 (tree-verified subset). zed's admission test requires a rule be "**Repeatedly encountered** — it came up more than once".
- House style, unmeasured: the 200-line figure as a fixed cap, the Vocabulary section, compressed set notation, bullets over tables.

What the evidence decides: the largest measured lever is precedence — telling the reader the code outranks a stale file — so it heads the spine. File shape (size, position, structure) shows no measured adherence effect, so the budget is set by cost (over 20% inference cost, paid per load) and by engine limits, never by an adherence claim. A directory tour is the content measured not to help, so the map is keyed by term. The corpus's dominant token-saving device is the on-demand trigger, so procedures and reference docs live behind trigger lines instead of inline.

## Engine facts

Verified at write time against the vendor page or source named.

- Claude Code sub-agents ([sub-agents docs](https://code.claude.com/docs/en/sub-agents), § What loads at startup): a non-fork sub-agent's initial context holds "every level of the CLAUDE.md hierarchy the main conversation loads, including `~/.claude/CLAUDE.md`, project rules, `CLAUDE.local.md`, managed policy files, and any `AGENTS.md` files … loaded as project instructions. The built-in Explore and Plan agents skip this." A definition setting `omitClaudeMd` loads only managed policy files. Its system prompt is "the agent's own prompt … not the Claude Code system prompt" — so a fleet or system-layer prompt never reaches a sub-agent, and the orientation chain does, except in Explore and Plan.
- Claude Code loading ([memory docs](https://code.claude.com/docs/en/memory)): files above the working directory load at launch, subdirectory files once Claude reads, writes or edits a file there; imported files "load together with the CLAUDE.md that imports them", so imports do not reduce cost; `.claude/rules/` files with `paths:` load when a matching file is read or edited; `AGENTS.md` is read "only when you have no `CLAUDE.md` in your working directory or above it" by default.
- Codex ([AGENTS.md guide](https://learn.chatgpt.com/docs/agent-configuration/agents-md)): from the project root down to the working directory, "it checks for `AGENTS.override.md`, then `AGENTS.md`", "at most one file per directory", concatenated root first, and it "stops adding files once the combined size reaches the limit defined by `project_doc_max_bytes` (32 KiB by default)". Source (`codex-rs/core/src/agents_md.rs`, `load_project_instructions`): the budget starts at `config.project_doc_max_bytes` and subtracts project docs only — the global `~/.codex/AGENTS.md` arrives as user instructions outside it; the crossing file is cut mid-file (`data.truncate(remaining as usize)`), signalled only by a tracing warning; whitespace-only files are skipped.
- Antigravity ([rules docs](https://antigravity.google/docs/rules)): it "truncates any single rule file that exceeds 24,000 bytes (after expanding … includes)".

## Why each part

- Precedence line: the one measured intervention with a large effect. It sits in the orientation file, not a system layer, because a sub-agent receives the orientation chain and never the system layer (§ Engine facts). It scopes to described facts (Vocabulary, Runtime, On demand) so a stale path yields while `# Rules` — the human's decisions — keeps binding.
- Vocabulary: term-keyed, because the term is the key the reader arrives with and the tour is measured not to help; closed world makes an absent term a reliable "not a component here". House style.
- Runtime: environments as well as engines, so the law holds for a web application (`## Local`, `## CI`, `## Production`) as much as an agent framework. The done-gate and the narrowest test are the two commands every task ends on; test procedures are the content developers write most.
- On demand: a trigger line costs one line per load and the procedure costs its bytes only on the task that needs it. A skill whose own `description:` names the trigger already routes, so its line would be a roster entry.
- Rules: admission demands a recorded recurrence because rules accrete one commit at a time and are rarely deleted (+226%); a rule nobody has needed twice is the growth. Sacred ground is exempt — nobody waits for a second leak. A sacred rule names its guard so the reader knows which mechanism holds it and the checker knows which rules rest on the prompt alone. One cost clause is allowed because comments measured a gain and a cost names what the rule protects; history stays out because it ages.
- Density: one fact per line, compressed notation, bullets over tables — house style for bytes and greppability.
- Budget: 200 lines and 16,000 bytes per file, plus the engine limits (24,000 bytes per file, 32 KiB Codex chain) the file would otherwise silently cross. Every byte is paid by every session and every sub-agent.
- Placement: a child orientation file is the one scoping device every engine reads; a `.claude/rules` file is invisible to Codex unless a generator carries it.

## The truth script

Embedded in the command, run by `check` and by `write`'s final step as `bash {script} {orientation file}`. Every finding line opens with its kind (`ERROR`, `MISSING`, `ABSOLUTE`, `UNCOVERED`, `OVER`, `PATH-RULE`, `ADVISORY`, `CODEX`) so a caller greps by prefix; the last line is the summary. Its broken states print `ERROR`, never silence: an unreadable file, a file outside git, no path extracted, no Vocabulary section, an unreadable chain file, a repository with a `.codex/` directory but no `AGENTS.md` mirror built.

- Path truth: every backticked token holding a `/` and no placeholder, URL, or `~` or `/` prefix must exist relative to the file's directory or the repository root.
- Absolute: a machine-absolute path with a real name after `/Users/` or `/home/`; a rule naming the pattern with `…` passes.
- Closed world: each top-level directory git tracks under the file's directory must open a backticked path inside `# Vocabulary`.
- Chain: the mirror each engine reads is measured, never the source — the generated `AGENTS.md` carries the engine adapter and can exceed a source inside budget. The summary reports the chain total against 32,768 bytes and the global file's bytes outside it; a crossing prints the file and the line where Codex stops reading.
- Sacred guards: each bullet of the first `##` under `# Rules` without ``· guard `…` `` prints `ADVISORY`; the summary counts sacred rules, so a file with no sacred section reads `sacred rules 0` rather than a clean pass.
- Path rules: in a repository with an `AGENTS.md` chain, each `.claude/rules` file prints `PATH-RULE` for `check` to judge against the mirror generator.

## Relations

- `/quality:prompt` governs every line's prose; this law governs the orientation file's shape and admission, and runs the prompt law's self-check as check 11.
- `/quality:doc` governs reference docs; the orientation file cites them from Vocabulary entries and trigger lines and never holds their content.
- `/quality:description` governs the `description:` of this command, and the description of every skill a trigger line names.
- `/quality:llm-codebase` owns the glossary and its spelling census; the Vocabulary holds the glossary's canonical terms and homes, and its orientation-truth check and this law's truth script test the same fact from two sides.
- `/context-audit` runs the truth script and reads its prefixed lines.
- In a framework install that routes prompt-file edits through a change manager, that manager's CLAUDE.md conventions point here instead of restating them.

## Decisions

- Scope is a project's orientation file. A user-level file (`~/.claude/CLAUDE.md`) maps no repository, so closed world and the truth script do not apply to it; its prose still answers to `/quality:prompt`.
- Name `quality:claude-md`: the file name people type. The body covers every engine's equivalent. Rejected: `quality:orientation`, which nobody types; `quality:agents-md`, which names one engine.
- A term-keyed Vocabulary instead of a directory tour: the tour is the content measured not to help; the term is the key the reader arrives with.
- `# Rules`, not `# MANDATORY Rules`: a MANDATORY heading tells the model every rule beneath it is an invariant (vendor opinion on emphasis; the research run found no measured study of emphasis in orientation files); the sacred-ground `##` placed first in `# Rules` carries the weight.
- Path vars under Vocabulary: the same name → path shape, defined before any rule uses them.
- Full paths only: a fragment relative to an earlier mention makes the reader rebuild the path — the round-trip the file exists to save.
- The truth script is embedded in the command, not shipped as a file: one file to install on every engine, no executable bit to keep.
- The truth script carries no bare `$` followed by a digit: the harness substitutes `$0`, `$1`… with the command's arguments when the command loads, so awk's `$0` arrived as the argument text; awk patterns match the line implicitly instead.
- `write` ends with the term list for the owner to strike: a term nobody strikes is one the owner uses. The ETH result covers developer-committed files too, so curation is not shown to raise success; the list is kept for its cost — one read by the owner.
- Precedence line at the head of the spine: the measured 4.2% → 91.4% intervention. Placed in the repository's root orientation file, because sub-agents receive the orientation chain and never the system layer; the root carries it once and a child inherits it. A user-level file stating it does not exempt the repository, which travels without it. Its placement in a repository file is untested.
- `# On demand` between Runtime and Rules: the corpus's main token-saving device (31/40 triggers, 32/40 on-demand doc links). It follows Runtime because a trigger names paths and commands Runtime defines, and precedes Rules because a rule may cite a trigger.
- Runtime environments carry the done-gate command and the narrowest test invocation: every task ends on one of the two, and test procedures are what developers write most (75.9%).
- Sacred ground stays first in `# Rules`, each rule naming its guard or reported `ADVISORY`: a rule resting on the prompt alone is a named risk the owner can choose to close with a hook, never a reject that forces a fake guard.
- One cost clause per rule: comments measured a gain (up to 23.1%, LLM-judged) and 30/40 corpus files carry rationale; history, incidents and dates stay banned because they age.
- Rule admission needs a recorded recurrence (review finding, repeated correction), sacred ground excepted: zed's "Repeatedly encountered" test, against the measured growth (+226%).
- Check 8 names generic directives that fail on sight: a directive naming no tool, path, command or threshold is unverifiable, and the named list makes the judgment mechanical.
- Check 12, contradiction with any co-loaded layer: two contradicting instructions leave the model to "pick one arbitrarily" (vendor opinion), and `check` reads the whole stream anyway.
- Budget: keep 200 lines and 16,000 bytes per file, justified by cost (over 20% inference cost per load, paid by every sub-agent too) and engine limits; the 200-line figure is house style and no adherence claim — the factorial study found no detectable size or position effect. Added: each file, mirror included, under Antigravity's 24,000 bytes; the Codex chain under 32 KiB, measured on the generated mirror.
- The global `~/.codex/AGENTS.md` is reported beside the chain, not inside its total: Codex's source subtracts only project docs from `project_doc_max_bytes`.
- Path rules: a child orientation file in a repository mirrored to another engine; a `.claude/rules` file passes only where the mirror generator carries it to every engine — engine-sync carries none — else `check` flags it a one-engine rule.
- `write` reports the byte change against `HEAD`, naming what any growth bought: files grow by small additions and are rarely pruned; a number at every write makes the growth visible.
- Rejected: moving sacred ground to the top of the file. The position null shows no measured effect, and the chain budget removes the truncation risk that motivated it.
- Rejected: flipping the source of truth to `AGENTS.md`. The corpus majority writes `AGENTS.md` and points `CLAUDE.md` at it; this framework generates `AGENTS.md` from `CLAUDE.md` with an engine adapter, and one hand-edited file either way holds the contract.
- Rejected: adopting agnix as a dependency. Its rules overlap checks this law already runs in-prompt, and its rule catalogue was not verified.
- Rejected: allowing directory tours. 25/40 corpus files carry one, and the measured result is that overviews do not help.
