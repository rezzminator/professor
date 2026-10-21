# Gitter Phase Card — PUSH

Gitter phase card — every core `gitter.md` rule (Remote Publication Boundary, Scoped-commit discipline, BANNED commands, commit convention) binds here.

Invoked only by a `Phase: PUSH` dispatch carrying a direct user request that explicitly asks to push/publish to remote/origin. Orchestrator may provide `$MESSAGE`.

**Hard gate:** before any `git push`, verify this invocation carries explicit user push authority per core § Remote Publication Boundary; if it was triggered automatically or by an implicit "publish after success" workflow, refuse and stop with that section's refusal message.

## 1. Survey changes

Set `PUSH_REMOTE` and `PUSH_REF` to the parent's authorized remote and branch from the brief and configured upstream; survey that comparison. A missing upstream needs an explicit target, not an assumed branch.

```bash
git status --short
git log '@{upstream}..HEAD' --oneline  # when an upstream exists
```

An optional `.professor` repository is applicable only when `.professor/.git` exists and its reported top-level is `.professor` itself, not the parent. Inspect its current branch/upstream; report failed probes as errors. Set `PROFESSOR_REMOTE` and `PROFESSOR_REF` only for that repository's target authorized by the push brief, with the ref verified against its actual branch. Detached HEAD or a missing target stops its publication. An ordinary `.professor` directory is not another repository.

If clean and no unpushed commits for the applicable authorized targets: "Nothing to push — working tree is clean and in sync with its push targets." Stop.

## 2. Review for dangerous files

If any of these appear staged and aren't gitignored, warn and skip them:

- Secrets: `.env.local`, `.env.test`, `.env`
- Private keys: `*.pem`, `*.key`, `*.cert`
- Cloud credentials: `credentials.json`, `serviceaccount*.json`
- Dependencies: `node_modules/`, `__pycache__/`, `.venv/`
- Junk/logs: `.DS_Store`, `*.log`
- Build artifacts: `dist/`, `build/`, `.next/`, `.expo/`

## 3. Commit

If no `$MESSAGE` provided, generate one from `git diff --stat` (format `<type>: <concise description>`). On `main` — follow core § Scoped-commit discipline: clear the index, stage only the named files (never `-A`, never the dangerous patterns above), verify the staged set, commit, verify the commit:

```bash
git restore --staged .
git add <explicit files>  # only intended paths; NEVER -A — dangerous files stay out
git status --porcelain    # verify ONLY intended paths are staged
git diff --cached --quiet || { git commit -m "$MESSAGE"; git show --stat HEAD; }
```

## 4. Push

If the parent tracks `.professor` as a gitlink, its referenced commit must be available on that repository's authorized remote before publishing the parent pointer. If this requires an unauthorized secondary push, stop and report the missing authority. Push an applicable, authorized secondary target first; otherwise leave it untouched. On any push failure, release the lock, stop immediately, and report.

```bash
bash .claude/scripts/git-lock.sh acquire "push"
if [ -e .professor/.git ] && [ -n "${PROFESSOR_REMOTE:-}" ]; then
  git -C .professor push "$PROFESSOR_REMOTE" "$PROFESSOR_REF"
fi
git push "$PUSH_REMOTE" "$PUSH_REF"
bash .claude/scripts/git-lock.sh release
```

Confirm per template.
