import { describe, it, expect } from 'vitest';
// Shared nested schema bricks live in agents/shared.js; each agent's TOP-LEVEL output schema is co-located in its agent module.
import {
  RABBITHOLE,
  SCORED,
  PAGE,
  LOOKUP,
  RESULT_SO_FAR,
  CLAIM_ITEM,
  CLAIM_ITEM_STANCE,
  TERM_SEED,
} from '../src/agents/shared.js';
import type { Schema } from '../src/types/index.js';
import { SCOUT, SCOUT_ANGLE, SCOUT_PLANNER } from '../src/agents/scout/index.js';
import { SOURCES } from '../src/agents/prospector/index.js';
import { COORD, buildCoord, BRAIN_COMPUTE } from '../src/agents/brainer/index.js';
import { VALIDATE } from '../src/agents/validator/index.js';
import { SCHEDULE } from '../src/agents/researchScheduler/index.js';
import { RESEARCH } from '../src/agents/researcher/index.js';
import { INITIATOR } from '../src/agents/initiator/index.js';
import { REFINE } from '../src/agents/refiner/index.js';
import { JUDGE } from '../src/agents/judge/index.js';
import { REPORT } from '../src/agents/synthesiser/index.js';
import { DIAG } from '../src/agents/debugAnalyst/index.js';

const isObjSchema = (s: Schema) =>
  s && s.type === 'object' && s.properties && typeof s.properties === 'object';

describe('schemas — shape', () => {
  it('every schema is a valid object schema', () => {
    for (const s of [
      RABBITHOLE,
      SCORED,
      CLAIM_ITEM,
      CLAIM_ITEM_STANCE,
      TERM_SEED,
      PAGE,
      SCOUT,
      SCOUT_ANGLE,
      SCOUT_PLANNER,
      RESEARCH,
      SCHEDULE,
      LOOKUP,
      RESULT_SO_FAR,
      BRAIN_COMPUTE,
      COORD,
      VALIDATE,
      SOURCES,
      INITIATOR,
      REFINE,
      JUDGE,
      REPORT,
      DIAG,
    ]) {
      expect(isObjSchema(s)).toBe(true);
    }
  });
  it('required arrays are correct', () => {
    expect(RABBITHOLE.required).toEqual(['keyword', 'why']);
    expect(SCORED.required).toEqual(['keyword', 'why', 'score']);
    expect(CLAIM_ITEM.required).toEqual(['claim', 'quote', 'source']);
    expect(CLAIM_ITEM_STANCE.required).toEqual(['claim', 'quote', 'source']);
    expect(TERM_SEED.required).toEqual(['term']);
    expect(PAGE.required).toEqual(['url', 'summary', 'rabbitHoles']);
    expect(SCOUT.required).toEqual(['landscape', 'pages']);
    expect(SCOUT_ANGLE.required).toEqual(['name', 'searchQuery', 'why']);
    expect(SCOUT_PLANNER.required).toEqual(['decomposition', 'angles']);
    // the channel fields are REQUIRED so a reader consciously reports zero — a MISSING field is
    // indistinguishable from a silently dropped one (a degraded lane once returned ONLY runningAnswer).
    // newTerms/surprise stay optional (TS ReaderOut keeps its optionals — the engine's `|| []` guards still apply).
    expect(RESEARCH.required).toEqual(['runningAnswer', 'claims', 'rabbitHoles', 'deadEnds']);
    expect(SCHEDULE.required).toEqual(['lanes']);
    expect(VALIDATE.required).toEqual(['checks', 'enough']);
    expect(SOURCES.required).toEqual(['highValueSources']);
    expect(JUDGE.required).toEqual([
      'goalMet',
      'verificationSound',
      'needsCompute',
      'computeSound',
      'reasoning',
    ]);
    expect(REPORT.required).toEqual(['report', 'verdict', 'confidence', 'plan', 'openQuestions']);
    expect(DIAG.required).toEqual(['diagnosis']);
  });
});

describe('schemas — nesting', () => {
  it('PAGE.items references the RABBITHOLE shape', () => {
    expect(PAGE.properties!.rabbitHoles.items).toBe(RABBITHOLE);
  });
  it('SCOUT.pages references PAGE; RESEARCH.rabbitHoles references RABBITHOLE', () => {
    expect(SCOUT.properties!.pages.items).toBe(PAGE);
    expect(RESEARCH.properties!.rabbitHoles.items).toBe(RABBITHOLE);
  });
  it('SCOUT_PLANNER.angles references SCOUT_ANGLE (the scout swarm — v3 batch 2s)', () => {
    expect(SCOUT_PLANNER.properties!.angles.items).toBe(SCOUT_ANGLE);
  });
  it('SCOUT.claims references CLAIM_ITEM at the TOP LEVEL (no stance — the scout has no digest yet)', () => {
    expect(SCOUT.properties!.claims.items).toBe(CLAIM_ITEM);
    expect(CLAIM_ITEM.properties!.stance).toBeUndefined();
    expect(PAGE.properties!.claims).toBeUndefined(); // claims are a scout-level union, never nested per page
  });
  it('RESEARCH.claims references CLAIM_ITEM_STANCE, which carries every CLAIM_ITEM property plus stance', () => {
    expect(RESEARCH.properties!.claims.items).toBe(CLAIM_ITEM_STANCE);
    for (const key of Object.keys(CLAIM_ITEM.properties!))
      expect(CLAIM_ITEM_STANCE.properties![key]).toBe(CLAIM_ITEM.properties![key]);
    expect(CLAIM_ITEM_STANCE.properties!.stance.properties!.kind.enum).toEqual([
      'supports',
      'attacks',
    ]);
  });
  it('newTerms (both scout and researcher) reference TERM_SEED', () => {
    expect(SCOUT.properties!.newTerms.items).toBe(TERM_SEED);
    expect(RESEARCH.properties!.newTerms.items).toBe(TERM_SEED);
  });
  // finding E: the scout footer promises a "Next sources" channel the SCOUT schema could not carry — the
  // work was silently discarded. nextSources requires ref+why (no expect/target — no ledger exists yet).
  it('SCOUT.nextSources requires ref+why, with no expect/target (no ledger exists yet)', () => {
    const item = SCOUT.properties!.nextSources.items!;
    expect(item.required).toEqual(['ref', 'why']);
    expect(item.properties!.expect).toBeUndefined();
    expect(item.properties!.target).toBeUndefined();
    expect(SCOUT.required).not.toContain('nextSources'); // top-level array itself stays optional
  });
  it('RABBITHOLE carries the optional kind enum shared by PAGE and RESEARCH rabbitHoles', () => {
    expect(RABBITHOLE.properties!.kind.enum).toEqual(['gap', 'attack', 'entity']);
  });
  // finding A (blocker): LOOKUP and SCORED had NO `kind` property, so the schema-constrained brainer could
  // never emit one despite its attackClause instruction — kind never survived to the store on any live path.
  it('LOOKUP carries an optional kind enum so an originated lane can set its origin channel (finding A)', () => {
    expect(LOOKUP.properties!.kind).toBeDefined();
    expect(LOOKUP.properties!.kind.enum).toEqual(['gap', 'attack', 'entity', 'origin']);
    expect(LOOKUP.required || []).not.toContain('kind');
  });
  it('SCORED carries an optional kind enum so a parked (`add`) lead can set its origin channel (finding A)', () => {
    expect(SCORED.properties!.kind).toBeDefined();
    expect(SCORED.properties!.kind.enum).toEqual(['gap', 'attack', 'entity', 'origin']);
    expect(SCORED.required).not.toContain('kind');
  });
  it('RESEARCH.nextSources expect/target are advisory + null-tolerant (v3.2.3: the engine seeds only ref/why, so a loose value must never fail the whole reader payload)', () => {
    const item = RESEARCH.properties!.nextSources.items!;
    expect(item.properties!.expect.enum).toBeUndefined(); // un-enumed — values live in the description
    expect(item.properties!.expect.type).toEqual(['string', 'null']);
    expect(item.properties!.target.type).toEqual(['number', 'string', 'null']);
    expect(item.required).toEqual(['ref', 'why']); // both stay optional
  });
  it('worker-emitted optional fields are null-tolerant (v3.2.3 — a null must validate, the engine null-scrubs at ingest)', () => {
    const ent = CLAIM_ITEM.properties!.entities;
    expect(ent.type).toEqual(['object', 'null']);
    expect(ent.properties!.funder.type).toEqual(['string', 'null']);
    expect(ent.properties!.dataset.type).toEqual(['string', 'null']);
    expect(ent.properties!.venue.type).toEqual(['string', 'null']);
    expect(CLAIM_ITEM.properties!.value.type).toEqual(['string', 'null']);
    expect(CLAIM_ITEM.properties!.cachePath.type).toEqual(['string', 'null']);
    // a stance target may arrive as 'c12' prose — validates as string, ingest coerces the digit-run
    expect(CLAIM_ITEM_STANCE.properties!.stance.properties!.target.type).toEqual([
      'number',
      'string',
    ]);
    expect(RESEARCH.properties!.surprise.type).toEqual(['string', 'null']);
  });
  it('COORD nests RESULT_SO_FAR/SCORED/LOOKUP and requires the delta fields', () => {
    expect(COORD.properties!.resultSoFar).toBe(RESULT_SO_FAR);
    expect(COORD.properties!.add.items).toBe(SCORED);
    expect(COORD.properties!.lookupNext.items).toBe(LOOKUP);
    expect(COORD.required).toEqual(['resultSoFar', 'rescore', 'add', 'lookupNext', 'stop']);
  });
  it('COORD.derivation is OPTIONAL: {code, inputs}, each input requires name/dist/claimIds/prior', () => {
    expect(COORD.required).not.toContain('derivation');
    const deriv = COORD.properties!.derivation;
    expect(deriv.required).toEqual(['code', 'inputs']);
    expect(deriv.properties!.code.type).toBe('string');
    const input = deriv.properties!.inputs.items!;
    expect(input.required).toEqual(['name', 'dist', 'claimIds', 'prior']);
    expect(input.properties!.claimIds.items!.type).toBe('number');
    expect(input.properties!.prior.type).toBe('boolean');
  });
  it('buildCoord prunes optional clauses per call — the spawn-classifier schema-size guard', () => {
    const slim = buildCoord({ compute: false, canSpawn: false });
    expect(slim.properties!.derivation).toBeUndefined();
    expect(slim.properties!.spawn).toBeUndefined();
    expect(slim.required).toEqual(['resultSoFar', 'rescore', 'add', 'lookupNext', 'stop']);
    expect(slim.properties!.resultSoFar).toBe(RESULT_SO_FAR); // shared bricks keep one identity
    const full = buildCoord({ compute: true, canSpawn: true });
    expect(full.properties!.derivation).toBeDefined();
    expect(full.properties!.spawn).toBeDefined();
    // Last known classifier-safe serialized sizes (design.md §COORD). A failure here means a
    // schema edit re-armed the spawn-classifier size kill that murdered a production run's
    // wave-0 brainer (v3.2.0 maiden flight, +144 chars crossed 4,096). Growing a COORD brick
    // requires shrinking elsewhere first.
    expect(JSON.stringify(buildCoord({ compute: false, canSpawn: false })).length).toBeLessThanOrEqual(3966);
    expect(JSON.stringify(buildCoord({ compute: true, canSpawn: true })).length).toBeLessThanOrEqual(5311);
  });
  it('RESULT_SO_FAR requires the full memory contract (v3: keyClaimIds replaces evidence as required)', () => {
    expect(RESULT_SO_FAR.required).toEqual([
      'answer',
      'keyClaimIds',
      'resolved',
      'openGaps',
      'tensions',
      'working',
      'confidence',
    ]);
    // the ledger is the evidence store; the deprecated evidence brick is REMOVED from the schema
    // entirely (dead weight against the spawn classifier's schema-size limit — no code read it)
    expect(RESULT_SO_FAR.properties!.evidence).toBeUndefined();
    expect(RESULT_SO_FAR.properties!.keyClaimIds.items!.type).toBe('number');
    // assumptions carry {claim, basis} and are optional (not part of the required memory contract)
    expect(RESULT_SO_FAR.properties!.assumptions.items!.required).toEqual(['claim', 'basis']);
    expect(RESULT_SO_FAR.required).not.toContain('assumptions');
  });
  it('INITIATOR requires refinement/synthesiser (no computement — the judge decides derivation)', () => {
    expect(INITIATOR.required).toEqual(['refinement', 'synthesiser']);
    expect(INITIATOR.properties!.computement).toBeUndefined();
  });
  it('INITIATOR facts carry an optional claimId, binding a fact to a ledger claim (v3 batch 4)', () => {
    const factItem = INITIATOR.properties!.refinement.properties!.facts.items!;
    expect(factItem.required).toEqual(['fact', 'why']); // claimId stays optional
    expect(factItem.properties!.claimId.type).toBe('number');
  });
  it('JUDGE is the finalize terminal skeptic — four boolean flags + reasoning required', () => {
    expect(JUDGE.properties!.goalMet.type).toBe('boolean');
    expect(JUDGE.properties!.verificationSound.type).toBe('boolean');
    expect(JUDGE.properties!.needsCompute.type).toBe('boolean');
    expect(JUDGE.properties!.computeSound.type).toBe('boolean');
    expect(JUDGE.properties!.reopenRabbitHoles.items).toBe(RABBITHOLE); // gap leads reuse the shared brick
    expect(JUDGE.required).not.toContain('directive'); // directive + reopenRabbitHoles are optional
  });
  it('JUDGE carries an optional retractClaimIds — the engine retracts + recomputes downstream (v3 batch 4)', () => {
    expect(JUDGE.required).not.toContain('retractClaimIds');
    expect(JUDGE.properties!.retractClaimIds.items!.type).toBe('number');
  });
  it('BRAIN_COMPUTE returns the updated resultSoFar (derivation folded into `working`)', () => {
    expect(BRAIN_COMPUTE.required).toEqual(['resultSoFar']);
    expect(BRAIN_COMPUTE.properties!.resultSoFar).toBe(RESULT_SO_FAR);
    expect(COORD.properties!.computement).toBeUndefined(); // the brainer computes inline — no computement field on its output
  });
  it('SCHEDULE groups sized sources per lane id — each source carries source/path/size/chars', () => {
    const src = SCHEDULE.properties!.lanes.items!.properties!.sources.items!;
    expect(SCHEDULE.properties!.lanes.items!.required).toEqual(['id', 'sources']);
    expect(src.required).toEqual(['source', 'path', 'size', 'chars']);
    expect(src.properties!.size.type).toBe('number');
    expect(src.properties!.chars.type).toBe('number');
  });
  it('VALIDATE is the per-wave coverage gate — checks{id,fulfilled} + enough required', () => {
    expect(VALIDATE.properties!.checks.items!.required).toEqual(['id', 'fulfilled']);
    expect(VALIDATE.properties!.enough.type).toBe('boolean');
    expect(VALIDATE.required).not.toContain('missing'); // missing is optional
  });
  it('REFINE requires report + the attack-recording fields (v3 batch 4)', () => {
    expect(REFINE.required).toEqual(['report', 'queriesTried', 'counterFound']);
    expect(REFINE.properties!.report.type).toBe('string');
    expect(REFINE.properties!.queriesTried.items!.type).toBe('string');
    expect(REFINE.properties!.counterFound.type).toBe('boolean');
    expect(REFINE.properties!.counterNote.type).toBe('string');
  });
  it('REPORT.confidence is an enum', () => {
    expect(REPORT.properties!.confidence.enum).toEqual(['high', 'medium', 'low']);
  });
});
