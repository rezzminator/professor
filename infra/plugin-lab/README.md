# plugin-lab

Runs a plugin checkout in a real, isolated, metered Claude Code session on the host, drives it through a scenario and prints one pass/fail row per check.

```bash
infra/plugin-lab/lab list
infra/plugin-lab/lab run forward-compaction/logger-ready --plugin {checkout}/plugins/forward-compaction --ledger {flight dir}/lab-ledger.tsv
```

- `scenarios/{plugin}/` holds the scenarios and `gaps.tsv`; `lib/` the lab; `hooks/mark.sh` the session marks; `tests/` the hermetic self-tests (fence step `templates.plugin-lab`).
- Keep the plugin tree quiet during a run: Claude Code hot-reloads a `--plugin-dir` on any edit, and `passport/identity` FAILs `tree-changed` on it.
- Isolation, reports, writing a scenario, the verbs and adoption: [docs/dev/testing/plugin-lab.md](../../docs/dev/testing/plugin-lab.md).
