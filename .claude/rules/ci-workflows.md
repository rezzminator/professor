---
paths:
  - ".github/workflows/**"
---

# One CI run, on the pull request into `main`, gates every release

Binds every loop writing a workflow under `.github/workflows/`; a push-started run goes unwatched while GitHub bills every minute.

- `ci.yml` = the one check: triggered by a pull request into `main`, it runs every layer on Linux and macOS.
- Its aggregate `ci` job is `main`'s only required status check: nothing merges, releases or publishes until it is green.
- `publish.yml` runs on that pull request's merge: it publishes and verifies the published assets, re-running no test.
- One `workflow_dispatch` workflow may run the full macOS suite between releases.
- No other workflow or trigger: never `push` (any branch or tag), nor `workflow_dispatch` on `ci.yml` or `publish.yml`.
