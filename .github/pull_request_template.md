## What and why

## How it was verified

- [ ] `.claude/scripts/dev.sh iso verify templates` — the leak gate and placeholder registry
- [ ] The affected tests, named: `.claude/scripts/dev.sh iso test pfm` or the package run in the fence
- [ ] A bug fix carries a regression test that failed before the fix

## Checklist

- [ ] Targets `develop`
- [ ] No private names, personal data or machine-absolute paths
- [ ] A `.claude/**` change adopters could use moved its `templates/project/**` twin, or this says why not
