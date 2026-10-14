# auto-effort: evaluation and proposal (2026-10-11)

This is a proposal; nothing is built yet. The evidence comes from three investigations run on 2026-10-11:

- the function-hook API, read in the Claude Code 2.1.296 binary and the engine-written typings;
- 2,216 main-chat transcripts (2026-08-02 to 10-11) plus the callmeter store;
- 24 live test chats (10 cache probes, 14 pilot) running a throwaway prototype.

The evidence is kept, safe from reboots, under `~/.local/state/pfm/flights/professor/auto-effort/evidence/`: the prototype, drivers, toy repo, run logs and the analysis scripts.

## 1. The answers

| Question | Answer |
| --- | --- |
| Can a plugin do it? | **Yes**, with experimental function hooks. They are switched on by `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1`, which pfm already sets. `turn.step` fires before every main-chat model request (its `agentId` is absent) and may rewrite that request's `effort`. In the binary, a hook's value outranks the session, env and per-turn effort. Phase 0 proved on the wire that the rewritten value reaches the API, with the cache intact (§ 2.2). |
| Who decides? | **The model, but never starting from low effort.** The first request of every user turn runs at a planning level, `high` by default. On that request the model sets the rest of the turn with an always-loaded `set_effort` tool. Mechanical rails surround it: a floor, a ceiling, escalation on repeated failure, your `/effort` as a pin, a per-model gate, and a runtime cache canary. |
| Will the model know when? | **Partly.** Started at xhigh with a per-turn reminder, both Opus and Fable lowered their effort for a mechanical rename and gave exact reasons. Started at low, no chat got above low on design or debugging (6 chats). Opus never tried; Fable tried twice, with a malformed call. A model at low effort is a poor judge of whether it needs to think. |
| Trigger on rising thinking tokens? | **Rejected as the primary signal.** Low effort suppresses the very signal: at low, 0.0% of Opus 5.5 requests exceed 2,000 thinking tokens, against 8.2% at xhigh. The hook's usage carries no thinking count anyway. |
| Cache across an effort change | **Opus 5.5 and Fable 5.1 keep the cache.** History: 46 of 48 and 20 of 20 live changes. Probe: ratio 1.0000 in both directions. **Sonnet 5 loses it** (0 of 5). Sonnet 5.5 and Haiku 5.5 kept it in the probe (one change per direction), so they go on probation. Every other model has no clean data. |

## 2. Evidence

### 2.1 What the hook API allows (Claude Code 2.1.296)

| Need | Mechanism | Verdict |
| --- | --- | --- |
| Change the main chat's effort per request | `turn.step`: `next({ ...e, effort })` when `e.agentId === undefined`. The event carries `turnId, index, model, effort, messageCount`. | YES; wire delivery not verified |
| A tool the model can call | `$.tool.register({ name, description, inputSchema, isDeferred: false })`, served by `tool.call`. It is deferred behind ToolSearch unless `isDeferred: false`. | YES |
| Nudge the model | Per user turn: `prompt.submit` → `context`. Mid-turn: a tool result's `context`, shown as one reminder after the result. | YES |
| Observe | Usage (input, output, cache read, cache write), tool calls and results, prompt text, model, compaction, current effort. There is no separate thinking-token count in the hook's usage. | PARTIAL |
| Show the level | `$.ui.status`, one pinned line per plugin under the prompt. The native effort indicator keeps showing the session level, which a `turn.step` rewrite does not change. | YES |
| Run the real `/effort` | `$.command.run({ command: "effort", args })`. It is queued until the session is idle, so it cannot act mid-turn, and it saves the level as your default for new sessions. | PARTIAL |
| Edit `effortLevel`/`modelSettings` from a classic hook | Effort settings are read into session state only at startup, so the edit does nothing until restart. | NO |
| A skill or command with an `effort:` layer | The skill tool pushes a per-turn effort layer that outranks the session level. Not tested at runtime. | UNVERIFIED |
| Stability | Marked EARLY ACCESS. Between 2.1.282 and 2.1.296, 11 events were added and none removed; `TurnStepInput` is unchanged. | Low churn |

### 2.2 Cache across an effort change, per model

| Model | History: live changes inside the TTL | Probe (hook route / `/effort` at a turn boundary) | Gate |
| --- | --- | --- | --- |
| claude-opus-5-5 | 46/48 HIT (`/effort` 23/23) | 1.0000 both directions / 1.0005 | ALLOW |
| claude-fable-5-1 | 20/20 HIT | 1.0000 both directions / 1.0005 | ALLOW |
| claude-sonnet-5-5 | no clean data | 1.0000 both directions / 1.0005 | PROBATION |
| claude-haiku-5-5 | 2/3 (the miss coincides with an MCP tool change) | 1.0000 low→xhigh; xhigh→low not run | PROBATION |
| claude-sonnet-5 | 0/5: system and tools prefix kept, messages rewritten | not probed | DENY |
| claude-opus-5 | no clean data | not probed | OFF |
| claude-opus-4-8, claude-haiku-4-5 | barely caches / effort not recorded | not probed | OFF |

**How to read the numbers.**

- **Ratio:** cache read at the request after the switch ÷ the previous request's whole prompt (input + cache read + cache write).
- **Spot-check, Opus 5.5 low→xhigh at request 4:** cache read 35,798 = 35,711 + 87, so the whole previous prompt was served from cache.
- **History method:** consecutive completed main-chat requests, classified HIT at ≥ 0.9 of the previous whole prompt. Pairs past the cache TTL, with a restart, or across a compaction are excluded.
- **TTL expiry dominates misses overall.** Across-turn controls past the TTL hit 9–29% of the time; inside it, 98–100%.
- **A restart or reload breaks the cache on every model**, whatever the effort.

**Wire truth (Phase 0, 2026-10-11).** Setup: 4 test chats (Opus 5.5 and Fable 5.1, one started at low and one at xhigh), 44 main-chat requests recorded by a private model-gateway instance. The outcome is **(a): the effort reaches the API, the cache holds, thinking follows.**

- **Two fields at launch.** Claude Code already sends effort in two places:
  - the top-level `output_config.effort`;
  - a `role: "system"` message inside `messages` carrying `output_config: { effort }`, with beta headers `per-turn-control-2026-07-01` and `mid-conversation-system-2026-04-07`.
  The per-turn system message is the normal channel.
- **What a hook rewrite does.** It changes the top-level field and appends a new system message with the new level. Earlier effort messages stay in history, so the cached prefix survives.
  - The transcript, the hook and the wire agreed on all 44 requests.
- **Cache at the switch:** 0.9999 at turn starts; 1.0 (Opus) and 0.9993 (Fable) mid-turn. A switch request writes about 93–147 tokens to the cache, the same as an ordinary step.
- **Thinking follows the level.** On the same puzzle:
  - Opus: hook xhigh about 2× hook low (1,279 vs 646; 1,436 vs 641).
  - Fable: weaker (3,368 vs 1,761; 1,336 vs 1,240).
  - Accuracy was 16/16, so the puzzles did not separate the levels on correctness.
- **The earlier "Fable barely thought" was the task, not a dropped effort.** On one-file reads both models think about 0 at every level, launch xhigh included.
- **Side requests (probably prompt suggestions)** carry the launch level, never the hook's. They are short and left the cache unharmed.
- **Still open:**
  - Which of the two fields the API honours: they always moved together.
  - Whether a top-level-only change would break the cache, as the docs say.
  - The cell size: n = 1 per model and level.

### 2.3 The prize

**Measured on Opus 5.5:**

- **Tool-only steps:** xhigh thinks 2.9× as much as medium (mean 711 vs 243 tokens) and writes 1.8× the output (about +560 tokens per step). Steps that end in text to you think little at every level.
- **Upper bound on the money:** about $85 of extra output from 2026-09-22 to 10-11 on xhigh tool-only steps in the main chat, plus about $21 on mixed steps. It is biased upward, because you choose xhigh for harder work.
- **Latency:** median turn 42 s at medium, 51 s at xhigh.

**Measured in the pilot toy repo:**

- **Output tokens:** fixed xhigh Opus used 37,131 output tokens and fixed low used 7,081. On the three non-design phases alone it was 6,709 against 2,273.
- **Correctness:** fixed low still passed every test, the planted bug included, so the toy did not separate quality.

**What that means:** the money is modest. The real prizes are:

1. Never sitting at medium through a design decision.
2. Quota and latency saved on long mechanical stretches.
3. Not having to track stages at all.

### 2.4 The pain, measured

- `/effort` runs in 7.5% of sessions, about 5 times on each day it is used, and 89 times mid-session.
- Mid-session you shift up 46 times and down 26 times.
- Opus 5.5 time at each level: 37% xhigh, 33% high, 30% medium, 1% low. A level, once set, stays for 1–2 hours.

### 2.5 Signals

| Direction | Cue | Strength |
| --- | --- | --- |
| Up | Test-failure output in a tool result | The next request thinks 1.3–1.6× as much, at every level |
| Up | "think" (×1.5–2.6), "design" (×1.4–1.9), prompt length (ρ 0.1–0.5) | Mild; small cells |
| Down | Prompt under 60 characters | ×0.5–0.7 at every Opus 5.5 and Fable level |
| Noise or backwards | A tool's `is_error` (less thinking follows), "plan", "decide", "bug", "review" | Unusable |

### 2.6 The pilot

| Start level | Tool shown as | Upshift on design or debug | Downshift on mechanical work |
| --- | --- | --- | --- |
| low (Opus and Fable, 6 chats) | deferred v1/v2 | never called | n/a |
| low | v3, with a per-turn reminder | Opus: no attempt. Fable: 2 malformed attempts (`effort` sent instead of `level`) | n/a |
| xhigh (Opus and Fable, 6 chats) | deferred v1/v2 | n/a | never called |
| xhigh | v3, with a per-turn reminder | n/a | Opus and Fable both lowered effort mid-turn in the rename, with exact reasons |

- No thrash anywhere.
- The v1-vs-v2 description comparison is **unmeasured**. The tool was deferred behind ToolSearch, so the model saw only its name.
- The test chats ran with the fleet prompt, so one reason quoted its "fleet floor is medium" rule.

## 3. Design

### 3.1 Shape

A plugin named `auto-effort`, in its own repo, plugged in as a submodule like `agent-effort`. It uses function hooks on the **main loop only**.

`agent-effort` keeps the sub-agents. The two split on `agentId`, so they never touch the same request and their hook order does not matter.

| Hook | What it does |
| --- | --- |
| `session.start` | Registers `set_effort` with `isDeferred: false`, loads the model gate, pins the status line. |
| `prompt.submit` | Opens the turn: the level is set to the planning level, `high`, or `xhigh` when the prompt carries "think", "design" or exceeds ~1,500 characters. Adds one context line naming the level and the tool. A change you made with `/effort` since the last turn becomes a pin. |
| `turn.step` | Main loop only: sends the turn's current level. After a switch, feeds the cache canary with the previous request's usage. |
| `tool.call` (`set_effort`) | Accepts `level` or `effort`, clamps to [floor, ceiling], applies from the next request, logs the reason, updates the status line. |
| `tool.call` (any tool's result) | On the second failing test run within a turn, raises the level one step (up to the ceiling) and adds a context line saying so. |
| `turn.complete` | Writes a turn summary to the decision log. |

### 3.2 The decision rule

1. Every user turn starts at the planning level for its first request.
2. On that request the model calls `set_effort` alongside its first tool calls, at no extra round trip. When the answer itself needs deep thought and there is no tool work, it calls the tool first and answers after the result: one cache-safe round trip.
3. The level holds until the model changes it, an escalation fires, or the turn ends.
4. If the model makes no call, the turn stays at the planning level. The rule errs toward quality.
5. Your own `/effort` mid-session is a pin: auto-effort steps aside until `/auto-effort on`.

### 3.3 Tool description, draft

```text
set_effort: set your reasoning effort for the requests after this call, until this turn ends.
Call it in your first response of every user turn, alongside your first tool calls.
- xhigh: a decision others will build on (architecture, design trade-offs, a spec, a review verdict),
  or a root cause one attempt did not find.
- high: debugging a known symptom, writing non-trivial code from scratch, explaining unfamiliar code.
- medium: edits whose every line the plan already fixes (a rename, a move, a known fix), running and reading tests.
- low: lookups, status reports, formatting, one-line edits.
Raise it the moment the work turns harder than planned: a test fails for a reason you cannot name,
or a fix you were sure of did not hold. Lower it once only mechanical steps remain.
If the answer itself needs deep thought and there is no tool work, call this first and answer after its result.
```

### 3.4 Model gate and cache canary

- **The gate:** the table in § 2.2. A model on probation runs normally and graduates after 5 clean switches. An unknown model stays off unless the config allows it.
- **The canary**, after every switch:
  - It compares the next request's cache read with the previous request's whole prompt.
  - Below 0.9, inside the TTL and with no compaction in between, it marks the model unsafe in plugin state, freezes at the session level, and shows one toast.
- **Its broken state:** the canary tests the cache only. It cannot see whether the effort reached the wire, which is why Phase 0 comes first.

### 3.5 Visibility and record

- **Status line:** `effort auto: medium · mechanical rename (model)`. The source is one of model / escalation / pin / plan.
- **Decision log:** JSONL under `~/.local/state/auto-effort/`, one row per decision and per switch with the canary ratio. It records no message content.

### 3.6 Config

| Key | Default | Note |
| --- | --- | --- |
| `floor` | `low` | Your words: "low/medium when doing regular work". The fleet's dispatch law says "Low never" for sub-agents; set `medium` to match it. |
| `ceiling` | `xhigh` | `max` only on your explicit say. |
| `planLevel` | `high` | The level for each user turn's first request. |
| `escalateAfterFailures` | `2` | Failing test runs within one turn. |
| `models` | the § 2.2 gate | Per-model ALLOW / PROBATION / DENY overrides. |

## 4. Rejected alternatives

| Alternative | Why not |
| --- | --- |
| The model self-regulating from its current level | 0 upshifts from low in 6 pilot chats |
| A detector for rising thinking tokens | Suppressed at low; the hook's usage has no thinking count |
| A prompt-keyword classifier as the decider | Lifts of ×0.25–×2.6 on cells of 15–60; kept only as the planning-level prior |
| `$.command.run('/effort')` as the actuator | Queued until idle, so it cannot act mid-turn, and every switch saves your default |
| A classic hook writing `settings.json` | Effort settings are read only at startup |
| A reload or restart (`pfm chat reload --effort`) | Rewrites the whole prompt prefix on every model |

## 5. Plan

1. **Phase 0, wire truth: DONE 2026-10-11, outcome (a)** (§ 2.2).
   - 4 test chats, at most 2 at once.
   - Requests were recorded by a private model-gateway instance on its own port. Your settings and your live gateway were untouched.
   - Raw data is in `evidence/phase0/`.
2. **Phase 1, pilot v2 (about 12 test chats, at most 3 at once).**
   - The tool is always loaded and the first request runs at the planning level.
   - Use a toy repo where effort changes correctness, so fixed-low must fail somewhere.
   - Judge the decision records blind.
   - Test chats get their effort from launch flags only, never `/effort`.
3. **Phase 2, build** through `flights-foreman`, in agent-effort's layout (`hooks/`, `src/`, `tests/`), with the decision core unit-tested.
4. **Phase 3, dogfood a week on Opus 5.5.** Success means:
   - your manual `/effort` count drops from its baseline of 2.0 per active day;
   - the cache ratio is ≥ 0.95 at every switch;
   - mechanical tool-only steps run at ≤ medium;
   - no design turn runs below high (spot-checked).

## 6. Side findings

- **Your Opus 5.5 default changed.** `modelSettings.claude-opus-5-5.effortLevel` now reads `medium`; it was `xhigh` when this session started. The file was last written at 15:44:43 local. The experiments never sent `medium`, so the source is not established. Restore it with `/effort xhigh` in an Opus 5.5 chat if that was not you.
- **`/effort` in a test chat writes the shared settings** (`~/.cc/{1,2,3}/settings.json` → `~/.claude/settings.json`). A test harness changes effort through launch flags only.
- **agent-effort's README is wrong about Haiku.** It says Haiku takes no effort setting. That is false for `claude-haiku-5-5` on 2.1.296: the hook saw `effort: "low"`, and `/effort high` changed it.
- **`pfm chat ask` appends a "to reply: call {parent}" line,** and a test chat followed it into the main chat.
- **My brief capped test chats at 40 in total but set no concurrency limit.** 13 launched at once, the load average hit 137, and the run was killed. The lesson is filed in `.professor/retro.md`.
