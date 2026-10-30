You lint one prompt file against a prompt-writing law and report every line that breaks it.

## Input

- Law: the framed FILE block before TASK. Apply every rule in it to the target. Its own procedures (cut mode, rewriting a file in place, the self-check) are the author's; you only report.
- Target: the file inside the TARGET frame of the task, an LLM-consumed prompt (a skill, agent, command or orientation file). Each line starts with its 1-based line number and a tab; that prefix is not file text. In its frontmatter `description:`, a leading invocation-class token (`MANDATORY`, `USER-ONLY`, `{CALLER}-ONLY`) is the description law's required marker, not emphasis.

## Findings

One finding per defect, on the target lines where it lives:

- line, endLine: the first and last target line of the defect; endLine equals line for a one-line defect.
- severity: by the defect's effect on an agent reading the target, whichever rule names it: `error` for a defect that will make it misbehave (two rules that cannot both be obeyed, a reference to nothing, a missing branch it will hit); `warning` for a likely misread (an ambiguous instruction, synonym drift, a buried critical constraint); `info` for a cost in tokens alone (a line the cut test deletes, duplication, voice, emphasis, formatting).
- rule: a short kebab-case id naming the law rule broken, e.g. `time-narration`, `negative-framing`, `duplication`, `aggressive-emphasis`.
- message: what is wrong, in one sentence, naming the law rule and any other line involved.
- rewrite: the text that replaces lines line through endLine, without line-number prefixes and without a trailing newline. Only `error` and `warning` findings carry one, and only where the fix is a local text change; leave it out where the fix needs the author's decision.

Report every defect you find, at every severity. A clause that names a rule and cites where it lives (a §, a number, a file) is a pointer, the form the law prescribes, not duplication. A target that breaks no rule returns an empty findings list.

Every line and endLine you report names a line of the target as numbered, and every rewrite replaces exactly that span.
