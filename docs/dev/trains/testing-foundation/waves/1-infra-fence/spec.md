# Wave 1 — `infra/fence/`: one home for the fence, demo keeps only the demo, readme-gif retired

**Worktree:** `.worktrees/tf-fence` (branch `wave/tf-fence` from `develop @ 8bfb6cdc`) — its own worktree because Wave 0 fences from `.worktrees/testing-foundation` while this wave moves the fence files.
**Owner split:** a `dev` agent does every non-guarded move and re-point; GOD applies the guarded edits (`.claude/scripts/dev.sh`, `.claude/commands/readme-gif.md` removal, root `CLAUDE.md` § Repo structure) through `/pcm` from the patch the agent leaves at `tmp/W1-guarded.patch`.

## Target layout

```
infra/
  README.md                      # the map: fence/ vs demo/ vs check-self-hosted-manifest.sh
  check-self-hosted-manifest.sh  # repo gate, not fence-only (dev.sh verify templates calls it) — stays
  fence/
    docker-compose.yml           # was infra/docker-compose.yml
    pfm-dev.Dockerfile           # was infra/pfm-dev.Dockerfile  (build context becomes infra/fence/)
    fence-env.sh                 # was infra/fence-env.sh
    tools.env  tools.sh          # were infra/tools.env, infra/tools.sh
    release-rehearsal.sh         # was infra/release-rehearsal.sh (it drives the fenced adopter machine)
  demo/                          # unchanged content: up.sh adopt.sh fleet.sh verify.sh storm.sh kill-storm.sh idle.sh look.sh setup.sh creds.sh daemon.sh aliases.zsh README.md *.code-workspace vscode-attach.json
  readme-cards/                  # UNTOUCHED this wave (open question for the user: keep or retire the README card recorder)
```

`infra/readme-gif/` is deleted whole (`chrome.py fleet.sh fleet.tape record.sh seed-limits.py` and any `fakeclaude/` it references), with every pointer.

## Every consumer door (enumerated on `8bfb6cdc`; the agent re-greps and appends any it finds)

| Consumer | Today | After |
| --- | --- | --- |
| `.claude/scripts/dev.sh:348` `compose=` · `:357` sources `infra/fence-env.sh` · `:293` comment `infra/tools.env` | `infra/…` | `infra/fence/…` — GUARDED, GOD applies |
| `.claude/scripts/dev.sh:271` `bash infra/check-self-hosted-manifest.sh` | stays | stays |
| `infra/demo/up.sh:60` sources `infra/fence-env.sh` · `:63` `docker compose -f "$ROOT/infra/docker-compose.yml"` | `infra/…` | `infra/fence/…` |
| `infra/demo/setup.sh`, `look.sh`, `README.md:7` (mentions readme-gif) | — | README line rewritten (readme-gif gone); setup/look re-point only if they touch tools.env/tools.sh (grep) |
| `infra/pfm-dev.Dockerfile` `COPY tools.env tools.sh` (build context) | context `infra/` | context `infra/fence/`, COPY paths unchanged relative to the new context |
| `infra/fence-env.sh` header comment naming its callers (`iso`, demo, readme-gif) | three callers | two callers |
| `.github/workflows/verify.yml:117` comment + any `infra/tools.sh`/`tools.env` invocation | `infra/…` | `infra/fence/…` |
| `pfm/Makefile` (`tools` target sources `infra/tools.env`? — grep `tools.env`, `TOOLS_BIN`) | verify | re-point |
| `.jscpd.json:2` comment/paths listing `infra/…` | verify | re-point |
| `infra/check-self-hosted-manifest.sh` roster — does it enumerate `infra/` files? (grep) | verify | re-stamp `.professor/manifest.json` hashes if the roster covers moved files (`python3` sha256, indent 2, sorted — the way the train re-stamped) |
| `docs/dev/isolated-dev-foundation.md` (the fence design doc) · `docs/RELEASE.md:48` · `docs/commands/pfm/references/release-rehearsal.md:5,47` | `infra/release-rehearsal.sh`, `infra/docker-compose.yml` | `infra/fence/…` |
| `.claude/commands/pfm/release.md:14` + mirrors `.codex/skills/pfm-release/SKILL.md`, `.opencode/command/pfm-release.md` | `infra/release-rehearsal.sh` | GUARDED source → GOD; mirrors regenerate via `pfm codex build .` + `node .claude/scripts/build-opencode.mjs generate` |
| `.claude/commands/readme-gif.md` + mirrors `.codex/skills/readme-gif/SKILL.md`, `.opencode/command/readme-gif.md` | the recorder command | DELETED (GOD, guarded) + mirrors regenerated (the mirror files disappear) |
| `.professor/drift.md:468` (`/readme-gif` entry), `:465,:492` (`dev.sh iso` paths) | historical ledger lines | append one dated line: fence moved to `infra/fence/`, readme-gif retired; never rewrite history lines |
| `README.md`, `INSTALL.md`, `docs/**` embeds of `docs/img/pfm-fleet.gif` (the recorded hero GIF) | grep | if nothing but readme-gif produced/used it and README no longer embeds it, delete the GIF; if README still embeds it, STOP and report (user decision) |
| `templates/refresh-map.json`, `templates/**` | grep `infra/` | expected none; report the count |
| `pfm/**/*.go` | grep `infra/` | expected none (the fence contract travels by env, `PFM_DEV_*`); report the count |

## Steps (agent)

1. `git mv` the five fence files into `infra/fence/`; `git rm -r infra/readme-gif`; fix the Dockerfile build context in the compose file (`build.context: .` stays valid only if compose lives beside the Dockerfile — it does).
2. Re-point every non-guarded consumer above; run `grep -rn 'infra/' --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=tmp --exclude-dir=.worktrees .` and reconcile the list: every hit is either re-pointed, historical (drift.md), or listed in the report with the reason it stays.
3. Write `tmp/W1-guarded.patch` (unified diff) for the guarded files: `dev.sh` lines 293/348/354–357, `.claude/commands/pfm/release.md:14`, deletion of `.claude/commands/readme-gif.md`, root `CLAUDE.md` § Repo structure line 10 (`infra/`: describe `fence/` + `demo/` + the manifest check).
4. Rewrite `infra/README.md` as the map (≤ 40 lines) and `infra/demo/README.md:7`.
5. Gates the agent CAN run without the guarded edits: `bash -n` on every touched `.sh`; `docker compose -f infra/fence/docker-compose.yml config` (validates the moved compose + build context) with `PFM_DEV_WORKTREE=$PWD PFM_DEV_GIT_COMMON=$(git rev-parse --path-format=absolute --git-common-dir) PFM_DEV_GIT_DIR_REL=worktrees/tf-fence`; `bash scripts/leak-check.sh --files <touched>`.
6. Report: the move list, the consumer reconciliation table (hit → action), the patch path, gate outputs quoted, and every STOP.

## Steps (GOD, after the agent)

7. `/pcm`: apply `tmp/W1-guarded.patch`; regenerate mirrors; re-stamp manifest if the roster covers moved paths.
8. Gate on the committed tree from `.worktrees/tf-fence`: `.claude/scripts/dev.sh iso status` (proves the moved fence builds and mounts), `iso verify templates` (leak, manifest, clone ratchet — expect the readme-gif clone entries to vanish: baseline may need `scripts/clone-check.sh --measure` with the count named in the commit), `iso verify pfm`, `bash infra/demo/up.sh --no-fleet` is NOT run here (spends nothing but takes 10 min; Wave 4 exercises it).
9. gitter commits by pathspec: `refactor(infra): the fence lives in infra/fence; demo keeps only the demo; readme-gif retired`.
