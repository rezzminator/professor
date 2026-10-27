# RR — Herdr pane process survival
Question: Find out whether anyone has already built a way to keep the processes running in Herdr panes (AI coding agent chats) alive when the Herdr server crashes or restarts. A per-pane or per-chat PTY-holder daemon, each with its own Unix socket, that the Herdr pane attaches to, the way `tmux -L <socket>` or dtach does.

PARTIAL for Herdr: coordinated live handoff already preserves “pane PTYs and processes,” but ordinary restart means “the original pane processes are gone” ([upstream docs](https://herdr.dev/docs/session-state/)). BUILT as standalone prior art: zmx documents “Daemon per session,” “Each session gets its own unix socket file,” and terminal-state/scrollback retention; no Herdr integration was verified ([zmx](https://github.com/neurosnap/zmx); [checked integration retains Herdr TTY ownership](https://github.com/RaviTharuma/cmux-herdr)).

## 1. Herdr upstream: planned handoff is built; independent crash-surviving holders are not in the inspected implementation

**BUILT — live upgrade, including v0.9.5.** The source trace found actual PTY-master descriptor transfer through `SCM_RIGHTS`, not merely client-state transfer: [v0.9.5 handoff code](https://github.com/herdrdev/herdr/blob/2ecf19a92fc3a7fff75b6ce3655ef04f4625ec01/src/server/handoff.rs#L394-L425), [current transfer code](https://github.com/herdrdev/herdr/blob/a2008661cd4a89ff6df22bf4a6b472bd413018be/src/server/handoff.rs#L418-L421). *(unquoted; commit-pinned code trace)*

The old server spawns a replacement, coordinates restoration/readiness/commit acknowledgments, releases its runtimes after transfer, and exits; this is Unix-only coordinated replacement, not an always-running independent pane holder ([lifecycle](https://github.com/herdrdev/herdr/blob/a2008661cd4a89ff6df22bf4a6b472bd413018be/src/server/headless/lifecycle.rs#L182-L225), [v0.9.5 Unix boundary](https://github.com/herdrdev/herdr/blob/2ecf19a92fc3a7fff75b6ce3655ef04f4625ec01/src/server/headless/lifecycle.rs#L227-L241)). *(unquoted; commit-pinned code trace)*

The user-facing promise is explicit: “A successful handoff preserves long-lived server-owned session state: pane PTYs and processes.” Conversely, after restart “the original pane processes are gone”; supported agent restoration uses official integration-reported session references ([session-state docs](https://herdr.dev/docs/session-state/)). This is the decisive distinction between **planned handoff** and **conversation restoration**.

**NOT BUILT in the inspected upstream ownership path — an external holder per pane.** The Unix pane PTY actor is a thread inside the server, not a separate holder process ([v0.9.5 actor](https://github.com/herdrdev/herdr/blob/2ecf19a92fc3a7fff75b6ce3655ef04f4625ec01/src/pty/actor/unix.rs#L386-L438)). Normal teardown shuts down the actor and terminates its owned session unless handing ownership off; agent restoration creates replacement runtimes ([teardown](https://github.com/herdrdev/herdr/blob/a2008661cd4a89ff6df22bf4a6b472bd413018be/src/pane.rs#L1702-L1718), [replacement runtimes](https://github.com/herdrdev/herdr/blob/a2008661cd4a89ff6df22bf4a6b472bd413018be/src/app/agent_resume.rs#L266-L279)). *(unquoted; commit-pinned code trace)* Normal destructor behavior is not evidence that destructors execute during an abrupt crash.

A test checks original-process continuity during planned handoff; it does not establish arbitrary-crash recovery ([handoff test](https://github.com/herdrdev/herdr/blob/a2008661cd4a89ff6df22bf4a6b472bd413018be/tests/live_handoff.rs#L1113-L1118)). *(unquoted; code/test inspection, not an executed test)*

### Public record checked

- [Issue #4009](https://github.com/herdrdev/herdr/issues/4009): agent detection, not pane survival; fetched status “Closed as not planned.”
- [Issue #5069](https://github.com/herdrdev/herdr/issues/5069): “a first-class, agent-agnostic source of semantic agent state,” not a holder; fetched status “Closed as not planned.”
- [PR #5086](https://github.com/herdrdev/herdr/pull/5086): relevant systemd handoff work reports “MainPID follows the new server and panes stay alive.” The fetched page showed Closed without establishing a merge; do not treat it as a shipped fix.
- The [README](https://github.com/herdrdev/herdr) says that after server/machine restart it restores layout and can resume supported agents, but “the original processes do not survive.” The more specific handoff documentation and code establish the planned-upgrade exception.

Repository and requested issues were reachable. Dedicated roadmap, discussions and release/changelog enumeration was not completed; no universal absence claim is made about them.

## 2. Herdr ecosystem: no verified holder integration found

| Checked project | Evidence | Result for this question |
|---|---|---|
| [herdr-focus-notify](https://github.com/yankewei/herdr-focus-notify) | “Clicking it brings the matching Herdr pane into focus.” | Notification/focus functionality documented; no external-holder integration documented in fetched README. |
| [cmux-herdr](https://github.com/RaviTharuma/cmux-herdr) | “Extra viewers, not TTY takeover”; “cmux terminal surfaces run `attach-pane`; Herdr retains the TTYs.” | Attachment/UI integration, not the requested external PTY custody. |
| [cmux integration discussion](https://github.com/manaflow-ai/cmux/discussions/10106) | “Restore attachment intent, not a copy of inner PTYs.” | Does not establish process preservation outside Herdr. |
| [herdr-sound-plugin](https://github.com/ezstack-dev/herdr-sound-plugin) | HTTP 404 through both attempted fetch methods. | **UNREACHABLE**, not checked absent. Its functionality remains unverified. |

**Verdict: NOT FOUND within the checked public evidence, not a defensible ecosystem-wide NOT BUILT.** No fetched primary source established a Herdr plugin, fork or command wrapper using dtach, abduco, shpool, zmx, diss, tmux or a custom holder to survive Herdr-server death. That is a bounded research result, not proof nobody has done it. The ecosystem digger did not return an exact executed-query log; its suggested follow-up queries must not be misreported as searches already run.

## 3. Existing per-session/PTY holders

**BUILT independently of Herdr.** Screen reconstruction, holder granularity and outer-terminal process discovery are separate questions; the evidence below deliberately does not turn one into a guarantee about another.

| Project | Screen/redraw evidence | Holder/process-tree evidence | License declaration | Release / maintenance evidence |
|---|---|---|---|---|
| [dtach](https://github.com/crigler/dtach) | “dtach does not have a terminal emulation layer.” It is not a retained virtual-terminal screen. | Attaching-terminal descendant visibility not established by fetched docs. | “GPL-2.0 license” repository declaration. | Latest release date and current maintenance **unresolved**. |
| [abduco](https://github.com/martanne/abduco) | Docs acknowledge “problems with incomplete redraws” and recommend dvtm; no full retained VT screen established. | Servers are described as “reparented to the init process”; attaching-terminal process discovery not specified. | “ISC license” repository declaration. | “abduco-0.6 released (24.03.2016).” This announcement does **not** establish that it remains the latest release or that development stopped. |
| [Google-origin shpool](https://github.com/shell-pool/shpool) | “maintains an in-memory render of the terminal state.” Not merely live-byte forwarding. | Sessions belong to the “daemon’s process tree”; per-session independent daemon/socket custody and attaching-terminal discovery were not established. | “Apache-2.0 license” repository declaration. | Latest release date and current maintenance **unresolved**. |
| [zmx](https://github.com/neurosnap/zmx) | “`ghostty-vt` holds terminal state and scrollback”; daemon sends PTY output to both client and ghostty-vt. | “Daemon per session”; “Each session gets its own unix socket file.” This directly matches the requested standalone holder shape. Outer-terminal descendant discovery remains unresolved. | “MIT license” repository declaration. | Latest release date and current maintenance **unresolved**. |
| [diss](https://github.com/yazgoo/diss) | Describes reading server output “as raw bytes and print them in the terminal”; full VT-state retention was not established by that description. | Explicitly “daemonize the process” and “so it is not a child of current terminal.” This documents ancestry separation, not a universal inability to discover its PID. | Registry declares `"license":"MIT"` ([metadata](https://crates.io/api/v1/crates/diss)). | Latest non-yanked registry release `0.3.2`, published `2024-01-16T12:52:00.933547Z`; current maintenance still unresolved ([metadata](https://crates.io/api/v1/crates/diss)). |

License declarations above are primary repository/registry metadata, not separately inspected license texts. Missing release dates are not “unmaintained” verdicts. No fetched evidence establishes any holder surviving **its own** process death.

**Process visibility answer:** diss explicitly documents not being a child of the current terminal; abduco and shpool also document different server/daemon ancestry. No project’s fetched material establishes an end-to-end contract for the particular outer terminal’s descendant-based agent detection. For dtach and zmx, even that visibility conclusion remains unverified; source inspection would be needed rather than guessing from sockets.

## 4. Other AI-agent terminal managers

| Manager | Primary evidence | What is and is not established |
|---|---|---|
| [Claude Squad](https://github.com/smtg-ai/claude-squad) | “tmux to create isolated terminal sessions for each agent.” | **tmux-backed.** Fetched README does not explicitly settle cleanup on UI quit/crash or tmux-server death. |
| [Agent Deck](https://github.com/asheshgoplani/agent-deck) | “every agent-deck session is spawned as `tmux -L agent-deck …`”; “It does not harden agent-deck's own tmux server against bugs inside tmux itself.” | **Dedicated tmux server/socket.** Isolation from other tmux servers is documented; an explicit Agent Deck UI-crash guarantee was not found in verification. |
| [CCManager](https://github.com/kbwo/ccmanager) | “Closing CCManager kills the AI assistant processes it started”; “restoring means starting the same command again, not resuming where it left off.” | **No live-process preservation on ordinary close.** Abnormal-crash behavior and independent-holder implementation remain unresolved. |
| [Conductor](https://www.conductor.build/) | “Create parallel Claude Code, Codex, and Cursor agents in isolated workspaces.” | Homepage fetched; PTY ownership, tmux/custom holder, and UI/server crash boundaries **unresolved**. Initial docs endpoint returned a redirect without page content. |
| [Vibe Kanban](https://github.com/BloopAI/vibe-kanban) | Repository page fetched. | Requested PTY custody and crash-survival boundary **not established**; no architecture claim made. |
| [Crystal](https://github.com/stravu/crystal) | Repository page fetched. | Requested PTY custody and crash-survival boundary **not established**; no architecture claim made. |

No custom per-session/per-socket holder was established for these managers. Agent Deck's documented MCP Unix-socket pooling concerns MCP processes, not PTY custody ([README](https://github.com/asheshgoplani/agent-deck)).

## 5. Live upgrade and limits

| Project / technique | Evidence | Boundary |
|---|---|---|
| **Herdr** | Actual `SCM_RIGHTS` PTY-master transfer in [v0.9.5 source](https://github.com/herdrdev/herdr/blob/2ecf19a92fc3a7fff75b6ce3655ef04f4625ec01/src/server/handoff.rs#L394-L425); docs promise preserved PTYs/processes. *(code mechanism unquoted)* | Coordinated old-to-new server handoff, not an independent per-pane daemon. |
| **[Scribe](https://github.com/sharaf-nassar/scribe)** | “the running server hands off all PTY file descriptors and serialized session state to the new process via `SCM_RIGHTS`.” | A documented real terminal FD-handoff implementation. Separately, its crash wording is “If the server crashes, layout, tabs, pane splits, and AI conversation IDs are restored on next launch.” That does not promise original-process continuity. |
| **[pymux / linked pyterm work](https://github.com/Lillecarl/pymux/issues/553)** | Author reports “Landed at pyterm 6db6a957,” with “one fd holder, never one process per pane.” | Public implementation claim for a separate holder, but **one holder**, not per-pane granularity. Linked implementation was not cloned or independently verified. |
| **[Zellij resurrection](https://zellij.dev/documentation/session-resurrection.html)** | “it will re-run them in command panes.” | Re-executing commands is not preserving original processes; this page does not establish live FD transfer. |
| **[WezTerm mux](https://wezterm.org/multiplexing.html)** | Documents client reconnection and requires “A compatible version of wezterm” remotely. | Client/mux separation and a compatibility requirement, not established mux-server-crash survival or live upgrade. |
| **[kitty sessions](https://sw.kovidgoyal.net/kitty/sessions/)** | Warns that a dangerous command such as `rm` or `mv` “will be re-run when the session is executed.” | Session definitions/re-execution, not proof of original-process preservation. Kitty-death behavior remains unresolved. |
| **tmux / systemd FDStore** | No completed primary-page check for tmux server upgrade; no confirmed terminal project using systemd FDStore. | **Open**, not NOT BUILT. PR #5086's systemd MainPID concern is not evidence that Herdr uses FDStore. |

## Coverage

| Planned sub-area | Lens field | Status | Reports relative to run directory |
|---|---|---|---|
| Herdr implementation | Systems programming | settled | `clone-distiller/herdrdev-herdr-2026-10-21.md` |
| Herdr upstream public record | Software maintenance | settled for inspected issues/docs/PR; broader enumeration explicitly bounded | `sub-rr/01-upstream-record.md` |
| Herdr ecosystem | Software ecosystem research | partial | `sub-rr/02-ecosystem-holders.md`; `sub-rr/06-ecosystem-final-check.md` |
| PTY-holder prior art | Terminal engineering | partial | `sub-rr/03-pty-holder-art.md`; `sub-rr/07-holder-release-check.md` |
| Other agent managers | Reliability engineering | partial | `sub-rr/04-agent-managers.md`; `sub-rr/08-conductor-doc-check.md` |
| Live upgrades | Unix operating systems | partial | `sub-rr/05-live-upgrade-techniques.md`; `sub-rr/09-kitty-session-check.md` |

Digging ended: a round settled nothing and added no sub-area

Two rounds: six dispatched/six returned, then four dispatched/four returned. All ten reported saved artifacts. No software was installed or executed for behavioral testing; source inspection and fetched documentation only. Remaining incomplete answers are intentional coverage gaps, not absence claims.

## Verification

Twelve numbered statements checked against four pages: Herdr session-state docs, Scribe README, zmx README, Agent Deck README. Quotes support the retained documentation claims. Commit-pinned clone findings count as fetched source inspection and were not re-fetched; those without returned verbatim code excerpts retain the **unquoted** mark.

**NOT ON PAGE — excluded from factual conclusions:**
- An explicit arbitrary-server-crash exclusion in the Herdr session-state documentation. The documentation describes planned handoff and restart; the inspected code, separately, establishes server-local ownership.
- A zmx guarantee that failures are isolated between sessions. The page supports a daemon per session, not that stronger fault-isolation guarantee.
- An explicit Agent Deck UI-crash survival guarantee. The page supports its tmux architecture and tmux-server limitation, not that stronger guarantee.

**UNCHECKED due to verification-fetch errors:** none. Agent Deck's verification returned the first 100000 characters; the final 11354 characters were not read, so the no-quote result is limited to that fetched portion.

## Open rabbit holes

- **dug, unsettled:** A verified Herdr plugin/fork/wrapper with independent per-pane custody. Checked cmux integration retains Herdr TTY ownership; sound-plugin returned HTTP 404. Suggested next queries, **not an executed-query log**: `herdr zmx`; `Herdr fork dtach abduco shpool zmx diss tmux`; `"ezstack-dev/herdr-sound-plugin" README fork mirror`.
- **undug:** Complete Herdr roadmap/discussion/release enumeration and the disposition of linked systemd issue #4972: `site:github.com/herdrdev/herdr/discussions "server crash"`; `site:github.com/herdrdev/herdr/releases handoff`; `herdr issue 4972 systemd live handoff`.
- **dug, unsettled:** Latest release dates and dated maintenance for dtach, abduco, shpool and zmx; ongoing diss maintenance and separately inspected license texts. Queries: `crigler dtach releases`; `martanne abduco latest release`; `shell-pool shpool releases`; `neurosnap zmx releases`; `yazgoo diss commits LICENSE`.
- **dug, unsettled:** Holder-specific redraw guarantees, own-holder death behavior and attaching-terminal process-tree detection. Source inspection would be needed for dtach, abduco, shpool, zmx and diss; do not infer visibility from daemon/socket architecture alone.
- **dug, unsettled:** Explicit manager failure contracts: Claude Squad cleanup on UI death, Agent Deck UI-crash behavior, CCManager abnormal-crash behavior, and Conductor/Vibe Kanban/Crystal PTY ownership. Conductor homepage links `/docs`, not followed in this run.
- **dug, unsettled:** WezTerm mux-server death and kitty process lifetime versus session re-execution. Queries: `wezterm mux server crash panes lost`; `kitty crash session restore running processes`.
- **undug:** Independently inspect the pyterm implementation linked from pymux #553; confirm holder/upgrade protocol and failure boundary rather than relying on the issue author's implementation claim.
- **undug:** Primary tmux server-upgrade/crash evidence and any real terminal FDStore user. Queries: `site:github.com/tmux/tmux upgrade server SCM_RIGHTS`; `terminal PTY "FDSTORE=1"`.
- **undug:** Newer candidate `mmonad/zmosh`, surfaced but not fetched. No claim about its implementation is included.
