---
paths:
  - "**/CLAUDE.md"
  - "**/.claude/**"
visible-to:
  include: ["main"]
---

- Only the `pcm` agent writes a `CLAUDE.md` or a `.claude/**` file, through any tool, `sed -i` and shell redirects included; everyone else hands `pcm` the change, however small.
