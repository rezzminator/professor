---
name: audit:security
description: Scans for security defects — info-leak, injection, auth, {API_PROTOCOL}, LLM/prompt, {SENSITIVE_DATA}, health endpoints, crypto/secrets, transport, supply-chain, CI/CD, concurrency (8A–8K). Scope — bare runs all; a letter (8C) or topic (auth, {SENSITIVE_DATA}, health) runs one section. Report-only.
argument-hint: [scope]
---

# Security — Deep Scan

> {PROJECT_NAME} handles {SESSION_NOUN} records, {SUBJECT_NOUN} data, and {DOMAIN_ADJ} observations — among the most sensitive data categories in this domain. A security breach here doesn't just leak emails; it exposes someone's most private information to whoever should never see it. This is not a checkbox — it's a fortress inspection. {SACRED_GROUND}

**Scopes:** bare `security` runs every section; a scope names a section by letter (`8C`) or a topic (`injection`, `auth`, `{API_PROTOCOL}`, `llm`, `prompt`, `{SENSITIVE_DATA}`, `health`, `crypto`, `secrets`, `transport`, `supply-chain`, `ci-cd`, `concurrency`) and runs only the section(s) covering it. Sections are independent.

**Every finding reports:** `SECURITY: {sub-category}/{issue_type}` — Where (`file:line`) · What (what is vulnerable and how) · Severity · Risk (what an attacker could exploit, in a {DOMAIN_ADJ}/{SENSITIVE_DATA} context) · Fix (remediation with the code pattern).

**Severity guide:**

- CRITICAL: {SUBJECT_NOUN} data exposure, missing auth on mutations, hardcoded real credentials, direct prompt injection allowing data exfiltration, unvalidated LLM output executed as code
- HIGH: exception internals reaching users, technology stack disclosure, missing input validation on external boundaries, indirect prompt injection vectors, {SENSITIVE_DATA} sent to external APIs without safeguards, JWT algorithm confusion
- MEDIUM: hardcoded enum lists, inconsistent auth patterns, verbose error messages, missing rate limiting, overly permissive CORS, missing security headers
- LOW: `X-Powered-By` header, internal IDs in non-sensitive responses, minor naming leaks in non-production code paths

## 8A — Information Leakage & Error Exposure

1. **Internal error details exposed to users:** Grep for `type(e).__name__` or `str(e)` stored in user-visible fields, `e.message` or raw exception strings in {API_PROTOCOL} error responses, Python/Node exception class names in any client-visible field, `stack`/`stackTrace` in API responses.

2. **Technology stack disclosure:** {ORM}/{AI_FRAMEWORK} driver or library names (e.g. ORM connection URLs, DB driver names, LLM SDK names) in user-visible strings, {API_FRAMEWORK} server-identity headers, {API_PROTOCOL} error `extensions` with internal paths.

3. **Debug/development artifacts in production:** `breakpoint()` in Python projects, `TODO`/`FIXME` describing security workarounds, commented-out auth checks, `NODE_ENV === 'development'` blocks disabling security.

4. **Verbose {API_PROTOCOL} errors:** Check `maskedErrors` configuration, custom error formatting, `extensions` field leaking resolver paths.

5. **Health/status endpoints:** an unauthenticated health or status route returns liveness only — never version strings, dependency/DB connection detail, {QUEUE} depth, or config values.

**Files to check:** {API_FRAMEWORK} error middleware, {API_FRAMEWORK} config, all catch blocks in resolvers/services, AI/pipeline-project exception handlers (if the roster has one), health/status route handlers.

## 8B — Injection Attacks

1. **SQL injection:** `sql.raw(` with user values, template literals with variables in {ORM}, Python `text(` or `execute(` with f-strings/.format() (e.g. in the AI/pipeline project).

2. **Command injection:** `child_process.exec(` with string args containing variables, `subprocess.run(`/`os.system(` in Python.

3. **XSS:** `dangerouslySetInnerHTML` in UI projects, `innerHTML` assignments, LLM-generated content rendered without sanitization.

4. **Template injection:** `vm.runInContext(` with user/LLM content, Python `eval(`/`exec(`/`compile(` with variable args.

5. **Header/CRLF injection:** `res.setHeader(`/`res.redirect(` with user-controlled values.

6. **Prototype pollution:** `Object.assign({}, userInput)`, missing `__proto__`/`constructor` filtering (the vulnerable-dependency side is 8I-2).

**Files to check:** Resolver input handling, AI/pipeline-project DB query code, shell command construction, UI components rendering AI content.

## 8C — Authentication & Authorization

1. **Missing auth on {API_PROTOCOL} operations:** Every mutation/sensitive query must have auth middleware. Mutations without auth (except login/register/health) are CRITICAL.

2. **JWT vulnerabilities:** `algorithms` not explicitly specified (algorithm none attack), `jwt.decode` without verify, hardcoded JWT secret, fallback patterns (`|| 'default'`), missing `expiresIn`, `ignoreExpiration: true`.

3. **IDOR:** Resolvers taking `id` without verifying requesting user has permission. {SUBJECT_NOUN} record access without {USER_NOUN}-{SUBJECT_NOUN} relationship check.

4. **Broken RBAC:** Role checks using string comparison instead of enum, role settable by user (mass assignment).

5. **Session/token management:** Missing refresh token rotation, no invalidation on password change, tokens in URLs/query params.

6. **Feature-flag login bypass:** any magic/demo login path must be gated behind an explicit feature flag that is off in dev/prod env files (enabled only in a dedicated demo env); a bypass the project has removed end to end stays removed — grep for its identifiers outside the tests, and pin the absence with a test.

7. **Header-trust (CWE-290):** authorization/identity/rate-limit decisions read the verified JWT context (`ctx.user`), never a raw header (`X-Forwarded-For`, `X-Real-IP`, `req.headers[...]`). Pin {API_FRAMEWORK} `trust proxy` to the exact proxy hop count.

8. **JWKS/`jku` injection (CWE-347, if RS256 is adopted):** the signing-key URL is code-configured, never taken from the token's own `jku`/`jwk`/`kid` header.

9. **Revocation honored:** a token carrying `jti`/session-id is checked against a server-side revocation store on every verify, not trusted because it parses.

**Files to check:** JWT middleware, resolver auth patterns, auth service, feature flag config, token storage in UI/client projects, env files.

## 8D — {API_PROTOCOL} Attack Surface

1. **Introspection enabled in production:** Must be disabled. Check for environment-based toggling.

2. **Query depth & complexity bombs:** Check for `depthLimit`/`queryComplexity`/`maxDepth`/`costAnalysis` plugins.

3. **Batching attacks:** Check batch size limits — 1000 login mutations in one request = brute force.

4. **Mass assignment (OWASP API3 BOPLA):** input types and mutation args that accept `role`/`isAdmin`/an `{ORG_UNIT}` id/`{USER_NOUN}` id/`createdAt`/consent flags must strip or re-derive them server-side, never persist the caller's value.

5. **Subscription/{REALTIME_PROTOCOL} security:** Auth on {REALTIME_PROTOCOL} connect, subscription resolver auth checks, connection limits.

6. **Field-level authorization:** {SENSITIVE_DATA} fields need per-field auth checks.

7. **Field suggestion leakage:** "Did you mean 'socialSecurityNumber'?" in errors.

8. **BOPLA via fragments (OWASP API3):** field-level authorization gates the RESOLVED runtime type — inline fragments (`...on Type`), interface and union spreads cannot reach a field the named query hides.

9. **Alias-count cost:** the complexity plugin multiplies cost by alias count, not depth alone — 50 aliased copies of one expensive resolver stay under a depth limit while running it 50×.

10. **Batch/export per-row fence (OWASP API1 at scale):** a resolver returning a collection (export, roster, `*sBy{SUBJECT_NOUN}`) fences EACH row, not once at the request level.

**Files to check:** {API_FRAMEWORK} config, schema type definitions, input types, {REALTIME_PROTOCOL} setup, subscription resolvers.

## 8E — LLM & Prompt Injection

1. **Direct prompt injection:** f-strings/`.format()` in prompt construction with user data, `HumanMessage(content=` with raw user/source text.

2. **Indirect prompt injection:** Source text containing "Ignore previous instructions..." flowing to LLM without hardening. Check system prompt instructs LLM to treat source text as DATA.

3. **Prompt leaking:** System prompts in user-accessible files/responses, raw LLM responses returned unfiltered, prompts logged at INFO level.

4. **LLM output trust:** `json.loads()`/`JSON.parse()` on raw LLM output without validation, LLM output inserted into SQL/shell, LLM output rendered as HTML.

5. **Excessive agency:** {AI_FRAMEWORK} agents with unguarded DB write/API call tools, missing `max_iterations`/`recursion_limit`.

6. **Missing output guardrails:** No content filtering, missing `aiGenerated` flags on AI output.

7. **RAG/embedding security:** Multi-{SUBJECT_NOUN} data in same vector space without isolation, search results not scoped by auth context.

8. **{AI_FRAMEWORK} vulnerabilities:** Check the framework's published CVE advisories and pin to a patched version. Check for `verbose=True`, tracing with {SENSITIVE_DATA}, `PythonREPL`/`ShellTool`/`BashProcess` imports.

9. **Data poisoning via vector store:** Source text not scanned before embedding, no knowledge file integrity verification.

10. **Agent-tool-argument trust:** an AI-project `@tool`'s record-id arguments come from trusted server context (a `{SUBJECT_NOUN}_id` set by the caller-verified session), never a model-chosen value.

11. **ATLAS tagging:** tag each injection finding with its MITRE ATLAS id — AML.T0051 (direct), AML.T0051.001 (indirect/RAG), AML.T0051.002 (agent-tool-abuse) — for the {PRIVACY_ASSESSMENT}.

12. **Semantic injection scan:** alongside structural source-text sanitize, a phrase-level jailbreak scan (log + metric) over source-derived text feeding any chain.

**Files to check:** Chain definitions, prompt templates, {AI_FRAMEWORK} agent/tool definitions, LLM response processing, {QUEUE} consumer, UI rendering of AI content, the {AI_SERVICE_NAME} manifest for the {AI_FRAMEWORK} version.

## 8F — {SENSITIVE_DATA} & {DOMAIN_ADJ} Data Protection

1. **{SUBJECT_NOUN} data in logs:** read EVERY log call in scope and judge each field it carries — a grep for known {SENSITIVE_DATA} field names is a regression check only, structurally blind to a new field shape. NEVER log {SENSITIVE_DATA} — only anonymized IDs. Report log calls read / log calls in scope; an unread call is a named hole, not silence.

2. **{SENSITIVE_DATA} in error messages/API responses:** Error strings interpolating {subject}/{session} data, AI/pipeline-project step status `reason` with raw source excerpts.

3. **{SENSITIVE_DATA} in URLs/query params/headers:** {SUBJECT_NOUN} names in URL construction — URLs are logged by proxies, stored in browser history.

4. **{SENSITIVE_DATA} in client-side storage:** `localStorage`/`AsyncStorage` storing {subject} data instead of a secure store.

5. **{SENSITIVE_DATA} sent to external APIs:** {TRANSCRIPTION_SERVICE} must use the {DATA_REGION} endpoint. Check data minimization in LLM prompts. Verify HTTPS.

6. **Media file security:** Auth checks on media URLs, unpredictable file paths (UUIDs not sequential IDs), signed URLs with short expiry.

7. **Missing audit trail:** No logging of who accessed what {SUBJECT_NOUN} data when.

8. **Data deletion / right to erasure:** Cascade deletion across all stores ({session}s, transcripts, analysis, media, embeddings, external service copies).

9. **{SENSITIVE_DATA} in analytics (the $7M FTC-fine precedent):** Check for `gtag`/`fbq`/`mixpanel`/`amplitude`/`posthog`/`segment` sending {DOMAIN_ADJ} data. Marketing site must not link visitors to {DOMAIN_ADJ} identities.

10. **{SENSITIVE_DATA} in crash reports:** Sentry `beforeSend` must strip {SENSITIVE_DATA}, breadcrumbs must not capture {API_PROTOCOL} response bodies with {SENSITIVE_DATA}, session replay disabled on {DOMAIN_ADJ} screens.

11. **{DOMAIN_ADJ}-notes special protection:** Under {DOMAIN_STANDARDS} and {REGULATION} Article {N}, {DOMAIN_ADJ} notes need stricter access controls than general {SESSION_NOUN} data. {ROLE_SUPER} role should NOT access note content.

12. **Consent management:** Consent flags verified before recording, AI analysis, vector embedding, external service calls. Check at every data processing boundary.

**Files to check:** All log statements, error handlers, {TRANSCRIPTION_SERVICE} integration, LLM prompt construction, UI/client storage, media handling, URL construction, package manifests for analytics SDKs, marketing site for tracking pixels, notes resolver access control, consent flag checks.

## 8G — Cryptographic Failures & Secrets Management

1. **Hardcoded secrets & committed key material:** Grep for `sk-`, `{LLM_KEY_PREFIX}`, `pk_live_`, `AKIA`, `Bearer `, `password = "`, base64-encoded credentials, raw PEM private-key block headers, and connection strings with inline `user:password`.

2. **Weak password hashing:** `md5(`/`sha1(`/`sha256(` for passwords. Must use bcrypt (>= cost 10) or argon2id.

3. **Insecure random:** `Math.random()` in security contexts. Must use `crypto.randomBytes`/`crypto.randomUUID`. Python: `random` module in security contexts — use `secrets`.

4. **JWT secret strength:** Minimum length, no fallback defaults, fail hard if missing.

5. **Missing encryption:** DB connections without SSL/TLS, `http://` in API base URLs.

6. **Secrets in version control:** Check `.gitignore` includes `.env*`/`*.pem`/`*.key`, `.env.example` has placeholders not real values.

7. **Weak-secret dictionary:** the JWT/session secret value is checked against a known-weak wordlist (`changeme`, `jwt_secret`, `your-256-bit-secret`), not length alone.

8. **AEAD nonce:** AES-GCM / ChaCha20-Poly1305 nonces and IVs are generated fresh per call from a CSPRNG, never static or hardcoded.

**Files to check:** All `.env*` files, auth/JWT config, password hashing, token generation, DB connection config.

## 8H — Server & Transport Security

1. **CORS misconfiguration:** `origin: '*'` or `origin: true` allows any website. Check credential + wildcard combination.

2. **Missing security headers:** Check for `helmet()` middleware. Missing CSP/HSTS/X-Frame-Options.

3. **SSRF:** `fetch(`/`axios(` where the URL comes from user input, config, or a callback/webhook registration.

4. **Insecure deserialization:** Python `pickle.load`/`yaml.load` without SafeLoader, `node-serialize`.

5. **Rate limiting:** Check for {API_FRAMEWORK} rate-limit middleware. Login endpoint must be rate-limited. {API_PROTOCOL} mutations per-user limited.

6. **{REALTIME_PROTOCOL} security:** Auth on connect, message schema validation, connection limits per user.

7. **Debug mode in production:** `NODE_ENV` checks disabling security, source maps served in prod builds.

8. **SSRF encoding + rebind:** URL allowlists reject encoded-localhost/private-IP forms (hex/decimal/octal) and pin the outbound connection to the validated IP — no DNS re-resolve between check and connect.

9. **Rate-limit key integrity:** the limiter's key is not attacker-chosen — a key derived from `X-Forwarded-For`/`X-Real-IP` is bypassed by header rotation (ties to 8C-7 header-trust).

**Files to check:** {API_FRAMEWORK} middleware stack, CORS config, HTTP client usage, {REALTIME_PROTOCOL} config, rate limiting, environment config branching.

## 8I — Supply Chain & Dependency Security

1. **Lock file integrity:** every project ships a committed lock file for its package manager — enumerate the roster and each project's package manager from root `CLAUDE.md` § Architecture (never assume a fixed list), then confirm each project's lock file exists and is tracked.

2. **Known vulnerabilities:** Check for `node-serialize` (RCE), `lodash` < 4.17.21 (prototype pollution), `jsonwebtoken` < 9.0.0 (algorithm confusion), `express` < 4.19.2 (open redirect).

3. **Dependency confusion:** Check for scoped packages (`@{PROJECT_NAME}/...`), registry pinning.

4. **Post-install scripts:** Check `package.json` for `preinstall`/`postinstall`/`prepare` scripts.

5. **Pinned vs floating versions:** Security-critical deps (auth, crypto, validation) should use exact pins.

6. **Typosquat denylist:** direct dependency names are checked against a typosquat list keyed to the real deps (`loadsh`, `axois`, `djago`, `python3-dateutil`).

7. **Runtime install:** no `pip`/`npm`/`pnpm install` executed from application or skill source at runtime.

**Files to check:** `package.json` files, lock files, `.npmrc`, CI config for audit steps.

## 8J — CI/CD & Framework Supply Chain

1. **Actions script injection:** every `${{ ... }}` expression inside a workflow `run:` block is a shell-injection sink — the value belongs in `env:`/`with:`, referenced as `$VAR`.

2. **Unpinned actions:** third-party CI actions are pinned to a full commit SHA, not a floating tag (`@v4`).

3. **Framework-file trust:** any new or changed file under `.claude/**` or the {AI_SERVICE_NAME} prompt dir gets a static injection/code-exec scan before merge — a marketplace skill or prompt file is untrusted input injected verbatim into an agent or the runtime LLM. The `pfm-guard.sh` gate owns this boundary.

**Files to check:** CI workflow files, `.claude/**`, the {AI_SERVICE_NAME} prompt/knowledge dir.

## 8K — Concurrency & Segregation of Duties

1. **TOCTOU fence:** an ownership/{ORG_UNIT}/role fence is re-checked at the point of use, not evaluated once at the top of an `await`-spanning multi-step handler whose state can change mid-flight.

2. **Atomic state:** read-modify-write on shared {DOMAIN_ADJ} state (consent flags, session/job status, analysis step) is atomic — no lost-update race between concurrent requests or {QUEUE} consumers.

3. **Segregation of duties (ISO 27001:2022 A.5.3):** no single role holds BOTH a {SENSITIVE_DATA}-mutation capability AND the ability to modify/delete the audit-log rows that would record it.

**Files to check:** resolvers/services with multi-step reads-then-writes, {QUEUE} consumers, audit-log write path, consent/erasure services.

## Method & Severity

- **Adversarial verification:** confirm each finding through distinct hostile lenses — a Saboteur (concurrency/state/error-swallow), a New Hire (misused API), a Security Auditor (authz/secret/injection) — surfacing at least one finding per lens. A finding two lenses raise independently is promoted a severity level.
- **State the contract first:** before reading a resolver body, state its authorization contract in one line (who may call, whose data) — then verify the code meets it.
- **CVSS on confirmed findings:** attach a vector as a reproducible severity input — horizontal read ≈ 6.5, horizontal write ≈ 8.1, vertical-to-admin ≈ 8.8, unauthenticated-admin ≈ 9.8.
- **Verification gate:** a scan is done when a re-run after the fix lands clean, not when the report is written.
- **Control mapping (ISO 27001:2022 Annex A):** A.5.15 (access control), A.5.18 (access rights), A.8.3 (info access restriction), A.5.16 (identity management), A.8.4 (source-code access), A.8.16 (monitoring), A.5.34 (PII/privacy).
