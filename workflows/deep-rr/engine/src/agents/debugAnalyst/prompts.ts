// DEBUG ANALYST prompts — the diagnostics template + its assembly function. Template strings are
// module-level consts; buildDebugAnalyst only assembles/substitutes the focus clause.
import { plain, render } from '../../utils/index.js';
import { FINISH } from '../shared.js';
import type { DebugAnalystArgs } from '../../types/index.js';

const DEBUG_TPL = `{{! debug — consolidates metrics, run log, and raw agent I/O into one debug report }}
Consolidate and analyze this RR run's diagnostics for an engineer debugging the pipeline. Goal: "{{query}}".
Walk it phase by phase — scout → prospector → each research wave → finalize (initiate → refine → judge → synthesise) — reporting what happened at each with the actual numbers, plus anomalies, degraded/failed agents, or wasted effort to fix.
Prospector→researcher utilization (run this check): the prospector named these venues:
{{highValueSources}}
Each lane in laneRecords carries the \`assignedVenues\` the brainer gave it; from that lane's summary + rabbitHoles, judge whether the researcher actually drew on those venues. Report per-lane used / not-used and the overall % of lanes that used their assigned venues.{{focusClause}}
v3 ledger machinery to sanity-check: claims are quote-pinned + audited by a claimAuditor (dead auditor ⇒ claims stuck pending), clustered by a lineageClerk (bad clustering ⇒ wrong settled/tentative), derivations rerun by a rerunner — all degrade to null. Check metrics.claimsTotal / nullAttacksTotal / citationsBogus / chao for anomalies (e.g. all claims pending, zero nullAttacks on a contested topic, bogus citations stripped). metrics.auditCounts is the quote-pin audit outcome — read the fail and repinned counts BEFORE declaring pinning healthy: citationsBogus alone masks a broken pin contract; a high fail count surviving normalization+repin means real pin rot. metrics.pursuedTotal INCLUDES finalize judge-reopen lanes (metrics.reopenedLanes) — reconcile lane counts against that split before suspecting a silent lane loss. Walk the FINALIZE phase with the same rigor as the waves: judge verdicts (metrics.goalMet, metrics.judgePasses), refine counter-findings, and any crawl reopen — when a report contradicts the crawl-era answer, the turn usually happened there.
Metrics:
{{metrics}}
Lane records (wave, keyword, assignedVenues, summary, rabbitHoles):
{{laneRecords}}
Per-wave log:
{{waveLog}}
Per-wave result-so-far log (the brainer's running memory each wave):
{{resultLog}}
Return diagnosis (markdown).{{FINISH}}
`;

export const buildDebugAnalyst = ({
  query,
  focus,
  metrics,
  waveLog,
  resultLog,
  highValueSources,
  laneRecords,
}: DebugAnalystArgs) => {
  const focusClause = focus
    ? `
Then answer this run-specific question directly: ${focus}`
    : '';
  return render(DEBUG_TPL, {
    query,
    highValueSources: plain(highValueSources),
    focusClause,
    metrics: plain(metrics),
    laneRecords: plain(laneRecords),
    waveLog: plain(waveLog),
    resultLog: plain(resultLog),
    FINISH,
  });
};
