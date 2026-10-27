## What and why

## How it was verified

- [ ] `.claude/scripts/test.sh <paths>` over every file the change touched — its affected tests and static checks, the leak scan and placeholder registry among them
- [ ] A bug fix carries a regression test that failed before the fix

## Checklist

- [ ] Targets `develop`
- [ ] No private names, personal data or machine-absolute paths
- [ ] A `.claude/**` change adopters could use moved its `templates/project/**` twin, or this says why not
