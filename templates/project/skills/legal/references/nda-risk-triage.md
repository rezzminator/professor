# NDA Triage & Legal Risk Assessment

> Adapted from the `nda-triage-anthropic` and `legal-risk-assessment-anthropic` skills in github.com/lawve-ai/awesome-legal-skills (Apache-2.0, © Anthropic). Assists legal workflow — not legal advice; have qualified counsel review before relying on a classification. Customise the thresholds to our risk appetite.

Two related tools: a fast **NDA triage** (GREEN/YELLOW/RED routing) and a general **severity × likelihood** risk method for any legal matter.

## NDA triage — GREEN / YELLOW / RED

**GREEN — standard approval** (all true): mutual (or correctly directed unilateral); all standard carve-outs present; term 1–3y, survival 2–5y; no non-solicit / non-compete / exclusivity; no (or narrow) residuals clause; reasonable governing law; standard remedies (no liquidated damages); permitted disclosures cover employees/contractors/advisors; return/destruction allows legal-retention; reasonably scoped confidential-information definition. → Approve per delegation; no counsel review.

**YELLOW — counsel review** (one or more, but not fundamentally broken): definition broader than preferred but not unreasonable; term longer than standard but within market; one easily-added carve-out missing; narrow residuals clause; acceptable-but-non-preferred jurisdiction; minor asymmetry in a mutual NDA; workable marking requirements; return clause lacks explicit retention exception. → Flag the specific issues; counsel resolves with minor redlines.

**RED — significant issues** (one or more): unilateral when mutual is required (or wrong direction); missing critical carve-outs (independent development, legal compulsion); non-solicit / non-compete embedded; exclusivity or standstill without business context; unreasonable term (10y+/perpetual without trade-secret basis); overbroad definition capturing public or independently-developed info; broad residuals clause amounting to a licence; hidden IP assignment/licence; liquidated damages/penalty; audit rights without scope; hostile jurisdiction with mandatory arbitration; **the document is not actually an NDA** (carries substantive commercial terms). → Full review; do not sign; counter with our standard form or reject.

### Standard carve-outs every NDA should contain

Public knowledge (no fault of the recipient) · prior possession · independent development · rightful third-party receipt · legal compulsion (with notice where permitted).

## Legal risk assessment — severity × likelihood

**Severity 1–5:** negligible · low · moderate · high · critical. **Likelihood 1–5:** remote · unlikely · possible · likely · almost-certain. **Score = severity × likelihood.**

| Score | Level | Colour | Action |
| ----- | -------- | ------ | ------------------------------------------------------------------------ |
| 1–4 | Low | GREEN | Accept, document, periodic review — no escalation |
| 5–9 | Medium | YELLOW | Mitigate, assign owner, brief stakeholders, define escalation triggers |
| 10–15 | High | ORANGE | Escalate to senior counsel, mitigation plan, consider outside counsel |
| 16–25 | Critical | RED | Immediate escalation, outside counsel, response team, preserve evidence |

### When to engage outside counsel

Mandatory — active litigation, government investigation, criminal/securities exposure, board-level matters. Strongly recommended — novel legal questions, jurisdictional complexity, material financial exposure, specialised expertise, material new regulation, M&A.

## {PROJECT_NAME} application

Use NDA triage on every inbound NDA from a {ORG_UNIT}, pilot partner, or investor before signing. Use the severity × likelihood method to tier any compliance finding consistently — it maps onto the CRITICAL/HIGH/MEDIUM/LOW audit ratings and the breach/DPIA risk registers, so the whole compliance posture speaks one risk language. Keep liability and commercial terms out of any NDA, consistent with the DPA scope rule.
