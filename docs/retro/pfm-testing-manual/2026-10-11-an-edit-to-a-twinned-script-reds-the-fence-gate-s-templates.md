# 2026-10-11 — an edit to a twinned script reds the fence gate's templates.clone

Observed: the format-md-nested flight edited `.claude/scripts/format-md.sh` and its byte-identical twin `templates/project/scripts/format-md.sh`; the known twin clone took a new jscpd fingerprint, so `iso gate` reported it NEW and failed `templates.clone` on its first run. `scripts/clone-check.sh --measure` swapped the one fingerprint (14 clones before and after) and the second run passed.

Amend: .claude/commands/pfm-testing-manual.md — an edit to any twin pair (a `.claude/**` file and its `templates/project/**` twin) runs `scripts/clone-check.sh --measure` in the same pass and names the `.jscpd-baseline.json` swap in the commit message.
