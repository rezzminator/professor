# 2026-10-15 — a spec placed shared data in a package that imports its consumer

Observed: engine-sync-tidy spec row 32 held the retired store paths once in `pfm/internal/hostcheck`, but `hostcheck` imports `installer`, which also reads them; the builder put the single copy in `installer` (`installer.RetiredGeneratedStores`) and reported the deviation. The same unit paid two build reds the spec did not price: arch `C13` (every new `x.go` needs its `x_test.go`, already in the testing manual's `check-pfm.sh` line) and `goconst` forcing named constants over repeated registry literals.

Amend: templates/global/agents/flights-speccer.md#The run (step 3, Dependencies) — data a spec places for several consumers lives in the package every consumer already imports; check the import direction between the holder and each consumer before placing it.

Resolved: 2026-10-18 — moot: flights-speccer is deleted; requirements settle in the main chat before dispatch (`pfm/harness-prompts/share/tail.md` § Orchestration)
