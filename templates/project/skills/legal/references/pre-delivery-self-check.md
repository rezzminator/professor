# Pre-Delivery Self-Check — Adversarial QC Before Any Legal Document Ships

> Run this before emitting any drafted or edited client-, {SUBJECT_NOUN}-, or regulator-facing document (privacy policy, DPA, ToS, consent notice, breach notification, ROPA, sub-processor register). Adapted from the Red Team Verifier and Statutory Interpretation skills in github.com/lawve-ai/awesome-legal-skills (AGPL-3.0) — distilled, not copied.

## The stance: assume error until proven correct

Approach your own draft as an adversary would. Your job is not to confirm what you wrote — it is to independently prove each claim is accurate, current, sourced, and appropriately framed. When you cannot verify, flag it. Better to over-verify than to ship a wrong legal statement to a regulator.

## The seven gates

A document passes only when every gate is clear. Each gate names the failure it catches.

### 1. Verify every fact against the primary source — never from memory

Every date, in-force date, transition deadline, article/§ number, recital, threshold, and numeric figure is verified against the **primary source** before it ships: EUR-Lex for EU law, legislation.govt.nz for NZ, the official gazette / regulator publication otherwise. Three checks per legal citation:

- **Existence** — the article/§ actually exists and the number is right (LLMs invent plausible-but-wrong citations, e.g. "Art. 42(5)" when the act stops at 42(4)).
- **Currency** — you are citing the **current, non-superseded** version, accounting for amendments and implementing acts — not the original text where it has since changed.
- **Recalculation** — every timeline (e.g. "X months from the in-force date") is recomputed by hand from the verified effective date, not trusted as written.

Distinguish binding law from non-binding guidance — a regulator's recommendation is not an obligation. Distinguish primary law (regulation, directive, statute) from secondary (guidance, opinion, commentary).

### 2. Opinion vs. fact — mark our reading as our position

Where the law leaves genuine room and we have taken a reading, label it as **our reasoned legal position**, never as settled law. Settled law is what an authoritative source states plainly; a contested or interpretive point is ours to argue, and the document must not dress it as fact. Predictive statements about future regulatory developments are speculation and are labelled as such.

### 3. No overclaim — don't assert a conditional thing as settled

If a statement depends on something not yet true or not yet decided (a control not yet built, an entity not yet registered, a mechanism not yet signed, an open question), do not assert it as settled. State the accurate current position, or scope the claim to its actual dependency. An untrue claim in a privacy policy or DPA is itself an Art. 5 transparency breach and a consumer-law misrepresentation — it costs far more than candour.

### 4. Commitments-only — a client/regulator-facing document states what we DO

The body lists what we **do** and what we **commit to** — never the controls we lack, the gaps we know about, or items we have "deferred." Internal gaps, missing controls, and to-do items live in the DPIA, the compliance posture, or an action stub — never in an outsider-facing document. This is not concealment of a notifiable breach (which is forbidden); it is the difference between a document of commitments and an internal audit log.

### 5. Contract form — parties by defined term, never pronoun or first name

In a contractual instrument (DPA, ToS, SLA), each party is referred to by its **defined term** ("the Processor", "the Controller", "the Customer") throughout the body. The legal name appears once where the term is defined and again at signing — never a pronoun, a first name, or an informal handle in operative clauses.

### 6. Scope discipline — keep each instrument to its legal subject

A document carries only clauses proper to its instrument. A DPA under Art. 28 is a **data-protection** instrument: it does not allocate liability, set insurance, price the service, or carry commercial terms — those belong in the master services agreement. Mixing scopes weakens both instruments and creates conflicting provisions.

### 7. Disclaimer and jurisdiction adequacy

The document states its applicable jurisdiction, the version/date of the regimes it relies on, and — where the audience needs it — that it is not a substitute for independent legal advice. A document silent on which legal system governs invites the wrong reading.

## Output of the check

If all seven gates clear, the document is deliverable. If any gate fails, fix it before shipping — or, where a fact genuinely cannot be verified, mark the draft `DRAFT` and gather the open items in one block at the top (never inline in the body).
