# 2026-10-12 — /ultimate-spec shipped a 7 MB Design artboard that the editor silently dropped

Observed: the build was published to a Design canvas as one inline artboard (three.js, data and app, 7.3 MB). The editor syncs an artboard only up to 240 KB inline (2 MB as an uploaded blob), so the canvas opened with "no artboards yet" and the user found it by screenshot; a local test under the runtime and the preview CSP had passed. The fix was a 16 KB artboard loading two uploaded script assets by `/_blob/<id>`.

Amend: templates/global/commands/imagine/build.md#U5 — the builder's brief carries the target's own limits (an editor's per-file sync cap, a frame's CSP) and the build is met on the real target, not a stand-in, before its first publish.

Resolved: 2026-10-17 — templates/global/skills/imagine-core/SKILL.md § Build through imagination (the dispatch carries the target with its publish route, size and sync caps, frame CSP and script hosts; /imagine:build U5 and /imagine:build-fast both run it) and templates/global/skills/imagine-core/live.md § Publish (the first publish is met on the real target before the full build goes up; a stand-in that passed proves nothing there).
