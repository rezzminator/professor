# Release rehearsal — the fenced adopter update

`/pfm:release` Step 10. A cheap Codex model plays an adopter on a fresh fenced machine: it installs the STABLE release exactly as the stable docs say, then updates to the CANDIDATE exactly as the candidate's docs say. Whatever it trips on, a real adopter trips on — the rehearsal exists to find that friction before `main` moves.

## The machine — `infra/release-rehearsal.sh`

The container `pfm-release-rehearsal` is a brand-new Linux host (own HOME, no Claude or Codex CLI, no GitHub access for this repo). Its only Professor source is `/root/upstream.git`, built from this repo's git objects. Sequence, each step checked by the script:

1. `up` → `seed {STABLE}` — upstream `main` at the newest published tag, no newer tag.
2. Stage A (brief below) → CLEAN or FRICTION; either way the machine now holds a working stable install → `snapshot`. Stage-A FRICTION is a defect already shipped — fix it in the candidate; the snapshot stays the world adopters actually live in.
3. `publish {NEW} $(git rev-parse release/v{NEW})` — the release event, local to the container.
4. Stage B (brief below). FRICTION → fix on the candidate, commit, `revert`, `publish` again, re-run Stage B.

Stage B's hardest case is the adopter several versions behind: rehearse it on a second machine beside the first (`PFM_REHEARSAL_NAME=pfm-release-rehearsal-behind`, the brief's `{CONTAINER}` set to match) seeded with an older tag in place of `{STABLE}` — the candidate's update docs must carry that adopter through every skipped release's actions.

## The driver

Model: the Codex model `pfm/internal/codexgen/config.go` maps `sonnet` to (`grep -o '"sonnet": *"[^"]*"' pfm/internal/codexgen/config.go`), effort `medium`. One run per stage attempt, its files in `$RUN` — a directory OUTSIDE every git repository (`$TMPDIR/pfm-release-rehearsal/{NEW}/{stage}-{attempt}/`): Codex loads each ancestor repo's `AGENTS.md`, and this repo's contract would turn the adopter into a Professor maintainer:

```bash
pfm headless exec --engine codex --model "$MODEL" --effort medium --no-session-persistence \
  --cwd "$RUN" --timeout 5400 --prompt-file "$RUN/brief.md" --schema "$RUN/schema.json" \
  --output-format text --out "$RUN/result.json" \
  --engine-arg --sandbox --engine-arg workspace-write \
  --engine-arg -c --engine-arg sandbox_workspace_write.network_access=true
```

`workspace-write` confines host writes to `$RUN`; `network_access=true` is what lets the sandbox reach the Docker socket. Codex credentials never enter the container — a token refresh in there would rotate the host's login.

`schema.json`:

```json
{"type":"object","additionalProperties":false,
 "required":["verdict","installed_version","release_notes_read","steps","friction"],
 "properties":{
  "verdict":{"enum":["CLEAN","FRICTION","BLOCKED"]},
  "installed_version":{"type":"string"},
  "release_notes_read":{"type":"array","items":{"type":"string"}},
  "steps":{"type":"array","items":{"type":"object","additionalProperties":false,
    "required":["doc_ref","command","exit","note"],
    "properties":{"doc_ref":{"type":"string"},"command":{"type":"string"},"exit":{"type":"integer"},"note":{"type":"string"}}}},
  "friction":{"type":"array","items":{"type":"object","additionalProperties":false,
    "required":["doc_ref","command","observed","expected","workaround"],
    "properties":{"doc_ref":{"type":"string"},"command":{"type":"string"},"observed":{"type":"string"},"expected":{"type":"string"},"workaround":{"type":"string"}}}}}}
```

Judge the result, never the model's verdict alone: re-run each claimed-clean step's check yourself through `infra/release-rehearsal.sh exec`, and replay each FRICTION command before fixing it. A missing `result.json`, a schema-invalid one, or a non-zero driver exit is BLOCKED — the rehearsal failed to run, which is never CLEAN.

## Shared brief preamble

Prepended to both briefs, `{CONTAINER}` / `{STABLE}` / `{NEW}` substituted:

> You are an adopter's assistant working on a fresh Linux machine: the Docker container `{CONTAINER}`. Run EVERY command inside it as `docker exec {CONTAINER} bash -lc '<command>'`; touch nothing else on this host. The machine cannot reach GitHub for this project: wherever docs name the project's GitHub repository (`https://github.com/<owner>/professor.git`, under either its former or its current owner), use `/root/upstream.git`; release downloads are unavailable, so take the build-from-source path. Neither the `claude` nor the `codex` CLI exists on the machine — pass `--skip-harvest --skip-engine codex` to `pfm install`, and `--skip-harvest` to `pfm update` and `pfm doctor`. The machine carries no `rg`; search with `grep`. When an interactive step expects a human, answer as a user with a small demo project would.
>
> Follow the docs literally. A documented step that fails, or docs that leave you guessing, is FRICTION: record the doc section, the exact command, what happened, and what the docs led you to expect — then do what a determined user would to get past it, and continue. Verdict CLEAN only when every step worked exactly as written; BLOCKED only when no workaround gets you through.

## Brief A — install the stable release

> Install Professor {STABLE}, reading its docs from the machine: `git -C /root/upstream.git show {STABLE}:INSTALL.md`, and `docs/SETUP.md` at the same tag.
>
> 1. Install `pfm` per INSTALL.md § Build from source, into `~/.professor`, then its preview and apply.
> 2. Adopt Professor on a project: create `/root/project` as a git repository holding a minimal program and one commit, run `pfm init` there, then execute `docs/SETUP.md`'s Install interview yourself — you are both the assistant running it and the user answering it — and commit the result.
> 3. Run `pfm doctor`, and `pfm update check` inside `/root/project`; record both.

## Brief B — update to the candidate

`{UPDATE_PROMPT}` is the text `professorUpdatePrompt` (`pfm/internal/picker/update_row.go`) returns for `{NEW}` at the candidate — the words a real adopter's update chat opens with from the `pfm ls` banner, so the rehearsal tests the product's own prompt, never a hand-tuned stand-in. A Stage B friction the prompt caused is fixed in that function.

> A new Professor release, {NEW}, is published. The user opened the update chat from `pfm ls`'s **PROFESSOR UPDATE** banner in the source clone `~/.professor`, and it opens with the update prompt quoted at the end — work it as written, running its commands from `~/.professor` (`docker exec {CONTAINER} bash -lc 'cd ~/.professor && …'`), the directory that chat starts in. The user approves the overview you present: record the overview and its checklist as your first step's `note`, then continue past the approval gate. Where a step needs docs, read them at {NEW} (`git -C ~/.professor show {NEW}:INSTALL.md`, `docs/SETUP.md` at {NEW}) — the installed copy is the old release.
>
> Then bring the adopted project `/root/project` current per `docs/SETUP.md` § Staying current at {NEW}: run `pfm update check` there and resolve every item it reports. Finish with `pfm doctor`, and `pfm update check` in `/root/project` holding no item you have not resolved. List in `release_notes_read` every release-notes file you read.
>
> The update prompt: {UPDATE_PROMPT}
