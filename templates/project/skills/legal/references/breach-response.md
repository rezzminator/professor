# GDPR Breach Response (Art. 33 & 34)

> Distilled from the `gdpr-breach-sentinel` skill in github.com/lawve-ai/awesome-legal-skills (AGPL-3.0, © Oliver Schmidt-Prietz) — summarised methodology, not copied. Structured guidance on EDPB Guidelines 9/2022 & 01/2021 and the ENISA severity method, not legal advice; involve the DPO and verify currency per the pre-delivery self-check.

## First moves

1. **Determine role.** Controller → full risk assessment + SA-notification decision (Track A). Processor → notify the controller only, no risk assessment, check the DPA deadline (Track B). Both → run parallel tracks, never conflate.
2. **Fix T0 — the clock start.** T0 is the moment of **reasonable certainty a breach occurred**, not full-scope determination. The 72h clock runs from T0. Challenge a T0 set conveniently (midnight, 9 AM) or with a > 24h suspicion-to-certainty gap.
3. **Two-stage T0 for processors.** Stage 1: processor becomes aware → must notify controller without undue delay (per DPA, often 24–48h). Stage 2: controller achieves certainty → the controller's 72h clock starts. The processor's awareness does **not** start the controller's clock.
4. **Capture the 11 data points** if assessing fast: role, T0, breach type(s), data categories, subject count, identifiers, encryption, malicious intent, cross-border, DPA deadlines, AI-system involvement.

## Risk severity (ENISA)

Formula: **SE = (DPC × EI) + CB**

- **DPC** (data processing context), capped 1.0–4.0: simple/contact = 1, behavioural = 2, financial = 3, sensitive Art. 9 (health, biometric) = 4. Excess from aggravating factors becomes qualitative narrative, not a higher number.
- **EI** (ease of identification): negligible 0.25, limited 0.50, significant 0.75, maximum 1.00.
- **CB** (circumstances, additive 0–2): confidentiality/integrity/availability loss each 0/+0.25/+0.50; malicious intent +0.50.

| SE | Level | Notification |
| ------- | --------- | ---------------------------------------- |
| < 2 | LOW | Internal log only (Art. 33(5)) |
| 2 – < 3 | MEDIUM | SA notification (Art. 33) |
| 3 – < 4 | HIGH | SA **and** data subjects (Art. 33 & 34) |
| ≥ 4 | VERY HIGH | SA + subjects + consider public notice |

When a score is within 0.25 of a threshold, flag it explicitly and lean conservative — the SA may disagree with a borderline LOW.

## Flags that shift the verdict

SCALE (>100 subjects → more scrutiny); ENCRYPTED (secure key → may remove the Art. 34 duty); VULNERABLE (minors, {SUBJECT_NOUN}s → consider upgrading); CROSS-BORDER (multiple Member States → notify the Lead SA only); UK SUBJECTS (separate ICO notification, ICO guidance differs from EDPB); AI SYSTEM (check AI Act Art. 62).

## Notification mechanics

- **Phased notification is allowed** (Art. 33(4)) — file initial known facts, commit to supplementary detail. Do not sit on the clock waiting for full scope; assume worst-case initially and revise down later.
- **Supply-chain chains** — sub-processor → processor → controller → SA. Each link notifies the next without undue delay; DPA deadlines stack and can leave the controller far less than 72h. Document when each link was notified.
- **AI Act Art. 62** runs in parallel to GDPR — a serious incident (death/serious harm, critical-infrastructure disruption) is reported to the market-surveillance authority no later than 15 days after awareness.

## {PROJECT_NAME} application

A breach of {SESSION_NOUN} {SENSITIVE_DATA} starts at DPC 4 and is almost certainly HIGH or VERY HIGH — plan for SA + data-subject notification. As a processor for {USER_NOUN}-clients, {PROJECT_NAME}'s Stage-1 duty is to notify the client (controller) within the DPA window; the client then runs Track A. The breach notification itself is an outsider-facing document — the self-check's "no concealment, but commitments-and-facts only" discipline applies.
