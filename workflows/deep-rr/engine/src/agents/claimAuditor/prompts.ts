// CLAIM AUDITOR prompts — the batched mechanical quote-audit template + its assembly function. Template
// strings are module-level consts; buildClaimAuditor only assembles/substitutes the items list. The audit
// normalizes both sides (dashes/quotes/ellipsis/markdown/whitespace) before matching so formatting noise
// never fails a real pin, does ordered ellipsis-fragment matching for spliced quotes, and — when a quote
// is broken but a verified contiguous span exists in the file — auto-REPINs it via newQuote instead of
// failing the claim outright.
import { plain, render } from '../../utils/index.js';
import { FINISH } from '../shared.js';
import { CONFIG } from '../../config.js';
import type { ClaimAuditArgs } from '../../types/index.js';

const CLAIM_AUDIT_TPL = `{{! claimAuditor — batched mechanical quote audit: does each claim's quote exist (verbatim or via normalized/ellipsis match) in its cache file, and does it carry the claim on its own? Broken-but-locatable quotes auto-repin. }}
You are the CLAIM AUDITOR. For each item below, mechanically verify its quote against the cache file on disk — you are grepping for a pin, not judging truth.
Items (\`#id claim | quote | cachePath\`):
{{items}}
For EACH item, use python3 for a robust NORMALIZED substring search — never judge by eye:
1. Read the file at cachePath with errors='replace'. If the file cannot be read for ANY reason (missing, permission, anything) NEVER fabricate a verdict — that item is 'fail' with note "file unreadable".
2. NORMALIZE the file text and the quote IDENTICALLY before comparing: lowercase; fold unicode dashes (em/en) to '-'; fold curly quotes to straight; fold the ellipsis character (…) to '...'; replace non-breaking spaces with plain spaces; strip markdown emphasis characters (*, _, \`); collapse every whitespace run to one space. Formatting noise must never fail a real pin.
3. verdict 'pass' when the normalized quote is a substring of the normalized file text AND the quoted text, on its own, actually carries the claim (not merely nearby context).
4. When the substring test fails and the quote contains '...': split on '...' and test each fragment of ≥15 chars as its own normalized substring, required to appear IN ORDER in the file. All found in order ⇒ the quote was ellipsis-spliced from real text: verdict 'repinned', and set newQuote to ONE contiguous span copied EXACTLY from the ORIGINAL (un-normalized) file text, at most {{quoteMax}} characters, that best carries the claim on its own (typically the strongest fragment's full sentence). Copy it from the file byte-for-byte — never compose or paraphrase it.
5. When the substring test fails without an ellipsis: search the file for the claim's key phrases; if ONE contiguous span of at most {{quoteMax}} chars carries the claim, verdict 'repinned' with that span as newQuote (same copy-exactly rule); otherwise verdict 'fail' with a one-line note naming precisely where it diverges (e.g. "quote not in file at all", "number differs: quote says 'over one year', file says 'over 1 year'").
Return checks: one {id, verdict, note?, newQuote?} per item — verdict is 'pass', 'fail', or 'repinned'; newQuote ONLY with 'repinned'.{{FINISH}}
`;

export const buildClaimAuditor = ({ items }: ClaimAuditArgs) =>
  render(CLAIM_AUDIT_TPL, { items: plain(items), quoteMax: CONFIG.QUOTE_MAX_CHARS, FINISH });
