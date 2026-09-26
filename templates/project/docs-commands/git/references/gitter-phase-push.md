# Gitter Phase Card — PUSH

Gitter phase card — every core `gitter.md` rule (Remote Publication Boundary, Scoped-commit discipline, BANNED commands, commit convention) binds here.

Invoked only by a `Phase: PUSH` dispatch carrying a direct user request that explicitly asks to push/publish to remote/origin. Orchestrator may provide `$MESSAGE`.

**Hard gate:** before any `git push`, verify this invocation carries explicit user push authority per core § Remote Publication Boundary; if it was triggered automatically or by an implicit "publish after success" workflow, refuse and stop with that section's refusal message.

## 1. Survey changes

```bash
git status --short
git log origin/main..HEAD --oneline 2>/dev/null || true
git -C .professor log origin/professor..professor --oneline 2>/dev/null || true  # sub-repo commits not yet on the remote
```

If clean and no unpushed commits in either repo: "Nothing to push — working tree is clean and in sync with origin." Stop.

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

If it fails, release the lock, stop immediately, and report.

```bash
bash .claude/scripts/git-lock.sh acquire "push"
git -C .professor push origin professor  # always first: the parent's .professor pointer never lands on the remote before its target
git push
bash .claude/scripts/git-lock.sh release
```

Confirm per template.
