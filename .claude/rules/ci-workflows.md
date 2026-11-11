---
paths:
  - ".github/workflows/**"
  - "plugins/*/.github/workflows/**"
  - "templates/global/plugin-ci/**"
---

# One CI run, on the pull request into `main`, gates every release

Binds every loop writing professor's or a plugin's workflows, or their template; a push-started run goes unwatched while GitHub bills every minute.

- `ci.yml` = the one check: triggered by a pull request into `main`, it runs every layer on Linux and macOS; a repo that cannot run on macOS says so in its README and drops `macos-15` from its matrix with a one-line comment why.
- Its aggregate `ci` job is `main`'s only required status check: nothing merges, releases or publishes until it is green.
- `publish.yml` runs on that pull request's merge: it publishes and verifies any published files, re-running no test.
- Professor's own `ci.yml` also takes `workflow_dispatch` on any branch, its `exclude` input naming the jobs to skip; the `ci` job fails on a name no job carries.
- A plugin keeps `ci.yml` on the pull request alone and may add one `workflow_dispatch`-only workflow to run a full suite between releases.
- No other workflow or trigger: never `push` (any branch or tag), nor `workflow_dispatch` on `publish.yml` or a plugin's `ci.yml`.
- A plugin's `ci.yml` and `publish.yml` follow `templates/global/plugin-ci/` and stay self-contained: no reusable workflow, no checkout of another repo.
