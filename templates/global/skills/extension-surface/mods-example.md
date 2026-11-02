# Worked example: Claude Code function hooks ("Mods")

How Claude Code's function hooks meet each lever of `SKILL.md`, quoted from public sources. Read a lever's section when its design or audit needs a concrete precedent.

## Sources

- [FH]: "Function Hooks: Core Architecture", Anthropic, August 2026 — <https://github.com/user-attachments/files/31802150/EXTERNAL.Function.Hooks.Core.Architecture.pdf>
- [ISSUE]: the feature author's replies on anthropics/claude-code#91870 — <https://github.com/anthropics/claude-code/issues/91870>
- [MODS]: the built-in mods' source, published as built into the binary — <https://github.com/anthropics/claude-code/tree/main/mods>
- [TYPES]: the plugin type declarations — <https://github.com/anthropics/claude-code/blob/main/mods/types/claude-code.d.ts>
- [SEC]: the `sec-default` mod — <https://github.com/anthropics/claude-code/tree/main/mods/sec-default>

Vocabulary: `$` is the effect object (the world), `e` the event, `next` the rest of the chain for this dispatch, a mod a plugin whose behaviour lives in a hooks module exporting `register(on, options)`.

## 1 One door for every effect

- `$` is "an object of nouns, each an object of events, `$.<noun>.<event>(input)`, and it's the only door: the environment that runs a plugin's hooks has no ambient file system or network, so what a plugin did is exactly the calls it made on `$`." [FH §4]
- The boundary is a Bun Worker around the plugin realm with a `node:vm` wrapper per plugin; "that's not part of the contract though, the contract is merely that you don't get access to ambients." [ISSUE]
- Mechanism, not policy: "Our prerogative is not to restrict what plugins can do; that's your org admin's job. The point is that there's no _ambient_ support, everything goes through `$`." [ISSUE]

## 2 One chain per event

- "A hook is Koa-style middleware" with the signature `($, e, next)`, the plugin registered first wrapping every later one. [FH §2, ISSUE]
- The host's own call sites go through `$` too ("the query loop calls `$.tool.call`"), so "Events are `$` and vice-versa." [FH §4]
- Five placements on one event — before, after, during (`const pending = next(e)`, work alongside), instead (`return { deny: "no tools allowed" }`), modifying (`return next({ ...e, timeout: 30 })`) — "One event therefore serves where command hooks needs a pre-event and a post-event". [FH §2.2]
- `next` called twice "is completely supported; for example if you want to do a retry, backoff, etc." [ISSUE]
- Return value versus effect: "To make the tool not get called, don't call `next(e)`. The return value is what we tell the model happened - when you hook onto `tool.call`, you are _responsible for calling the tool_." The host's denial test is `const approved = result.deny === undefined`. [ISSUE]
- "Events are immutable plain values: to modify, you must pass next a copy with a changed field." [FH §3.1]
- Streams: `turn.step`, the one streaming event, is hooked as an async generator whose budget counts only while its own code runs, "never at a `yield`". [TYPES]
- Recursion: "A hook is never re-entered while its own frame is being dispatched ... to keep the total number of hook invocations ~ linear and terminating." [FH §6.4]
- One spelling: "I would rather minimize sugar / multiple spellings and have folks deal with `next` directly." [ISSUE]

## 3 Order is policy

- Tiers, outermost first: `prepend`, `user`, `append`, `builtin`, `core`; `prepend` and `append` are administrator-managed plugins, `user` the person's installs, `builtin` the bundled mods. [TYPES]
- "mods cannot dictate their own registration order, full-stop." [ISSUE]
- "every 'numeric priority' system in the world has failed on this, which is why I don't introduce any number levels at all", citing Raymond Chen: <https://devblogs.microsoft.com/oldnewthing/20050607-00/?p=35413>. The user tier is "some sort of deterministic merge of the user-supplied order and the order-per-topological-sort on a plugin-dependency level." [ISSUE]
- Skip-ahead is `next.to(e, tier)`, for managed hooks only: "prepend may name append, builtin or core, append core; it never skips a tier with more authority, so no user hook skips the org's ... skipped links are traced." [TYPES]

## 4 Declared hookable, everything else sealed

- The surface declares its grammar (primitive building blocks) "as well as a set of _product components_ that can be wrapped, modified, replaced, etc. So, the answer is that our internal 'permission request component' is simply not something you can hook into, because the surface does not declare it as being hookable." [ISSUE]
- Render hooks see component names and props, which are public API declared per component. [FH §3.2]

## 5 Failure isolation, fail-closed by code

- "the engine will _only_ skip you in three cases: 1. You throw 2. You took too long 3. You returned the wrong data shape, or passed in the wrong data shape to `next`. All three of these are within your control to prevent and detect." [ISSUE]
- A failed hook "is skipped: the hooks beneath and core run in its place, or its last `next` result stands; the failure is reported by name." The budget is 10 000 ms per dispatch. [TYPES]
- Fail-closed is `on(...).catch(handler)`: the handler runs "afresh when it throws, misreturns or overruns", its `next` carries `error` and `called` and "is replay-safe". [TYPES]
- A config-level error policy was declined: "`{ onError: "deny" }` I'm still not convinced. I don't believe in representing code as JSON." [ISSUE]

## 6 Contract without source

- Mods are written "against the declarations `/plugin-types` writes"; `claude plugin test mods/diff` runs a test file where hooks "the test registers with `on` sit beneath the mod, where the rest of the world would be, and nothing is beneath them: a call they leave unanswered throws"; `mock.env`, `mock.store` and `mock.clock` answer the world from memory. [MODS README]
- Types merge into one engine interface, so two plugins typing one event two ways do not compile; new nouns are added by hooking `engine.create`, the surface's own construction chain. [FH §4.1, §4.3]
- "A call is always spelled `$.noun.event(...)`; a module that manipulates `$` any other way fails `claude plugin validate`." [FH §4]
- Contract over internals: raw transcript files "can be very large and technically do not form a stable interface. ergo, I would want to default to making that information available on an actually contractual interface." [ISSUE]

## 7 Administrator control, observable at linear cost

- `plugin.register` hands a gate each plugin's `tier` and `uses` (events hooked, calls made, env reads and writes), read from its source: "`tier` and `uses` are the host's reading of the module, what a gate holds it to; `name`, `root`, `version` and `provenance` are its own word ... so a rule keyed on them is one a rename walks past." [TYPES]
- "everything goes through `$` so that admins can audit, allowlist, deny, log, etc. any event"; an allowlist is a hook on the event that registers plugins, and "An audit log: one prepended hook on every event" (`on("*", …)`). [ISSUE, FH §2.3, §5, §6.3]
- `next.trace` carries timing and event and result snapshots for every link below; the engine already holds that data immutably, "so we can provide it without imposing a $N^2$ serialization cost." Reading it is logged at validation as usage metadata an administrator can refuse. [ISSUE, TYPES]
- Quadratic traps: hooking every message on every request means "your total amount of work will be quadratic", so `session.append` represents "appending something to the session". [ISSUE]

## 8 First-party features ship as plugins

- "These four ship inside Claude Code; this folder is their source, published as it is built into the binary": `sec-default`, `diff`, `telemetry`, `agents-md`. [MODS README]

## 9 Old extension points bridge one to one

- `classic.PreToolUse` and the other `classic.*` events wrap the settings-file shell hooks with the same input and output; managed-settings hooks run first, other settings hooks as core. [TYPES]

## 10 Opening the surface regresses no guarantee

- An organization's controls "never within a person's reach before function hooks; seated outermost, this plugin keeps exactly those out of the user tier's reach and adds no policy of its own." [SEC]
- Three moves only: continue past the user tier (`next.to(e, "append")`), refuse a user-tier caller by name, or pass; policy reads "fail closed, so an unreadable policy counts as a policy in force." [SEC]
