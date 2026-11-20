# The professor plugin's checks: its tests, the type-check and Claude Code's
# strict validation of the installable folder plugins/professor/. Professor's
# ci.yml scan job runs them through the root Makefile's plugin-check; the
# plugin ships in Professor's own release, so it has no release target. CI's
# runner has Node 22 already; locally mise gives it.
RUN := $(if $(CI),,mise exec node@22 --)

.PHONY: check

check:
	$(RUN) npm ci
	$(RUN) npm test
	$(RUN) npm run typecheck
	CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1 $(RUN) npm run validate:plugin
