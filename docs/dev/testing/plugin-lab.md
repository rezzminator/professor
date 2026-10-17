# Plugin lab

`infra/plugin-lab/lab` runs a plugin checkout in a real, isolated, metered Claude Code session on the host, drives it through a scenario and prints one pass/fail row per check. Its hermetic self-tests run in the fence as the `templates.plugin-lab` gate step.

## Table of Contents

- [Isolation](#isolation)
- [Run](#run)
- [Read a report](#read-a-report)
- [Adopt for a plugin](#adopt-for-a-plugin)
- [Write a scenario](#write-a-scenario)
- [Verbs](#verbs)
- [Gaps](#gaps)
- [Self-tests](#self-tests)
- [Follow-up migration](#follow-up-migration)

## Isolation

Each run gets a private folder `{root}/{run id}` (`{root}` is `$LAB_ROOT`, else `/tmp/{project}/lab`) holding its own `config` (`CLAUDE_CONFIG_DIR`), `home`, `tmp`, `work` (the session's project dir), `logs` (`PLUGIN_LOG_DIR`), `pane` and a private tmux socket `t.sock`.

- claude starts under `env -i` with an allowlisted env (`HOME`, `TMPDIR`, the XDG dirs and `CLAUDE_CONFIG_DIR` inside the run, `PLUGIN_LOG_DIR={run}/logs`, CLAUDE.md loading, autoupdate and nonessential traffic off) plus the scenario's `LAB_ENV`, and with `--setting-sources project --settings {run}/settings.json --plugin-dir {the checkout} --model {LAB_MODEL} --debug-file {run}/debug.txt`.
- Every installed copy of the same plugin in the live store is turned off in the run's settings; the run's own store installs none of it.
- Sign-in needs no login: the lab reads the invoking store's keychain item read-only (`--auth-from DIR`, default `$CLAUDE_CONFIG_DIR`, else `$HOME/.claude`; Linux reads `{DIR}/.credentials.json`), takes only its `accessToken` and hands it to claude on fd 3 through a FIFO under `{run}/sec`, with `CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR=3`. The token never reaches argv, env, a file, the report or the ledger. Preflight refuses a token expiring before the scenario's wall cap plus 10 minutes. A keychain item the run created under its own config hash is deleted at teardown, and only that one.
- Managed settings come from `{run}/managed/managed-settings.json` (default `{"prependPlugins":[]}`), the directory handed over as `CLAUDE_CODE_MANAGED_SETTINGS_PATH={run}/managed`.
- A hook mark (`hooks/mark.sh`, one JSON row per `SessionStart`, `UserPromptSubmit`, `Stop`, `SubagentStop`, `PreCompact` and `SessionEnd` into `{run}/marks.jsonl`) tells the driver where the session is; permissions run in `dontAsk` mode with `LAB_ALLOW` as the allow list and `LAB_DENY` as the deny list.

The lab needs bash >= 4.4 (`brew install bash` on macOS; the entry re-execs under it), tmux, jq, and `pfm` on `PATH` as the meter (`LAB_METER` overrides it).

## Run

```bash
infra/plugin-lab/lab list                 # {plugin}/{scenario} {rows} {model} est {usd} cap {usd}
infra/plugin-lab/lab list --gaps          # {row} GAP {reason}, from every scenarios/*/gaps.tsv
infra/plugin-lab/lab run {plugin}/{scenario} --plugin {checkout}/plugins/{plugin} \
  [--ledger {file}] [--budget USD] [--auth-from DIR] [--claude PATH]
infra/plugin-lab/lab attach {run}         # read-only tmux view of a live run
infra/plugin-lab/lab stop {run}           # identity-checked kill, then the run's tmux server
infra/plugin-lab/lab report {run}         # re-renders report.md
infra/plugin-lab/lab prune --days N       # deletes run folders older than N days
```

- `--plugin` is the plugin directory holding `.claude-plugin/plugin.json`; its `name` must equal the scenario's `LAB_PLUGIN`.
- `--ledger` is append-only: a run appends a `RUNNING` row before it spends and its final tab-separated row when it ends (run id, scenario, plugin, rev, dirty hash, verdict, USD, cap, started, wall seconds, report path), and copies the report to `{ledger dir}/lab-reports/{run id}.md`. Every write holds `{ledger}.lock`; a run id already in the ledger is refused before any spend. A flight keeps its ledger in its flight directory.
- `--budget` (default 10.00) refuses the run when the ledger's spend so far plus this scenario's cap exceeds it; an unknown spend counts at its cap. It needs `--ledger`: without one only the scenario's own cap holds. The sum takes a run's final row in place of its `RUNNING` one and an open `RUNNING` row at its cap, so an interrupted, aborted or killed run still counts; the check and the `RUNNING` row are one step under the lock, so runs started together never each pass on the same sum.
- The meter is polled after every wait; spend at or over `LAB_CAP_USD` stops the session and the check reads `STOPPED`.
- Exit status: 0 PASS (a GAP included), 1 FAIL, 2 ERROR or STOPPED, or a refused preflight (`ERROR {reason}` on stderr, no run folder for a usage error).

## Read a report

`{run}/report.md`, printed at the end of the run:

- Header: run id, scenario, plugin and version, claude binary, the checkout's rev and dirty hash, start, wall time. The dirty hash is a content hash: sha256 over the path and blob of every modified or untracked file under the plugin folder (`git ls-files -m -o --exclude-standard`), leaving out the engine-written `tsconfig.json` and `.claude-plugin/types/`.
- `## Passport`: the isolation proofs, one line each — observed env keys equal the allowlist plus `LAB_ENV` (read from outside the process), the session id is in no live store, no keychain item left behind, no undeclared hook bypass line in the debug log, rev and dirty hash equal at start and end with no hot reload of the plugin's hooks in the debug log (Claude Code reloads a `--plugin-dir` on any edit), the installed copies off, the spend metered against the cap.
- Vitals (when `LAB_LOGGER=yes`): `vital/first-row` (at least one level or decision row since the start under `PLUGIN_LOG_DIR/{plugin}/`, the earliest named; a plugin logs on events only, so the scenario must make it write one) and `vital/no-new-errors` (no row in `errors.jsonl` since the start but those `expect_error` declared).
- `## Checks`: one row per beat, `| {scenario}/{slug} | {row} | {verdict} | {evidence} |`.
- Last line: `verdict {PASS|FAIL|ERROR|STOPPED} · ${usd} / {cap}`.

Verdict words:

| Verdict | Means |
| --- | --- |
| PASS | the verb held |
| FAIL | the plugin behaved wrongly; the evidence names what was found |
| ERROR | the lab could not judge: a setup step, a precondition, an unreadable log, a timeout; never a plugin verdict |
| GAP | the fault the scenario needs was not induced (`LAB_GAP_IF_NOT_INDUCED`); no verdict on the plugin |
| STOPPED | the spend cap stopped the session |

A beat whose `--needs` did not PASS is `ERROR blocked-by {id}` (`GAP blocked-by` when that one was a GAP). The run verdict is the worst row: STOPPED, then ERROR, then FAIL, else PASS. The raw evidence stays in the run folder: `checks.tsv`, `marks.jsonl`, `logs/{plugin}/`, `pane/`, the transcript under `config/projects/`.

## Adopt for a plugin

1. Make the plugin's level and decision logs honour `PLUGIN_LOG_DIR`; a scenario whose session writes no row sets `LAB_LOGGER=no`.
2. Nothing to ignore for the files Claude Code writes into the plugin's folder on load: `passport/identity` leaves out `tsconfig.json` and `.claude-plugin/types/`; any other file the load writes fails it as `tree-changed`.
3. Create `infra/plugin-lab/scenarios/{plugin}/`, the directory name equal to the plugin's `name`.
4. Write `logger-ready.sh` first, a turn that makes the plugin write its first row (copy `scenarios/effort-live-control/logger-ready.sh`); run it once live.
5. Add a scenario per requirement row; list rows only unit tests can carry in `gaps.tsv`.
6. Run `bash infra/plugin-lab/tests/scenarios_test.sh` before the first live run.

## Write a scenario

A scenario is `scenarios/{plugin}/{slug}.sh`, sourced by the lab: a header of variables, then one function `lab_scenario` made of beats.

```bash
# S2: one turn with an invalid option; the plugin's first row is its option-error warning under PLUGIN_LOG_DIR (R1).
# shellcheck shell=bash disable=SC2034
LAB_PLUGIN=effort-live-control
LAB_ROWS=(R1)
LAB_MODEL=haiku
LAB_CAP_USD=0.25
LAB_EST_USD=0.02
LAB_WALL_S=300
LAB_ENV=()
LAB_LOGGER=yes
lab_options '{"floor":"bogus"}'

lab_scenario() {
  beat ready -- turn 'Reply with the word READY.' 300
  beat option-error-row --needs ready -- level_has warnings 'select(.event == "option-error" and .where == "hooks/effort-live-control.ts#start")'
}
```

Header:

| Variable | Holds |
| --- | --- |
| `LAB_PLUGIN` | the plugin `name` |
| `LAB_ROWS` | requirement rows; the first is every beat's default row |
| `LAB_MODEL` | `--model` for the session |
| `LAB_CAP_USD` | the spend cap that stops the run |
| `LAB_EST_USD` | the expected spend, shown by `lab list` |
| `LAB_WALL_S` | wall cap in seconds; the token must outlive it by 10 minutes |
| `LAB_ENV` | extra `KEY=value` pairs for claude's env; `{run}` expands |
| `LAB_ALLOW` | `permissions.allow` entries for the run settings |
| `LAB_DENY` | `permissions.deny` entries; a corpus-filling scenario denies `Bash Grep Glob`, since Claude Code runs read-only shell commands even in `dontAsk` and a model answers a read turn by `tail` |
| `LAB_MANAGED` | managed-settings JSON; `{run}` expands to the run folder |
| `LAB_GAP_IF_NOT_INDUCED` | the reason a fault-inducing scenario reads GAP when the fault did not happen |
| `LAB_LOGGER` | `yes` adds the vitals; `no` skips them |
| `LAB_DECISIONS` | the decision log the decision verbs read, when an option moves it; `{run}`, `{plugin}` and `{sid}` expand; default `{run}/logs/{plugin}/decisions/{sid}.jsonl` |
| `lab_options` | the plugin's options JSON, written to `pluginConfigs.{plugin}@inline.options`; `{run}` expands |

Beats: `beat {slug} [--row R] [--needs slug]... -- {verb} [args]`. Slugs are unique in a scenario. The body may use only verbs plus `for`, `if`, `local`, `((` and plain assignments; `tests/scenarios_test.sh` enforces that, the header, and that the caps of all scenarios sum to at most USD 10.

- Preludes (`need_*`) run before or between beats; a failed prelude writes a `{scenario}/setup` ERROR row and blocks every later beat.
- `decision_precondition` and `level_precondition` read a row the scenario depends on but does not judge: absent is ERROR, never FAIL.
- The first drive verb boots the session lazily; the session ends with `exit_session` after the last beat.
- `slash {command} SessionStart` follows the new session: `LAB_SID_PREV` keeps the old id, `LAB_SID` and `LAB_TRANSCRIPT` move to the new one, so later decision verbs read the new session's log; read the old one with `jsonl_has '{run}/logs/{plugin}/decisions/'"$LAB_SID_PREV"'.jsonl' …`.
- `LAB_AGENT_ID`, `LAB_AGENT_TRANSCRIPT` and `LAB_SID_PREV` stay empty until `wait_subagent` or `slash … SessionStart` sets them, so a beat naming them takes `--needs` on that beat.
- `expect_error {event}` declares a level-log error row the scenario induces on purpose; `expect_bypass {plugin} {event}` declares a hook bypass line it induces.

## Verbs

| Verb | Arguments | Does |
| --- | --- | --- |
| `need_corpus` | name tokens | writes `{run}/work/corpus/{name}.txt` of `tokens × 4` bytes, ending in a unique last word; Claude Code counts these words at about 1.5 tokens per 4 bytes (a 15000 file reads as 22.4k), so size a fill against the measured context, not the argument |
| `need_agent` | name model tools [flow map] | a project agent in `{run}/work/.claude/agents/`; the map renders as nested `autoCompact:` lines |
| `need_dir_mode` | path mode | pre-creates a directory with that mode |
| `need_fixture_plugin` | fixture marketplace | installs `fixtures/{fixture}` into the run's store and enables it in the run settings |
| `say` | text | types one line; returns at its prompt-submit mark; a line neither the transcript nor the pane holds after 5 s is typed once more |
| `wait_turn` | [timeout] | the stop mark ending the last `say`'s own turn: its transcript shows a stop-hook summary after that prompt (a user entry, or the `queued_command` attachment a line typed during a running turn is delivered as), so the Stop of a background notification's turn is passed over |
| `turn` | text [timeout] | `say` then `wait_turn` |
| `slash` | command [mark event] [timeout] [confirm option] | a slash command; with an event, waits for its mark; without one, for the transcript's record of the command's output (`<local-command-stdout>`), the output as evidence, pressing Enter once on a confirmation whose selected line holds the confirm option (`/model` asks before it drops a warm cache) |
| `wait_subagent` | type [timeout] | the sub-agent stop mark of that type |
| `exit_session` | none | `/exit`, then the session-end mark |
| `read_turns` | prefix from to [stop at N] | one read turn per corpus file; each turn must `Read` its file whole (else `ERROR fill-not-read`, naming the tools used) and its reply name the last word |
| `fill_until_boundary` | prefix from to | read turns, each proven as in `read_turns`, until a main compact boundary; GAP when a declared bypass was not induced |
| `bypass_seated` | builtin plugin id | the plugin carrying the declared bypass was seated at boot (its `plugin.register` line); its `not seated` debug line is a GAP, so an uninduced fault ends the run before any fill is paid for; neither line is ERROR |
| `witness_compactions` | [--agent TYPE] | plugin compaction rows matched one to one with transcript boundaries at ±5%; a boundary's size is the last reply's context before it (the plugin's measure), its `preTokens` only when no reply precedes |
| `decision_has` / `decision_lacks` | jq select [min] | rows of the session's decision log |
| `decision_count` | jq select op n | count with `-le`, `-ge` or `-eq` |
| `decision_first` | event | line 1 of the decision log carries that event |
| `decision_precondition` | jq select | absent is ERROR |
| `decision_prefix_save` / `decision_prefix_kept` | none | the saved prefix is unchanged and no `.tmp` is left |
| `level_has` / `level_lacks` | level jq select [min] | rows of `logs/{plugin}/{level}.jsonl`; `level_lacks` passes a level file the logger never wrote in a log folder that holds rows |
| `level_count` | level jq select op n | count of level rows |
| `level_precondition` | level jq select [min] | fewer rows is ERROR |
| `jsonl_has` | file jq select | any JSONL file; `{run}` expands |
| `file_absent` | path | the path does not exist |
| `transcript_has` / `agent_transcript_has` | jq select | rows of the main or the last sub-agent transcript |
| `reply_has` | text | the last assistant reply contains it |
| `pane_lacks` | text | the pane, saved now to `pane/`, does not contain it |
| `mark_has` | jq select | rows of `marks.jsonl` |

The authoritative list is `LAB_VERBS` in `infra/plugin-lab/lib/verdict.sh`.

## Gaps

`scenarios/{plugin}/gaps.tsv` names each requirement row no live scenario proves: `{row}<TAB>{reason}`, `#` lines are comments. `lab list --gaps` prints them as `{row} GAP {reason}`. A gap is a decision that the row's proof lives in unit tests, never a pass.

## Self-tests

`infra/plugin-lab/tests/` mirrors the code (`lib/*_test.sh`, `hooks/mark_test.sh`, `lab_test.sh`, `lab_errors_test.sh` and `lab_violations_test.sh` over the shared `lab_setup.sh`, `scenarios_test.sh`) and runs hermetically against the fakes in `tests/fakes/` (`claude`, `meter`, `security`): no network, no tokens. The fence step `templates.plugin-lab` (`checks_templates_plugin_lab` in `infra/fence/checks.sh`) runs `shellcheck -x -S warning` over the lab's shell (`infra/plugin-lab/.shellcheckrc` turns off SC2034, since a global set in one sourced file is read in another), then every self-test side by side, each file in its own temp root, printing each suite's log and rc and a `{n} suites, {red} red` total. One file on the host goes through the fence: `.claude/scripts/test-templates.sh infra/plugin-lab/tests/lib/observe_test.sh`.

## Follow-up migration

Two older per-plugin live scripts predate the lab and stay as they are until moved onto it:

- `cache-live-control`: `scripts/live-proof.sh`.
- `buddy`: `scripts/live-proof.sh`, `live-configs.sh`, `live-drawer.sh` and `live-lib.sh`. `live-lib.sh` and `live-configs.sh` sign in with `claude setup-token`, a login the lab's sign-in rule forbids; a migration replaces it with the lab's fd 3 token handoff.

Each becomes a scenario under `scenarios/{plugin}/` whose beats replace the script's own waits and greps.
