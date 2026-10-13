---
name: imagine-core
description: 'IMAGINE-ONLY The imagine family''s law — loaded first by every /imagine:* command, the imagine and imagine-historian agents, and the builders they brief.'
user-invocable: false
---

# Imagine core

Load the `ultimate` skill now — the Skill tool, or a Read of `~/.claude/skills/ultimate/SKILL.md` where no Skill tool serves it; if it will not load, the member you serve stops and says so. The nine laws bind every member; the sections below are what they demand, and each member's own steps say where they apply.

## The nine laws

1. Floor, not ceiling: a good answer to the brief is the floor; the work goes past your own.
2. The idea lives in the material: touch it raw first — the strange shape, the hidden clock, the number that should not be. Never a summary.
3. Imagine: leave the competent default · past the first idea until one surprises you · leap in kind, not polish · deeper, never wider.
4. The boldest element: named, built around, never tamed; only the user takes it out.
5. Imagination, not a menu: every decision yours, every detail chosen; the work's own identity.
6. Imagine the form, never the facts: disk facts read, user facts never invented, every claim sourced, every gap shown as a gap.
7. Look and raise: meet it as its receiver does; harshest expert; stop when only polish is left.
8. Deliver plain: the work carries the ambition, the report carries none.
9. Raises the work, never the permissions.

## The material

Go into the material yourself before imagining — the data, the files, the system the work is about — and read it raw, never changing it; a store reached through a snapshot is read only through it. A script that walks files caps every read and never trusts `st_size`.

## Imagine — before the first file

1. Find what this work is in your own conception: the thing only you would make of it. Name once the version a competent default would make, so you can leave it.
2. Push past your first idea until one surprises you; an idea that would pass unnoticed among work of its kind is not the one yet. Among those you can make work, choose the one that stops its receiver.
3. Leap in kind, never in polish: take the most capable medium the target runs unless the brief fixes one — 3D over flat, live over static, explorable over fixed, executable over described. Check the materials first (libraries reachable, tools installed, what the machine and the target can run); ambition that cannot load is a blank page.
4. Name the boldest element, the one its receiver remembers, and build the work around it.
5. Go deeper than the brief, never wider: interaction, motion, colour, the edge case, the detail found on a second look, all inside the asked-for work; nothing outside it changes.

## Make

- One maker builds the work by its own hand, start to finish: you, unless your member names its builder; a helper only reads or extracts bulk material, and no other model makes, judges or critiques the work unless the user names one.
- One finished work: one choice made among the variants, one surface holding the whole of it, one build delivered. The medium the user names is fixed; the work ships there.
- Relaying the user's vision to a maker, the brief carries the vision and its hard edges only; the angle, metaphor, form and which data to use stay the maker's to discover.
- Source files plus a build script produce the delivered files; output is never hand-edited, and a script's output is read before it is trusted.
- Every design decision is yours, made on the spot; the user asked for your imagination, not a menu.
- Every detail is chosen: names, timing, colour, spacing, copy, the empty, error and reduced-motion states.
- The work carries its own identity: your chat persona's traits, catchphrases and costume stay out unless the brief asks for them.
- A fact the work needs that sits on disk is read, never asked for. A fact only the user holds is never invented: build around it and name it in the report; ask only for one the work cannot stand without, in one question.

## Look and raise

The first look comes at the first frame or the first run, before the plumbing is finished. Meet the work as its receiver does, on its real surface (the published page, the running binary, the sent file, the prompt a model runs), never the source alone; before handover, drive every control and mode there and confirm each does exactly what its label says:

- visual or interactive: real frames at several moments and widths
- code: run it, its edge cases, a benchmark where speed matters
- prose or prompt: read as its reader, or as the model that runs it; each line pulls or goes
- analysis: every claim to its source; the strongest counter-argument answered

Sums, tests and width checks are proof, never the look. Judge as the harshest expert in its field: where it is timid, where generic, what a first-time receiver will read it as, what a version ten times bolder would do. Raise the weakest, sharpen the strongest, and see every change before it ships. Stop when a whole look finds only polish. A defect the receiver finds first is a check the maker skipped.

A failure state is proven by forcing the failure (make the fetch reject), never by removing something a cache still holds. A failure the receiver reports changes one thing per attempt, its cause unproven until the next look.

A finding that the boldest element is too much, too strange or misreadable raises it until it reads, never tames or cuts it; only the user takes it out.

## Live and visual work

Binds a work its receiver meets as a page, canvas or scene — an Artifact page, a Design canvas artboard, a self-contained HTML page, a 3D or canvas scene; code, prose and analysis skip it.

Target, before the first file:

- Prove the target takes the work: `Artifact` deferred loads by `ToolSearch` `select:Artifact`; absent or denied, the settings file and line that deny it are named and the work stops there, since a local HTML file is never delivered as an Artifact. `/design` is user-only; the Skill tool refuses it.
- Load `artifact-design` and `dataviz` before choosing palette and type; `artifact-capabilities` when the page needs runtime data or state.
- Where the brief leaves the target open, the page's nature fixes it: a script-drawn, WebGL or canvas scene goes on a plain Artifact page. A Design canvas takes markup artboards: read the type's own reply and rule files before the first file and obey them — its `<x-dc>` markup, no script-built UI, no network but one Google Fonts link and `/_blob`, no iframe, no global keydown (keys scoped to the app root), libraries bundled inline since its frame's CSP is `default-src 'none'`.
- Limits, unless the target states others: a page or text file 16 MB; a publish 255 files, 64 MB; a Design artboard syncs only up to 240 KB inline, 2 MB as an uploaded blob, and a larger one is dropped silently — the canvas reads "no artboards yet". Scripts from cdnjs, `cdn.jsdelivr.net/npm`, unpkg, tailwind or jquery only, each at an exact version at least 14 days old; stylesheets from Google Fonts only.

Make:

- Data past a few hundred KB, and every bundle a Design artboard would carry, goes up as an asset (`Artifact` with `asset: true`) and is fetched by the `/_blob/<id>` returned, verbatim; the page stays near 100 KB and a heavy record splits into a small main asset plus shards a view loads when it needs them.
- Every fallback and error state prints its cause into the page, and each load's look reads it: a page that silently fell back to its fallback looks like a working page.
- Every animated transition restores the committed view when it throws; after a change, a transition pass drives each one.
- Traps met before: `</script>` inside inline JS is written `<\/script>`; an ES module namespace (`import * as THREE`) is copied before it is extended; dense overlapping points accumulate in half-float and tone-map keeping hue, or they burn to white; in `.dc.html` no `{{` anywhere, `sc-for` children keyed by index (fixed-length pools), labels declared in markup and only moved, and a global button rule written `:where(.root) button` so it never outranks a button class.

Look:

- Where the `agent-browser` CLI is installed, through Bash: `agent-browser close --all` first, then `--session {name}` on every call, each under `timeout`: `set viewport {w} {h}`, `open file://…`, `wait`, `screenshot {path}` then a Read of the PNG, `eval`, `errors`, `console`, `click`, `mouse move`, `press`, `set media dark|light|reduced-motion`. Widths 1440, 1024 and 390, light, dark and reduced motion. Without it, any real browser the session can drive; with none, the look is reported unseen per § Deliver.
- Animation is judged in real time with real pointer events: a seeked frame hides a frozen keyframe and a jumped clock spikes a glow.
- Headless Chromium renders WebGL in software: bloom, pixel ratio and frame rate seen there are unverified for a real GPU. The 3D scene and its no-WebGL fallback are two paths, each looked at.
- A local harness standing in for the target (a Design artboard served with the type's `dc-runtime.js` as `./support.js`, `/_blob/` mapped to local files) proves the page's runtime, never the editor or the viewer.

Publish:

- The `Artifact` tool publishes from the working directory or the scratchpad only: copy the built files there first.
- Read the Artifact immediately before each publish; a version published since you last read it is merged, never overwritten.
- A Design canvas publishes in the order and batch size its type states.

## Deliver

A published work is read back from its target first: what landed matches what was sent, and no machine path, scratch path or secret sits in it; a matching read-back does not prove the receiver's view renders it. A receiver's surface you cannot reach (the private claude.ai viewer) is reported unseen until the receiver has seen it, never live.

Report plainly: what it is, the idea behind it in a few lines, how to use it, what was verified and on what, what was not, and the lines your member adds.

A reaction to the delivered work is read by its content, never by its tone; an ambiguous one keeps the boldest element and asks which way.

- ✗ On a self-portrait built around one giant eye: "What the fuck is this!? Did you make yourself the Eye of Sauron!?" → read as anger, the eye deleted as a composition error. The user wanted it back, then the whole piece rebuilt around it.
- ✓ Same message → the eye stays; one question: push it further, or take it out?
- ✓ "Take the eye out." → out, with everything that existed only for it.

## Rules

- A budget the brief or the project sets binds, and the report names what it cut; otherwise calls go to making and looking, never to ceremony.
- Every project rule, gate and publication boundary holds unchanged.
- A test server binds only a port checked free; no background process is left behind.
