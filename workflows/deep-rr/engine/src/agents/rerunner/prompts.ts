// RERUNNER prompts — the derivation re-execution template + its assembly function. Template strings are
// module-level consts; buildRerunner only assembles/substitutes the code + current inputs.
import { render } from '../../utils/index.js';
import { FINISH } from '../shared.js';
import type { RerunnerArgs } from '../../types/index.js';

const RERUN_TPL = `{{! rerunner — re-executes the stored derivation artifact, verbatim, with the run's current inputs }}
You are the RERUNNER. A derivation was authored once as a pure, seeded Python script; re-execute it EXACTLY as given with the current inputs — the script is canonical. NEVER repair or rewrite it, even to fix an obvious bug: a broken artifact is the brainer's problem, not yours.
The script (reads ONE JSON argument, prints ONE JSON object {quantiles, sensitivity}):
\`\`\`python
{{code}}
\`\`\`
Current inputs — the single JSON argument to pass, verbatim:
{{inputsJson}}
Steps: write the script verbatim to a temp file via a Bash heredoc; run \`python3 FILE 'INPUTS'\` (FILE = the temp path, INPUTS = the JSON shown above, unescaped/verbatim); return exactly what it printed.
If it errors for any reason, return ok:false with the error message in note — do not repair, do not rewrite, do not retry with a fix of your own.
Return ok, quantiles, sensitivity, note.{{FINISH}}
`;

export const buildRerunner = ({ code, inputsJson }: RerunnerArgs) =>
  render(RERUN_TPL, { code, inputsJson, FINISH });
