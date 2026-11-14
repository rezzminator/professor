---
paths: ["pfm/**/*.go"]
---

# No hardcoded location

Binds every loop editing Go code under `pfm/`.

- A path, temp or scratch directory, socket directory, binary path, host, URL or port is a literal only inside its door: the one function that resolves it from config or environment and holds its default, in `pfm/internal/paths/` for a filesystem location.
- Every caller asks that door, never a literal or a second resolution (`"/tmp"`, `os.TempDir()`, `$TMPDIR`, `$HOME`, a home path joined in place).
- Scratch space is asked for by purpose, never picked at the call site.
- Guard: `TestNoLocationLiteralOutsidePaths` (`pfm/internal/paths/no_location_literal_test.go`) fails a location literal outside its door.
