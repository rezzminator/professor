# Release rehearsal — the fenced adopter update

`releaser` Phase REHEARSE, one round per spawn. A cheap Codex model plays an adopter on a fresh fenced machine: it installs the STABLE release exactly as the stable docs say, then updates to the CANDIDATE exactly as the candidate's docs say. Whatever it trips on, a real adopter trips on — the rehearsal exists to find that friction before `main` moves.

## The machine — `infra/fence/release-rehearsal.sh`

The container `pfm-release-rehearsal` is a brand-new Linux host (own HOME, no Claude or Codex CLI, no GitHub access for this repo). Its only Professor source is `/root/upstream.git`, built from this repo's git objects. Sequence, each step checked by the script:

1. `up` → `seed {STABLE}` — upstream `main` at the newest published tag, no newer tag.
2. Stage A (brief below) → CLEAN or FRICTION; either way the machine now holds a working stable install → `snapshot`. Stage-A FRICTION is a defect already shipped, routed like any friction (releaser REHEARSE step 2) and fixed in the candidate; the snapshot stays the world adopters actually live in.
3. `publish v{NEW} $(git rev-parse release/v{NEW})` — the release event, local to the container: upstream `main` moves to the candidate and carries every published tag it contains, so a required stop in a skipped release can be updated to.
4. Stage B (brief below). FRICTION is routed to the caller (releaser REHEARSE step 2); once it is fixed, the next round `revert`s, `publish`es the new HEAD and re-runs Stage B.

Stage B's hardest case is the adopter several versions behind, so every round runs two machines side by side: `main` (`PFM_REHEARSAL_NAME=pfm-release-rehearsal`, seeded with `{STABLE}` = the newest published tag) and `behind` (`PFM_REHEARSAL_NAME=pfm-release-rehearsal-behind`, seeded with the fifth-newest tag in place of `{STABLE}`), each brief's `{CONTAINER}` set to match — the candidate's notes and docs must carry the behind adopter through every skipped release's actions and stops. The first round runs Stage A on each machine and snapshots it; every later round `revert`s to the snapshot, `publish`es the candidate's current HEAD and runs Stage B again.

## The driver

Model: the Codex model `pfm/internal/codexgen/config.go` maps `sonnet` to (`grep -o '"sonnet": *"[^"]*"' pfm/internal/codexgen/config.go`), effort `xhigh` — the weaker model at its highest setting, per `docs/design/integration-suite/laws.md` Law 5. One run per stage attempt, its files in `$RUN` — a directory OUTSIDE every git repository (`$RUN` = the release directory's `rehearsal/{machine}-{round}/` for Stage B and its `stage-a/` subdirectory for Stage A, under `$HOME/.local/state/pfm/releases/`): Codex loads each ancestor repo's `AGENTS.md`, and this repo's contract would turn the adopter into a Professor maintainer:

```bash
pfm headless exec --engine codex --model "$MODEL" --effort xhigh --no-session-persistence \
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

Judge the result, never the model's verdict alone: re-run each claimed-clean step's check yourself through `infra/fence/release-rehearsal.sh exec`, and replay each FRICTION command before routing it. A missing `result.json`, a schema-invalid one, or a non-zero driver exit is BLOCKED — the rehearsal failed to run, which is never CLEAN.

## Shared brief preamble

Prepended to both briefs, `{CONTAINER}` / `{STABLE}` / `v{NEW}` substituted:

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

`{UPDATE_PROMPT}` is the text `professorUpdatePrompt` returns for `v{NEW}` in the version INSTALLED on that machine — find the function in that tag with `git grep -n 'func professorUpdatePrompt' {installed tag} -- pfm/` (`pfm/cmd/pfm/update_notice_command.go` through v0.77.x, `pfm/internal/picker/update_row.go` after) and assemble its return verbatim. The banner a real adopter clicks is drawn by the binary they already run, so the rehearsal tests the prompt they will actually receive, never the candidate's and never a hand-tuned stand-in. A Stage B friction the prompt caused is routed twice: in the candidate's `professorUpdatePrompt`, which serves the next update, and in this release's note, which is all this update can change.

> A new Professor release, v{NEW}, is published. The user opened the update chat from `pfm ls`'s **PROFESSOR UPDATE** banner in the source clone `~/.professor`, and it opens with the update prompt quoted at the end — work it as written, running its commands from `~/.professor` (`docker exec {CONTAINER} bash -lc 'cd ~/.professor && …'`), the directory that chat starts in. The user approves the overview you present: record the overview and its checklist as your first step's `note`, then continue past the approval gate. Where a step needs docs, read them at v{NEW} (`git -C ~/.professor show v{NEW}:INSTALL.md`, `docs/SETUP.md` at v{NEW}) — the installed copy is the old release.
>
> Then bring the adopted project `/root/project` current per `docs/SETUP.md` § Staying current at v{NEW}: run `pfm update check` there and resolve every item it reports. Finish with `pfm doctor`, and `pfm update check` in `/root/project` holding no item you have not resolved. List in `release_notes_read` every release-notes file you read.
>
> The update prompt: {UPDATE_PROMPT}
