# DPA Drafting Guide (Art. 28)

> {PROJECT_NAME} house guide for writing a controller–processor Data Processing Agreement (GDPR Art. 28 / {JURISDICTION} equivalent term) that survives counsel review and a regulator. Load before drafting or revising any DPA. Assists legal workflow — not legal advice; verify every cited provision against the primary source per the pre-delivery self-check. The worked example that follows every rule here is `docs/epics/legal/instruments/dpa-processor-agreement.md`.

---

## 1. The legal floor — what Art. 28 makes mandatory

A DPA is **required by law** whenever a processor handles personal data for a controller (Art. 28(3)). Both parties are liable for its absence — a supervisory authority can fine either side for not having one. Two layers are non-negotiable.

### 1.1 The set-out elements (Art. 28(3) chapeau)

The contract must specify, concretely:

- **Subject-matter** of the processing
- **Duration** of the processing
- **Nature and purpose** of the processing
- **Type of personal data** (flag special-category / Art. 9 explicitly)
- **Categories of data subjects**
- **Obligations and rights of the controller**

### 1.2 The eight mandatory clauses (Art. 28(3)(a)–(h))

The processor, by contract, must:

| Clause | Requirement |
| ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) | Process **only on the controller's documented instructions**, including for any third-country transfer, unless required by law (then inform the controller first, unless the law forbids it). |
| (b) | Ensure persons authorised to process are under a **confidentiality** obligation. |
| (c) | Take all **Art. 32 security** measures. |
| (d) | Respect the **sub-processor** conditions of Art. 28(2) & (4) — prior authorisation, flow-down of the same obligations, processor stays fully liable. |
| (e) | **Assist the controller** with data-subject-rights requests by appropriate technical/organisational measures. |
| (f) | **Assist the controller** with Arts. 32–36 (security, breach notification 33/34, DPIA 35, prior consultation 36). |
| (g) | **Delete or return** all personal data at the end of the service and delete existing copies, unless law requires storage. |
| (h) | Make available all info needed to **demonstrate compliance**, and allow for and contribute to **audits/inspections**. |

Plus the standalone duty (Art. 28(3) final sub-paragraph): the processor **immediately informs the controller if an instruction infringes** the GDPR.

---

## 2. Best-practice principles (the difference between compliant and good)

1. **Do not merely restate the GDPR.** This is the EDPB's central instruction (Guidelines 07/2020): the agreement must add **concrete, specific information on _how_ each requirement is met and _what level of security_ applies** — not paraphrase the articles. A DPA that just quotes Art. 28 back is a red flag.
2. **Describe security in assessable detail.** Give the controller enough specificity to evaluate the measures (encryption in transit/at rest, access control, logging, MFA, pseudonymisation where used). State which party implements which control. The processor must **not change agreed security measures without the controller's approval**.
3. **Be concrete on sub-processors.** Name them (purpose, data received, location, transfer instrument). Specify general vs specific authorisation, the **notice + objection window** for changes, the **flow-down** of identical obligations, and that the processor **remains fully liable** for them.
4. **Pin a breach SLA number.** "Without undue delay" is the legal floor but operationally useless — commit a concrete internal SLA (e.g. **notify the controller within 24h of awareness**), because the controller's regulator clock can start when the processor becomes aware.
5. **Right-size liability.** There is no market-standard liability model (caps, super-caps, breach-specific indemnities all exist). Negotiate to actual risk. **Avoid broad indemnities** — lethal for an unlimited-liability sole trader ({LEGAL_ENTITY_TYPE}) and often excluded by cyber policies. Pair with a "liability as imposed by law" cap.
6. **Tame audit rights.** Offer compliance evidence (the access-log trail, SOC 2 / ISO certs when they exist) to satisfy the audit duty without open-ended on-site inspections.
7. **Address international transfers head-on.** State the Chapter V basis for every cross-border leg (adequacy / SCCs / derogation). If the whole path is EEA-resident, say so and name the residency of each hop.
8. **Plain, unambiguous language; living document.** Clear enough that both parties understand their duties; review and update as law and the system evolve. Get specialist counsel review before signing.
9. **Honesty rule ({PROJECT_NAME} house rule, non-negotiable).** Every security and retention representation must describe the system **as actually built**, or be explicitly flagged as a target not yet implemented. **Never present a target as a current control** to a controller or regulator. Re-pin the doc to the as-built state at signing time.

---

## 3. Recommended structure (section order)

1. Status banner (drafting instrument vs executed; counsel-review note; honesty rule)
2. **Parties** + controller/processor split (who decides, who acts)
3. Subject-matter, duration, nature, purpose; **data categories** (mark Art. 9); **lawful-basis hook**
4. Processing **only on documented instructions** (+ no own-purpose use / no model training)
5. **Confidentiality**
6. **Security of processing** (Art. 32) — current controls vs targets, separated honestly
7. **Sub-processor** authorisation + flow-down + named list
8. **International-transfer basis** (Chapter V; plus local overlays e.g. NZ IPP-12)
9. **Assistance**: security, breach (dual-recipient where cross-border), DPIA — with the breach SLA
10. **Data-subject rights** assistance (state the model: {USER_NOUN}-mediated, self-service, etc.)
11. **Audit** rights
12. Records & accountability
13. **Return or deletion** on termination (+ retention model)
14. Order of precedence & survival
15. **Open questions** (collected) — every unresolved point flagged inline AND listed at the end

---

## 4. The contested hot-spots (where DPAs get negotiated)

- **Instructions scope** — keep "documented instructions" defined (the agreement itself + configuration + further written instructions).
- **Sub-processor changes** — general authorisation + objection right is the workable default; a pure specific-authorisation model stalls operations.
- **Security specificity** — too vague fails the EDPB test; too rigid traps the processor. State the measures + a no-degradation commitment.
- **Liability & indemnity** — the sharpest negotiation. Cap it; never give an open indemnity.
- **Audits** — narrow from "any time, on-site" to "evidence first, inspection on reasonable cause."
- **Breach timing** — convert "without undue delay" to a number.
- **Return vs deletion** — controller's choice; cover copies and any law-mandated retention.

---

## 5. Common mistakes to avoid

- Restating the GDPR verbatim with no concrete "how."
- "Appropriate technical and organisational measures" with zero detail.
- Open-ended or unnamed sub-processor list; missing flow-down.
- No breach SLA number.
- Broad/uncapped indemnities (fatal for an {LEGAL_ENTITY_TYPE}).
- **Claiming a control that isn't built yet** (violates the honesty rule).
- Omitting a chapeau element (duration, data-subject categories, etc.).
- Ignoring international transfers or asserting "EU-only" without verifying every hop.
- Letting the doc go stale after the system changes.

---

## 6. {PROJECT_NAME}-specific overlays (apply on top of the floor)

- **Dual-purpose single document.** For non-EU clients, bolt the local cross-border regime onto the Art. 28 core in one signed doc (e.g. **NZ IPP-12** model comparable-safeguards clauses for NZ; see the {example-client} DPA §6.2).
- **Processor = processing layer, not system of record.** {PROJECT_NAME} holds a time-boxed **working copy** (2-year retention + export-and-warn), the controller keeps the long-term {RECORD_NOUN}. State this explicitly; it resolves the data-minimisation vs {DOMAIN_ADJ}-retention tension.
- **Insurance precondition.** The {LEGAL_ENTITY_TYPE} has unlimited personal liability (no corporate liability shield); cyber + professional-liability + general-liability insurance from an EEA-admitted carrier with the relevant cross-border jurisdiction cover and the {DOMAIN_ADJ}-systems exclusion waived is the substitute — a precondition to the agreement taking effect, not a clause of it.
- **No own-purpose use is load-bearing.** Several protections (e.g. NZ Privacy Act s 11 agency-model shield) hold **only** while the processor never uses/discloses data for its own purposes (no model training, no cross-client analytics). Keep that prohibition absolute and flag it as the condition.
- **Sacred-ground discipline.** {DOMAIN_NOUN} data is Art. 9 special-category; positioning the tool as a documentation assistant (NOT a {DOMAIN_ADJ} device / {FORBIDDEN_DOMAIN_OUTPUTS} tool) is load-bearing — keep every capability claim modest and evidence-backed.

---

## 7. Templates & worked examples to crib from

- **EU Commission Standard Contractual Clauses for controller↔processor** (Art. 28(7) — Implementing Decision 2021/915): an official, ready-made Art. 28 module — use as a base, then add commercial terms.
- **ICO** — "What needs to be included in the contract?" (the canonical checklist).
- **Irish DPC** — _A Practical Guide to Controller-Processor Contracts_ (downloadable PDF).
- **EDPB Guidelines 07/2020** — the controller/processor concepts + the "don't restate the GDPR" rule.
- **Public real-world DPAs** for tone/structure: HubSpot DPA; activeMind.legal and Juro free templates.
- **Internal worked example:** `docs/epics/legal/instruments/dpa-processor-agreement.md` (Art. 28 + NZ IPP-12, honesty rule applied, processor-not-storage retention, s 11 agency model).

---

## Sources

- GDPR Art. 28 (full text): <https://gdpr-info.eu/art-28-gdpr/>
- ICO — what a controller-processor contract must contain: <https://ico.org.uk/for-organisations/uk-gdpr-guidance-and-resources/accountability-and-governance/contracts-and-liabilities-between-controllers-and-processors-multi/what-needs-to-be-included-in-the-contract/>
- Irish DPC — Practical Guide to Controller-Processor Contracts: <https://www.dataprotection.ie/en/dpc-guidance/data-processing-agreements>
- EDPB Guidelines 07/2020 (controller & processor concepts): <https://www.edpb.europa.eu/system/files/2023-10/EDPB_guidelines_202007_controllerprocessor_final_en.pdf>
- EU Commission SCCs (controller-processor, Art. 28): <https://commission.europa.eu/law/law-topic/data-protection/international-dimension-data-protection/standard-contractual-clauses-scc_en>
- Cooley — DPAs: the 10 most important considerations: <https://cdp.cooley.com/data-processing-agreements-the-10-most-important-considerations/>
