# 2026-10-11 — "beyond the run-to-run spread" passed every change on a deterministic target

Observed: the Jev classifier lab (`/iterative-improvement` on a TypeSafe Jev question) repeats a run to within ≤0.04 M, so its run-to-run spread is near zero and any change "rose beyond the spread" — v1→v3 looked like sure wins while item sampling, not the model, carried most of the uncertainty. The verdict only became honest once the lab added a paired item bootstrap (P(better) ≥ 0.9 on pooled rungs): v3 held at 0.946, while the later Haiku track scored better by point estimate (0.387 vs 0.412) and landed at 0.669, a tie.

Amend: templates/global/commands/iterative-improvement.md#Round step 5 — keep only when the metric rises beyond the run-to-run spread AND a paired bootstrap over the rung's items gives P(better) ≥ 0.9; a deterministic target's near-zero spread is no evidence.
