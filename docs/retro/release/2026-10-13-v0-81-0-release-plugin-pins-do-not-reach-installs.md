# 2026-10-13 — v0.81.0 release: plugin pins do not reach installs

Observed: `pfm install` takes a plugin from its repo's main with no ref (pfm/internal/installer claude_plugins.go), so the submodule pin is decorative and a plugin main release changes every adopter at once.

Amend: judgment — pfm install should honour the pinned ref, or the release flow must treat a plugin main release as a publication.
