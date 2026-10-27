# RR — Herdr pane process survival
Question: Find out whether anyone has already built a way to keep the processes running in Herdr panes (AI coding agent chats) alive when the Herdr server crashes or restarts. Answer numbered questions 1–5 about upstream handoff, ecosystem, PTY holders, agent managers, and live-upgrade techniques.

Determine what survives; distinguish restore from preservation; find existing integrations; compare holder semantics and live upgrades.

Herdr implementation — settled when commit-pinned code establishes PTY ownership, update handoff contents and failure limits.
Lens: systems programming — method: trace ownership and lifecycle; sources: repository code and tests.
Herdr upstream public record — settled when issues 4009 and 5069 and bounded issue/PR/docs searches establish explicit proposals or absence within checked surfaces.
Lens: software maintenance — method: issue and change-history audit; sources: issues, PRs, changelogs, docs.
Herdr ecosystem — settled when named plugins and bounded public fork/integration searches establish an existing holder integration or precisely bounded non-discovery.
Lens: software ecosystem research — method: repository and integration discovery; sources: plugin repositories, fork docs, public issues.
PTY-holder prior art — settled when dtach, abduco, shpool, zmx and diss have cited screen, maintenance/release, license and process-visibility findings, or named unresolved gaps.
Lens: terminal engineering — method: compare documented PTY and screen semantics; sources: project manuals, releases, license files.
Other agent managers — settled when named managers have documented UI/server failure boundaries and tmux/custom-holder status, with inaccessible or ambiguous projects named.
Lens: reliability engineering — method: identify fault domains from architecture docs; sources: project READMEs and architecture documentation.
Live upgrades — settled when at least three cited entries distinguish FD transfer, independent holders and unsupported crash recovery, including multiplexer limits.
Lens: Unix operating systems — method: distinguish descriptor lifetime and process ownership; sources: protocol and system manuals, multiplexer documentation.

Opening page: https://github.com/herdrdev/herdr explicitly says original processes do not survive server or machine restart; leaves update --handoff, implementation and integrations unexplained.
Budget: about 40 web searches/fetches total; <=6 diggers/round, <=5 rounds, <=12 verification pages. No installs, no execution of external software, no design recommendations. Clone reads are source inspection only.
