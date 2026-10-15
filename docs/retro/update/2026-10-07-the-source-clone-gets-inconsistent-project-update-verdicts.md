# 2026-10-07 — the source clone gets inconsistent project-update verdicts

Observed: after the successful v0.79.0 update, the updater printed `NOT-MANAGED` and explained that a missing `.professor/baseline.json` is expected in the Professor source clone. Running the recommended `pfm doctor --project-updates` in that same clone exited 3 with `FAILED` and suggested `pfm update adopt` or `pfm init`. No baseline was created: this was the framework source, not an adopted project.

Amend: make the source-clone distinction consistent across the updater and standalone project report. An adopted project with a missing baseline must still report its real failure; the source clone should not be steered into scaffolding or adoption. Add a paired source-clone/adopter fixture and preserve exit/output parity for both commands.
