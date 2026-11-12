---
name: interaction-faults
description: 'Forecasts where systems break together — `/interaction-faults <systems> [paths…] [since]`, or asks like "same areas keep breaking", "fix whack-a-mole", "which combinations no test holds". /interaction-faults → interaction-faults agent. Returns the faults.json path and the ranked forecast. Test design → /design:test-suite.'
argument-hint: "<systems> [paths…] [since YYYY-MM-DD]"
---

# interaction-faults

Hand the request to the `interaction-faults` agent; it does the whole run.

## Brief

Build the brief from the arguments, then spawn one `interaction-faults` agent with it:

- `SYSTEMS`: the system names joined by `|` (`herdr|tmux`).
- `SCOPE`: the paths given; with none, the top-level directories holding files whose path or text matches a system name (`git ls-files | grep -iE '{SYSTEMS}'`, `git grep -ilE '{SYSTEMS}'`), joined by spaces.
- `SINCE`: the date given; with none, 30 days before today.
- `OUT`: `/tmp/{project}/interaction-faults/{slug}/`, `{project}` being `basename "$(git rev-parse --show-toplevel)"` minus any leading dot, `{slug}` the system names joined by `-`.
- `REPO`: the repository root. `K` only when the user names a count.

## Return

Relay the agent's return as it came: the `FAULTS` line, the forecast table and NOT READ. A return starting `BRIEF INCOMPLETE`, `NO FIXES`, `NO SURFACE` or `… FAILED` is relayed with the brief that produced it. Then one line: `Next: /design:test-suite {OUT}faults.json`.
