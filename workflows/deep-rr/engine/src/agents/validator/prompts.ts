// VALIDATOR prompts — the per-wave coverage-gate template + its assembly function. Template strings are
// module-level consts; buildValidator only assembles/substitutes the null-lane clause.
import { plain, render } from '../../utils/index.js';
import { FINISH } from '../shared.js';
import type { ValidatorArgs } from '../../types/index.js';

const VALIDATE_TPL = `{{! validator — per-wave coverage gate: did this wave's lanes fulfill their requests? }}
You are the VALIDATOR for one research wave of: "{{query}}". Judge cheaply, from the intros below, whether each lane fulfilled what it was sent to find.
Requests this wave (\`#id keyword — why\`):
{{requests}}
What each lane returned (intro only):
{{findings}}{{nullClause}}
For each request return {id, fulfilled, reason}: fulfilled=true when the return actually answers the request; false when it is off-target, empty, or too thin to use (one-line reason). Then set enough — did the wave make real progress overall? — and missing — the specific gaps still open for the next wave to re-pursue.
Return checks, enough, missing.{{FINISH}}
`;

export const buildValidator = ({ query, requests, findings, nullLanes }: ValidatorArgs) => {
  const nullClause =
    nullLanes && nullLanes.length
      ? `\nLanes that returned nothing (failed outright): ${plain(nullLanes)}`
      : '';
  return render(VALIDATE_TPL, {
    query,
    requests: plain(requests),
    findings: plain(findings),
    nullClause,
    FINISH,
  });
};
