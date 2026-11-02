---
name: extension-surface
description: 'Opens closed systems to plugins — "make X extensible", "design a plugin or hook API", "is our extension API sound"; `design <system>` lays out the surface, `audit <system|path>` grades an existing one lever by lever. Returns a surface spec or a graded gap table. Not for writing a Claude Code mod → plugin-authoring.'
argument-hint: "design <system> | audit <system|path>"
---

# Extension surface

Ten levers decide how far outsiders reach into a system without touching its internals: `design` answers each lever's questions for the target, `audit` grades each against the target's code. The worked example is Claude Code's function hooks ("Mods"); `mods-example.md` beside this file holds its evidence per lever, each cited to a public source.

## Modes

- `design <system>`: read the system first — its side effects, its existing extension points, the parties who install and administer plugins. Answer every lever's design questions for this system, then write § Output › Surface spec.
- `audit <system|path>`: read the API's code, type declarations, docs and shipped plugins. Grade each lever MET, PARTIAL, MISSING or N/A with evidence (`path:line` or URL) and the gap a missing lever leaves open, then write § Output › Audit.
- No mode given: `audit` where the system already has a plugin, extension or hook API, `design` where it has none.

Every answer derives from the target's own code. The Mods example shows one solution per lever, never the required one; a lever that cannot apply (no administering party, say) is N/A with its reason.

## The levers

### 1 One door for every effect

Plugin code holds no ambient authority: every side effect it can cause (file, network, process, clock, model call, drawing on screen) is a call on one object the host hands it, so what a plugin did is exactly the calls it made there. The no-ambient boundary is the contract; the mechanism enforcing it (a worker, a VM realm, WASM, a separate process) stays private. The surface makes effects visible, not forbidden: what is allowed is the administrator's policy, applied through the same hooks.

- Design: list the system's effects as `noun.event`. Which sandbox removes every other route? What does a plugin in another language call?
- Audit: find any route to an effect that bypasses the object (a direct import, a global, a raw handle passed in).
- Gap: whatever a plugin does off the object escapes audit, allowlist and deny, so every later lever is advisory.

### 2 One chain per event

Each event runs one middleware chain, every hook shaped `(world, event, next)` and nesting outer around inner. One wrapping hook serves where a pre/post pair would: before, after, during (start `next` and work alongside it), instead (answer without calling `next`), modifying (pass `next` a copy). Calling `next` zero times blocks or answers, once passes, more than once retries. The return value is what the caller is told happened, separate from whether the effect ran: to stop an effect, a hook does not call `next`. The events plugins hook are the same calls plugins and the host's own features make, so every affordance is hookable.

- Design: per event — its name, its input as an immutable plain value, its result, whether it streams (a stream hooks as a generator that yields), what the chain's bottom does when reached.
- Audit: separate before and after hooks; a hook that cannot block, modify or retry; mutable events; a hook re-entered from inside its own dispatch (keep invocations linear and terminating); several spellings for one operation.
- Gap: hooks cannot replace or retry an effect, and pre/post pairs share no state.

### 3 Order is policy

Plugins never choose their own position, and no numeric priority exists. Fixed tiers set order, outermost first, and the outer tier holds more authority because it wraps more. Inside the person's tier, order is their configured order merged with a dependency topological sort, never install time. Any skip-ahead primitive moves only toward less authority and is traced.

- Design: name the parties (organization, person, bundled features, core) and seat each as a tier. Which tier sees an event first, which last, and how does a person reorder their own?
- Audit: a priority number, a plugin-declared position, order by install time, or any path letting a person's plugin run outside an organization's.
- Gap: priority arms races, and a plugin that slips beneath the guard meant to wrap it.

### 4 Declared hookable, everything else sealed

The surface publishes its grammar (the primitive events and components) and the product components that may be wrapped, modified or replaced. Anything undeclared is unreachable by construction. Seal what guards trust: consent and permission prompts, credentials, billing.

- Design: list the hookable set and the sealed set, with the reason for each sealed item. Can an administrator switch off a whole class, such as UI hooks?
- Audit: is the sealed set enforced by not existing on the surface, or only by documentation? Can a plugin suppress or answer a consent prompt?
- Gap: a plugin forges or skips the prompts that hold the person's consent.

### 5 Failure isolation, fail-closed by code

The host skips a hook only when it throws, overruns its time budget, or passes or returns the wrong shape; the plugin author can prevent and detect all three. A skipped hook's place goes to the links beneath it, and the failure is reported by plugin name; the host never breaks. Fail-closed is opt-in code (a catch handler on the hook, rerun with a fresh grace budget, told the error and whether `next` was already called), never a config flag naming an error policy.

- Design: the budget per dispatch and what counts against it; what skipping means for each event; how a security hook fails closed.
- Audit: a plugin error or hang that crashes or stalls the host; no timeout; no runtime shape check; a guard plugin with no fail-closed path.
- Gap: one bad plugin takes the host down, or a guard fails open in silence.

### 6 Contract without source

Plugin authors get type declarations, a test kit and a validator, never the host's source. Declarations are generated from the surface and merge per plugin, so two plugins typing one event differently fail to compile. The test kit runs the plugin inside the host's real chain with mocks beneath it, and a call nothing answers throws naming its event. The validator refuses computed access (`world[expr]`), so every call a plugin makes is statically readable. Adding new nouns goes through a hook too, and the providing plugin owns that noun's types. Expose stable contractual events in place of raw internal files and formats.

- Design: how are declarations generated and shipped? What do tests mock, and where do mocks sit? What does the validator refuse?
- Audit: hand-kept type files that drift from the host, plugins testable only against a live host, dynamic dispatch allowed, plugins reading internal files.
- Gap: host releases break plugins silently, and lever 7's static reading becomes impossible.

### 7 Administrator control, observable at linear cost

Administrator controls are hooks themselves: an allowlist hooks plugin registration, adding nouns hooks surface construction, an audit log is one outermost wildcard hook seeing every event. At registration the host reads each plugin's uses (events hooked, calls made, settings read and written) from its source and gates on those host-derived facts, never on a plugin's self-declared name or version, which a rename walks past. Tracing and budgets ride on `next`, read without serializing per link, since per-link logging and hooks re-reading the whole history cost quadratic time; offer append-style events instead.

- Design: what can an administrator refuse before a plugin loads, and from which facts? How does one hook observe everything?
- Audit: gating by name alone, permissions a plugin declares and the host trusts, no pre-load reading of what a plugin does, observability whose cost grows with chain length times event count.
- Gap: administrators learn what a plugin does only after it ran.

### 8 First-party features ship as plugins

The host's own features run on the surface in the bundled tier, ideally with published source. A first-party feature that needs a private door marks an event the surface still lacks.

- Design: which existing features move onto the surface first, and what parity work does each need?
- Audit: list first-party features that bypass the surface; each is a missing event.
- Gap: third-party authors hit walls first-party code never meets.

### 9 Old extension points bridge one to one

Each existing hook or config extension runs as a wrapped event of the new chain, with the same input and output, seated where it ran before.

- Design: map every legacy extension point to its event and tier.
- Audit: old and new mechanisms running side by side in undefined order, or the old one dropped without a bridge.
- Gap: migration forces a flag day on every adopter.

### 10 Opening the surface regresses no guarantee

Whatever a person could not reach before (organization hooks, managed policy, deny rules) stays out of the person's tier. A default plugin seated outermost holds those guarantees, adds no policy of its own, and treats an unreadable policy as in force.

- Design: list every pre-existing administrator guarantee and the hook that keeps it.
- Audit: for each guarantee, can a plugin in the person's tier now bypass it?
- Gap: the new extension API becomes the bypass of existing administrator control.

## Output

### Surface spec

`Surface spec — {system}`, then one section per lever holding its decisions, plus:

- events table: `noun.event | input | result | streams | sealed or hookable`
- tiers, outermost first, each with its party and what it may skip
- sealed set, each with its reason
- failure table: event, budget, what a skip yields, the fail-closed path
- contract artifacts: declarations, test kit, validator rules
- open questions the system's owner must settle

### Audit

`Extension surface audit — {system}`, then a table `lever | grade | evidence | gap left open`, then numbered findings, worst first. A promise the code does not keep (a documented protection with no enforcing code) ranks first. A source that could not be read is `NOT READ: {path} — {why}`, never MISSING. Close with `VERDICT: {n} MET, {n} PARTIAL, {n} MISSING, {n} N/A`.

## Examples

- `design a plugin API for our build server` → events such as `job.start` and `artifact.upload`; tiers `org → user → builtin → core`; secret injection sealed; per-dispatch budget 2 s; every built-in step re-cast as a builtin plugin.
- `audit src/extensions/` → lever 3 MISSING: `priority: number` on registration (`src/extensions/registry.ts:40`), so any extension can seat itself outside the admin's audit hook.
- `audit our webhook system` → lever 2 PARTIAL: a receiver can observe an event but not block, modify or retry it; lever 5 MISSING: a slow receiver stalls the dispatcher.

## Routing

- Writing a Claude Code mod → the `plugin-authoring` skill.
- Packaging a Claude Code plugin → <https://code.claude.com/docs/en/plugins>.

Lever 1 carries the rest: an effect that reaches the world off the one object escapes every other lever.
