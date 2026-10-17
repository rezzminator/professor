# Live and visual work

The imagine core's rules for live and visual work, read when the core sends you here.

Binds a work its receiver meets as a page, canvas or scene — an Artifact page, a Design canvas artboard, a self-contained HTML page, a 3D or canvas scene; code, prose and analysis skip it.

Target, before the first file:

- Prove the target takes the work: `Artifact` deferred loads by `ToolSearch` `select:Artifact`; absent or denied, the settings file and line that deny it are named and the work stops there, since a local HTML file is never delivered as an Artifact. `/design` is user-only; the Skill tool refuses it.
- Load `artifact-design` and `dataviz` before choosing palette and type; their page layouts, chart forms and dashboards yield to the core's one living view. `artifact-capabilities` when the page needs runtime data or state.
- Where the brief leaves the target open, the page's nature fixes it: a script-drawn, WebGL or canvas scene goes on a plain Artifact page. A Design canvas takes markup artboards: read the type's own reply and rule files before the first file and obey them — its `<x-dc>` markup, no script-built UI, no network but one Google Fonts link and `/_blob`, no iframe, no global keydown (keys scoped to the app root), libraries bundled inline since its frame's CSP is `default-src 'none'`.
- Limits, unless the target states others: a page or text file 16 MB; a publish 255 files, 64 MB; a Design artboard syncs only up to 240 KB inline, 2 MB as an uploaded blob, and a larger one is dropped silently — the canvas reads "no artboards yet". Scripts from cdnjs, `cdn.jsdelivr.net/npm`, unpkg, tailwind or jquery only, each at an exact version at least 14 days old; stylesheets from Google Fonts only.

Make:

- The page is the core's one living view: it fills the viewport and never scrolls to a second view; every width re-frames that same view, never stacks it into parts; a Design canvas holds it on one artboard, any further artboard showing it at another width.
- Data past a few hundred KB, and every bundle a Design artboard would carry, goes up as an asset (`Artifact` with `asset: true`) and is fetched by the `/_blob/<id>` returned, verbatim; the page stays near 100 KB and a heavy record splits into a small main asset plus shards the view loads when it needs them.
- Every fallback and error state prints its cause into the page, and each load's look reads it: a page that silently fell back to its fallback looks like a working page.
- Every animated transition restores the committed view when it throws; after a change, a transition pass drives each one.
- Traps met before: `</script>` inside inline JS is written `<\/script>`; an ES module namespace (`import * as THREE`) is copied before it is extended; dense overlapping points accumulate in half-float and tone-map keeping hue, or they burn to white; in `.dc.html` no `{{` anywhere, `sc-for` children keyed by index (fixed-length pools), labels declared in markup and only moved, and a global button rule written `:where(.root) button` so it never outranks a button class.

Look:

- Where the `agent-browser` CLI is installed, through Bash: `agent-browser close --all` first, then `--session {name}` on every call, each under `timeout`: `set viewport {w} {h}`, `open file://…`, `wait`, `screenshot {path}` then a Read of the PNG, `eval`, `errors`, `console`, `click`, `mouse move`, `press`, `set media dark|light|reduced-motion`. Widths 1440, 1024 and 390, light, dark and reduced motion. Without it, any real browser the session can drive; with none, the look is reported unseen per the core's § Deliver.
- Animation is judged in real time with real pointer events: a seeked frame hides a frozen keyframe and a jumped clock spikes a glow.
- Headless Chromium renders WebGL in software: bloom, pixel ratio and frame rate seen there are unverified for a real GPU. The 3D scene and its no-WebGL fallback are two paths, each looked at.
- A local harness standing in for the target (a Design artboard served with the type's `dc-runtime.js` as `./support.js`, `/_blob/` mapped to local files) proves the page's runtime, never the editor or the viewer.

Publish:

- The `Artifact` tool publishes from the working directory or the scratchpad only: copy the built files there first.
- Read the Artifact immediately before each publish; a version published since you last read it is merged, never overwritten.
- A Design canvas publishes in the order and batch size its type states.
