# RR — Browser-automation MCP servers for coding agents (Oct 2026): adopt vs fork for a lean, CDP-attached, logged-in Brave MCP

Question: Map the field of browser-automation MCP servers and toolkits that a coding agent (Claude Code, Codex) can use, as of today, 2026-10-10. Verify everything against current sources. [...] We are deciding whether to keep a browser MCP server we're building ourselves, or to adopt or fork an existing one. [Dedicated logged-in Brave (Chromium 154) on macOS, one process per profile, lean output: compact page views with stable refs, change-only responses, several actions per call, per-response size limit, per-chat tab isolation.] Deliverables: comparison table plus cited notes; ranked top 3 for (a) adopt as-is over CDP and (b) best base to fork or distill; top 1–2 benchmark additions; what could not be verified.

## Answer

None of the three servers whose code was read has per-client tab scoping or a per-response size cap.
- Chrome DevTools MCP's `list_pages` shows every tab in the attached browser ([code](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/McpContext.ts#L634-L648)).
- Playwright MCP's `--cdp-endpoint` reuses the browser's default context and every page already open in it ([code](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/mcp/program.ts#L140-L160)).
- agent-browser's MCP results carry the payload in both `content` and `structuredContent`, with no truncation ([code](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/mcp.rs#L3989-L4018)).

So your own build is not duplicated by anything available. If you adopt instead, Chrome DevTools MCP ranks first for both decisions:
- It attaches to any CDP URL through `puppeteer.connect` with no browser-vendor check ([code](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/BrowserManager.ts#L340-L363)).
- It already has an Apache-2.0 Brave fork published on npm as `brave-mcp` ([npm](https://registry.npmjs.org/brave-mcp)).
- It is a standalone Apache-2.0 TypeScript repository ([GitHub API](https://api.github.com/repos/ChromeDevTools/chrome-devtools-mcp)).

The best ideas to distill are agent-browser's refs keyed by backendNodeId and its `--delta` snapshots ([code](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/native/snapshot.rs#L526-L543)).

## Comparison table

Legend: "code" = read from the cloned repository at a pinned commit (see Coverage). "–" = not found. Rows below the line are out of scope for a local logged-in Brave.

| Candidate | License / lang | Latest release | Engine | Attach to running Chromium | Brave evidence | Default tools | Page view / refs stable? | Change-only / batch / size cap | Per-client tab isolation | LLM inside | Fork shape |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Chrome DevTools MCP | Apache-2.0, TS | 1.10.1, 2026-09-23 | puppeteer-core (bundled) | `--browserUrl`, `--wsEndpoint`, `--autoConnect` | none official; fork `brave-mcp` | ~30 of 64 (code grep); `--slim` = 3 | a11y tree, uid; stable until navigation (code) | no diff; `includeSnapshot` opt-in; `fill_form` batch; no cap | no: sees all tabs | no | standalone repo; src 23.9k LOC, tools 4.95k |
| brave-mcp (triuzzi fork of above) | Apache-2.0 | 1.10.1 (npm modified 2026-10-06) | same | `--autoConnect` to running Brave | yes (purpose) | as upstream | as upstream | as upstream | as upstream | no | fork tracking upstream |
| @playwright/mcp | Apache-2.0, TS | 0.0.83 (Sep 28) | Playwright | `--cdp-endpoint`, `--extension` (Chrome/Edge) | one unofficial config (0.0.68) | 25; 72 with all caps (code) | aria YAML `[ref=eN]`; stable (code) | full snapshot after each action; `fill_form`, `run_code_unsafe`; no cap, `filename` offload | no: CDP reuses default context | no | lives in microsoft/playwright monorepo, ~8.7k LOC, uses internals |
| @playwright/cli | Apache-2.0 | – (no GitHub releases) | Playwright | – | – | CLI + skills, not MCP | – | built to avoid schemas and full trees | – | no | in Playwright |
| agent-browser | Apache-2.0, Rust | v0.39.0, Oct 9 | direct CDP (Rust daemon) | `--cdp`, `--auto-connect` (reads Brave's DevToolsActivePort) | auto-connect path; Brave 154 run worked | core 29; all 151 (code) | a11y tree `@eN`; stable via backendNodeId (code) | `delta` snapshot; `batch` tool (debug profile); no MCP cap | `--session` = separate daemon; default shared | no | one 5.4k-line `mcp.rs`, hand-rolled JSON-RPC, shells out per call |
| dev-browser | MIT | – | Puppeteer | `--connect` to open browser | – | 5 | a11y tree, "stable element refs" | not mentioned | – | no | standalone |
| Playwriter | MIT | – | Playwright via extension | extension on a tab | – | 1 (`execute`) | a11y 5–20KB | – | – | no | standalone |
| Browser Use MCP | MIT, Python | 0.13.11 (Oct 7) | raw CDP (cdp-use) since Aug 2025 | not in MCP docs | – | – | indexed DOM | – | own launched instance | yes (needs key; agent tool) | large framework |
| Stagehand | MIT, TS/Py/Go | 3.7.3 marked Latest; v4 docs exist | CDP | `cdpUrl` / `localBrowser.connect()` | – | (SDK) | – | – | – | yes (act/observe/extract) | monorepo |
| Browserbase MCP | Apache-2.0 | archived 2026-07-20 | Stagehand + cloud | no | – | 6 | – | – | – | yes | archived |
| BrowserMCP (browsermcp.io) | Apache-2.0 | no GitHub releases | extension (adapted Playwright MCP) | extension only | Chrome only named | – | – | – | – | no | can't build standalone |
| Claude in Chrome | proprietary (unverified) | – | extension + native messaging | extension | DISPUTED | – | – | `browser_batch` exists | tab group per session | Claude is the model | closed |
| ExecuteAutomation mcp-playwright | MIT | npm 1.0.12, 2025-12-12 | Playwright | none documented | – | 28 (docs count) | visible text / HTML / screenshot | – | – | no | standalone |
| Puppeteer reference server | MIT | npm 2025.5.12, deprecated | Puppeteer | launch only | – | 7 | selectors + screenshot | – | – | no | archived 2025-05-29 |
| raw-CDP small fry (chromium-cdp-mcp, cdp-browser-mcp, mcp-playwright-cdp, even-better-playwright-mcp) | mixed | – | raw CDP / Playwright | yes (port) | claimed only | 26 / – | even-better: `[ref=eN]`, `showDiff` | even-better: diff, 50KB network cap | – | no | 1–52 stars |
| hangwin/mcp-chrome, real-browser-mcp, Agent360 Browser MCP, Chrome-MCP | MIT | Agent360 v1.30.1 2026-10-07 | extension + local bridge | extension | not mentioned | 20+ / 18 / 40 / 37 | compact a11y (real-browser) | – | – | no | small |
| Steel, Hyperbrowser, Browserless, Cloudflare MCP | MIT / MIT / SSPL-1.0 / Apache | – | cloud | own sessions only | n/a | 16 / 10 / 14 / 13 | – | – | – | Hyperbrowser has agent tools | – |

## Map

### 1. Chrome DevTools MCP and the reference Puppeteer server (settled)
- **Chrome DevTools MCP: license, language, stars, activity.** Apache-2.0, TypeScript, `"stargazers_count":53233`, `"pushed_at":"2026-10-09T15:00:38Z"` ([GitHub API](https://api.github.com/repos/ChromeDevTools/chrome-devtools-mcp)).
- **Latest release.** v1.10.1, "(2026-09-23)" ([releases](https://github.com/ChromeDevTools/chrome-devtools-mcp/releases)).
- **Officially supported browsers.** "officially supports Google Chrome and Chrome for Testing only", while "Other Chromium-based browsers may work" ([README](https://raw.githubusercontent.com/ChromeDevTools/chrome-devtools-mcp/main/README.md)). Brave support was requested in issue #889, which is open ([issue](https://github.com/ChromeDevTools/chrome-devtools-mcp/issues/889)).
- **Engine and attach (code).** It runs on puppeteer-core, which Rollup bundles into the build ([third_party](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/third_party/index.ts#L59-L61)).
  - `--wsEndpoint` and `--browserUrl` go straight to `puppeteer.connect` ([BrowserManager](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/BrowserManager.ts#L340-L363)).
  - `--autoConnect --userDataDir` reads `DevToolsActivePort` from that directory. Without `--userDataDir` it assumes Chrome's own profile ([autoConnect](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/BrowserManager.ts#L364-L400)).
  - No browser-vendor check was found.
- **Snapshot and uids (code).** uids map from `${loaderId}_${backendNodeId}`, so they stay stable across snapshots until a navigation ([TextSnapshot](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/TextSnapshot.ts#L73-L89)).
  - There is no diff snapshot. A snapshot after an action is opt-in through `includeSnapshot` ([input.ts](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/tools/input.ts#L148-L150)).
  - The docs say "Always use the latest snapshot" ([tool reference](https://raw.githubusercontent.com/ChromeDevTools/chrome-devtools-mcp/main/docs/tool-reference.md)).
- **Output size (code).** There is no overall response cap.
  - Network and console listings page only on request ([network.ts](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/tools/network.ts#L44-L58)).
  - Network bodies are cut at 10000 characters ([formatter](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/formatters/NetworkFormatter.ts#L256-L261)).
  - There is no markdown or text view. The only batch tool is `fill_form` ([fill_form](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/tools/input.ts#L497-L510)).
- **Tools.** The code has 64 tool definitions, about 30 of them on by default (grep count, not checked tool by tool).
  - `--slim` registers only screenshot, navigate and evaluate ([configuration.md](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/docs/configuration.md#L222-L223)).
  - Tools behind a disabled flag are still registered, then switched off with `.disable()` ([index.ts](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/index.ts#L313-L330)).
- **Isolation (code).** `list_pages` shows every tab except `devtools://` pages ([McpContext](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/McpContext.ts#L634-L648)). Page-ID routing adds a `pageId` parameter ([ToolDefinition](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/src/tools/ToolDefinition.ts#L472-L477)). Nothing ties a tab to a client.
  - Per the docs, `--isolated` "avoids sharing the default Chrome DevTools MCP user data directory" ([advanced usage](https://raw.githubusercontent.com/ChromeDevTools/chrome-devtools-mcp/main/docs/advanced-usage.md)).
- **Fork shape (code).** `src` is 23,886 lines, of which `src/tools` is 4,951. There are no runtime `dependencies`; everything is bundled ([package.json](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/f08dbe152502d66e75fa07fb2588dc0feb42bc20/package.json#L56-L62)).
- **Brave fork `brave-mcp`.** npm shows `"license":"Apache-2.0"`, `"latest":"1.10.1"`, repo `triuzzi/brave-devtools-mcp`, `"modified":"2026-10-06T10:39:55.912Z"` ([npm](https://registry.npmjs.org/brave-mcp)).
  - The Brave Community post says it "is built from Chrome DevTools MCP upstream and kept in sync while preserving Brave-specific behavior" ([Brave Community](https://community.brave.app/t/brave-devtools-mcp-agent-native-browser-automation-for-brave/656905)) (unquoted at step 6).
- **Reference Puppeteer server.** The repo was "archived by the owner on May 29, 2025", license MIT, 7 `puppeteer_*` tools ([servers-archived](https://github.com/modelcontextprotocol/servers-archived)).
  - It only launches a browser: `browser = await puppeteer.launch(deepMerge(` ([index.ts](https://raw.githubusercontent.com/modelcontextprotocol/servers-archived/main/src/puppeteer/index.ts)).
  - npm marks it `"deprecated"` with latest `2025.5.12`, published `2025-05-12T21:29:35.151Z` ([npm](https://registry.npmjs.org/@modelcontextprotocol/server-puppeteer)).

### 2. The Playwright family (settled)
- **@playwright/mcp package.** npm shows `"version":"0.0.83"`, `"license":"Apache-2.0"`, and dependencies `playwright` / `playwright-core` `1.64.0-alpha` ([npm](https://registry.npmjs.org/@playwright/mcp/latest)). The repo has "38.0k stars" ([repo](https://github.com/microsoft/playwright-mcp)).
- **Where the code lives.** The microsoft/playwright-mcp repo now says "Playwright MCP source code is located in the Playwright monorepo" ([src](https://github.com/microsoft/playwright-mcp/tree/main/src)).
  - Git history: moved into the monorepo on 2025-09-03 (#37286), into playwright-core on 2026-02-26 (#39440), and to `packages/playwright-core/src/tools/` on 2026-03-11 (#39632). The full timeline is in the clone-distiller file (see Coverage).
- **Tools (code).** 25 tools by default. The caps add vision 6, pdf 1, testing 5, network 4, config 1, devtools 13 and storage 17, giving 72 in total ([tools.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/backend/tools.ts#L83-L84), [capabilities.spec.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/tests/mcp/capabilities.spec.ts#L19-L46)).
  - This explains both third-party counts, 24 and 72 ([oodle.ai](https://www.oodle.ai/guides/mcp/playwright), [browsermcp.dev](https://browsermcp.dev/compare/playwright-mcp)).
- **Refs (code).** Each ref is cached on its element as `_ariaRef` and changes only when the element's role or name changes, so refs stay stable ([ariaSnapshot.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/injected/src/ariaSnapshot.ts#L231-L237)).
  - `--snapshot-mode` is only `full` or `none` ([program.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/mcp/program.ts#L78-L78)).
  - Action tools attach a full snapshot to every response ([navigate.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/backend/navigate.ts#L37-L37)).
  - `browser_snapshot` takes `target`, `depth` and `filename` ([snapshot.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/backend/snapshot.ts#L38-L55)).
- **Output size (code).** Nothing is truncated inline. Large output goes to files through `filename` params ([console.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/backend/console.ts#L29-L29)).
  - `--output-max-size` only prunes old files in the output directory ([response.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/backend/response.ts#L247-L262)).
  - The batch-style tools are `browser_fill_form` ([form.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/backend/form.ts#L23-L34)) and `browser_run_code_unsafe` ([runCode.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/backend/runCode.ts#L32-L35)).
- **CDP attach (code).** `connectOverCDP` with `noDefaults: true` ([browserFactory.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/mcp/browserFactory.ts#L66-L72)), and `isolated` is forced off ([config.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/mcp/config.ts#L197-L198)).
  - It reuses `browser.contexts()[0]` ([program.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/mcp/program.ts#L140-L160)).
  - It registers every page already open in that context ([context.ts](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/backend/context.ts#L471-L473)). Tabs are therefore not scoped per client.
  - The docs say the CDP endpoint "Works with Chrome/Chromium started with `--remote-debugging-port`, Edge, Electron apps" ([playwright.dev](https://playwright.dev/mcp/configuration/browser-extension)).
- **Fork shape (code).** `tools/mcp` is 2,615 LOC and `tools/backend` 6,073 LOC. Resolving refs depends on the internal `aria-ref` engine and injected code ([mcp/DEPS.list](https://github.com/microsoft/playwright/blob/d9f2fd3e2232ace8e317a84eda6cb86e25bcf49c/packages/playwright-core/src/tools/mcp/DEPS.list#L1-L12)).
- **@playwright/cli.** "Playwright comes with playwright-cli, a command-line interface for browser automation designed for coding agents."
  - "CLI commands avoid loading large tool schemas and verbose accessibility trees into the model context." ([playwright.dev](https://playwright.dev/docs/getting-started-cli))
- **ExecuteAutomation mcp-playwright.** MIT, "5.7k stars", no GitHub releases ([repo](https://github.com/executeautomation/mcp-playwright)).
  - npm latest `1.0.12`, published `2025-12-12T08:08:12.010Z` ([npm](https://registry.npmjs.org/@executeautomation/playwright-mcp-server)).
  - 28 tools by count, output as visible text, HTML or screenshot, and no CDP option documented ([docs](https://executeautomation.github.io/mcp-playwright/docs/playwright-web/Supported-Tools)).

### 3. agent-browser and the raw-CDP / new entrants (settled)
- **agent-browser.** "Apache-2.0 license", "43.8k stars" ([repo](https://github.com/vercel-labs/agent-browser)).
  - v0.39.0 was "released this 09 Oct 18:57" and "Added an experimental Obscura engine" ([releases](https://github.com/vercel-labs/agent-browser/releases)).
  - The daemon is a "Pure Rust daemon using direct CDP, no Node.js required" ([README](https://raw.githubusercontent.com/vercel-labs/agent-browser/main/README.md)).
- **agent-browser MCP layer (code).** It is one file, `cli/src/mcp.rs`, 5,354 lines with about 1,000 of tests. It uses no MCP SDK ([Cargo.toml](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/Cargo.toml#L13-L30)).
  - Each tool call starts the binary again with `--json` ([spawn](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/mcp.rs#L3895-L3900)).
  - The `core` profile has 29 tools and `all` has 151 ([profiles](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/mcp.rs#L339-L368)).
- **Why your responses carried the payload 2–3x (code).** Every result sets `content[0].text` and `structuredContent`, and `structuredContent` holds both the raw stdout and the parsed response ([builder](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/mcp.rs#L3989-L4018)).
- **Refs and delta (code).** `@eN` refs are keyed by session, frame and backendNodeId, and the counter is never reset ([snapshot.rs](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/native/snapshot.rs#L526-L543)).
  - The MCP snapshot tool exposes `interactive`, `compact`, `depth`, `selector` and `delta` ([schema](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/mcp.rs#L836-L850)).
  - `delta` returns `unchanged` or ref ops, and falls back to a full snapshot when the delta is at least 70% of the tree ([actions.rs](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/native/actions.rs#L5893-L5902)).
  - There is no MCP truncation, because `--max-output` applies only in text mode.
- **Batch (code).** `agent_browser_batch` takes an array of argv arrays plus `bail`, and it is in the `debug` profile, not `core` ([schema](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/mcp.rs#L1630-L1636)).
- **Attach (code).** `--cdp` and `--auto-connect` attach to every existing page target ([browser.rs](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/native/browser.rs#L762-L780)).
  - Auto-connect reads `DevToolsActivePort` from `"BraveSoftware/Brave-Browser"` among other defaults, but not from a custom user-data-dir ([chrome.rs](https://github.com/vercel-labs/agent-browser/blob/44af39842650f0bb9c1afb7354df9a82921d4f09/cli/src/native/cdp/chrome.rs#L1284-L1297)).
  - Each `--session` gets its own daemon. MCP clients that omit `session` share `default`.
- **dev-browser.** "MIT", "6.7k stars", "Real Puppeteer."
  - "connect to a browser you already have open."
  - "Accessibility trees give agents readable output and stable element refs." ([repo](https://github.com/SawyerHood/dev-browser))
- **Playwriter.** "MIT", "4.0k stars", "1 `execute` tool".
  - "Click extension icon on a tab → turns green when connected."
  - "A11y snapshots (5-20KB)" ([repo](https://github.com/remorses/playwriter))
- **Small raw-CDP servers.** These have 1–52 stars, so none is an adoption candidate:
  - chromium-cdp-mcp: 26 tools, attaches to "an **already-running** Chromium instance", 3 stars ([repo](https://github.com/undetectable-io/chromium-cdp-mcp)).
  - cdp-browser-mcp: Brave "not claimed as supported" ([repo](https://github.com/David-chinese-wei/cdp-browser-mcp)).
  - mcp-playwright-cdp: in "maintenance mode", v0.2.10 ([repo](https://github.com/lars-hagen/mcp-playwright-cdp)).
  - even-better-playwright-mcp: `[ref=e1]` refs, `showDiff` "Show changes since last snapshot", `--cdp-endpoint`, 2 stars ([repo](https://github.com/SJMakin/even-better-playwright-mcp)).
- **Token benchmarks.** All are single-author or vendor posts and disagree with each other, so treat them as indicative only:
  - Cheparsky reports total input tokens "Playwright ~145K, Agent Browser ~110K, Chrome DevTools ~78K" ([Medium](https://medium.com/@cheparsky/ai-in-testing-6-giving-ai-eyes-and-hands-which-browser-mcp-actually-works-039de2b21ea2)).
  - isagentready puts tool definitions at "Playwright MCP costs roughly 13,700 tokens. Chrome DevTools MCP costs around 17,000." ([isagentready](https://isagentready.com/en/blog/vercel-agent-browser-why-a-cli-beats-mcp-for-browser-automation))

### 4. LLM-in-the-loop frameworks (settled)
- **Browser Use local MCP.** The server "requires your own LLM API keys".
  - `retry_with_browser_use_agent` will "Run a complete browser automation task with an AI agent".
  - The launch command is `browser-use --mcp`, and the docs never mention attaching to an existing browser ([docs](https://docs.browser-use.com/open-source/customize/integrations/mcp-server)).
  - The engine changed in 2025: "All interactions are now 100% CDP-based—no more Playwright dependencies" ([changelog](https://browser-use.com/changelog/19-8-2025)).
  - MIT, "117.5k stars" ([repo](https://github.com/browser-use/browser-use)).
- **Stagehand.** MIT, a "TypeScript, Python, and Go monorepo" ([repo](https://github.com/browserbase/stagehand)).
  - v4 "`localBrowser.connect()` attaches to a running Chromium browser through its DevTools endpoint" ([docs](https://docs.stagehand.dev/v4/configuration/browser)).
  - act, observe and extract need an LLM key ([v3 ref](https://docs.stagehand.dev/v3/references/stagehand)).
- **Browserbase MCP.** "This repository was archived by the owner on Jul 20, 2026."
  - "This server exposes 6 tools."
  - "When using any custom model (non-default), you must provide your own API key." It is Apache 2.0 ([repo](https://github.com/browserbase/mcp-server-browserbase)).

### 5. Extension-based servers (settled)
- **Claude in Chrome.** The Claude Code docs say it "detects the extension and sets up the connection in other Chromium-based browsers, including Brave."
  - "The extension collects the tabs Claude opens into a Chrome tab group tied to your session." It needs "A direct Anthropic plan" and starts with `claude --chrome` ([docs](https://code.claude.com/docs/en/chrome)).
  - **DISPUTED:** the support article says "Claude in Chrome is not supported on other Chromium-based web browsers" ([support](https://support.claude.com/en/articles/12012173-get-started-with-claude-in-chrome)).
- **BrowserMCP (browsermcp.io).** "Apache-2.0 license", "adapted from the Playwright MCP server", "7.2k stars".
  - It "cannot yet be built on its own due to dependencies on utils and types from the monorepo" ([repo](https://github.com/browsermcp/mcp)).
  - There are no GitHub releases ([releases](https://github.com/browsermcp/mcp/releases)).
- **Playwright extension mode.** `--extension` is to "Connect to a running browser instance (Edge/Chrome only)" ([README](https://raw.githubusercontent.com/microsoft/playwright-mcp/main/README.md)).
- **Chrome DevTools MCP `--autoConnect`.** On Chrome 144+ it requires enabling remote debugging at `chrome://inspect/#remote-debugging`, and "Chrome shows a dialog asking for permission" ([developer.chrome.com](https://developer.chrome.com/docs/devtools/agents/get-started/configuration)).
- **Other extension servers.** None mentions Brave:
  - hangwin/mcp-chrome: MIT, "12.5k stars", "20+ Tools" ([repo](https://github.com/hangwin/mcp-chrome)).
  - real-browser-mcp: 18 tools, compact snapshots ([repo](https://github.com/ofershap/real-browser-mcp)).
  - Agent360 Browser MCP: "40 tools, MIT" ([repo](https://github.com/Agent360dk/browser-mcp)).
  - Chrome-MCP: its README admits "any local process that reaches 127.0.0.1:7890 could impersonate the extension" ([repo](https://github.com/hmumu230-ops/Chrome-MCP)).

### 6. Cloud servers (settled; none attaches to a local Brave)
- **Browserless MCP.** License "SSPL-1.0", "Official MCP server for the Browserless.io", 14 tools.
  - It "can also be run locally — useful for air-gapped deployments or pointing at a self-hosted Browserless instance" ([repo](https://github.com/browserless/browserless-mcp)).
- **Steel.** steel-mcp-server is MIT with 16 default tools, and `STEEL_LOCAL=true` works with a self-hosted steel-browser ([repo](https://github.com/steel-dev/steel-mcp-server)) (license unquoted).
- **Hyperbrowser.** 10 tools, including `browser_use_agent` and `claude_computer_use_agent` ([repo](https://github.com/hyperbrowserai/mcp)).
- **Cloudflare.** "Any CDP-compatible client, including Puppeteer and Playwright, can connect" ([changelog](https://developers.cloudflare.com/changelog/post/2026-04-10-browser-rendering-cdp-endpoint/)).
  - With chrome-devtools-mcp, "--wsEndpoint ... creates a new browser session" ([docs](https://developers.cloudflare.com/browser-run/cdp/mcp-clients/)).

### 7. Brave CDP attach constraints (added; partial)
- **Chrome's rule.** Chrome 136 made the debugging switches "no longer be respected if attempting to debug the default Chrome data directory" ([Chrome blog](https://developer.chrome.com/blog/remote-debugging-port)).
- **Brave, non-default directory.** A run on "Brave Browser 154.1.96.61 (Chromium 154.0.8037.98)" with `--user-data-dir=/tmp/cdp-profile-9222` reported "everything worked with Brave".
  - The same report notes "The debug flag cannot be added to a live process." ([GenesisTools #454](https://github.com/genesiscz/GenesisTools/issues/454))
- **Brave, default directory.** A fork README states "Brave requires you to use a non-default user data directory when enabling the remote debugging port" ([neolivz](https://github.com/neolivz/brave-devtools-mcp)) (unquoted at step 6).
  - No Brave maintainer or Chromium source confirms it.
  - Your setup uses a dedicated profile with its own data directory, so this rule does not block you.

## Rankings (analysis built on the map above)

**(a) Adopt as-is, pointed at your Brave over CDP**
1. **Chrome DevTools MCP with `--browserUrl`/`--wsEndpoint`, or `brave-mcp`.**
   - For: connects to any CDP URL with no vendor check; uids are stable; snapshots after actions are opt-in, so responses can stay small; `fill_form` batching; `--slim`; Apache-2.0; most active (53k stars, pushed 2026-10-09).
   - Against: no diff snapshots, no response cap, and every client sees all tabs.
2. **@playwright/mcp with `--cdp-endpoint`.**
   - For: stable refs, 25 tools, and the tools/list you measured is the smallest of those tested.
   - Against: a full snapshot follows every action (your 50.9k-character Hacker News result); it adopts the default context and every tab; and the code now ships from the Playwright monorepo.
3. **agent-browser with `--cdp`.**
   - For: the only mainstream option with change-only snapshots (`delta`), stable refs, batching and a Brave-aware auto-connect.
   - Against: MCP results duplicate the payload through `structuredContent`; the tools/list is large; and batch sits outside `core`.
   - Usable only if your host ignores `structuredContent` or you call it as a CLI.
   - Not ranked: Claude in Chrome (Brave support is disputed, and it needs an Anthropic plan); Browser Use and Stagehand (they run their own LLM); cloud servers (they cannot reach your local Brave).

**(b) Best base to fork or distill**
1. **Chrome DevTools MCP.**
   - Standalone Apache-2.0 TypeScript repository; the tools layer is 4,951 lines.
   - The uid map keyed by `loaderId_backendNodeId` matches your design.
   - Cost: it brings puppeteer-core, which you would drop or replace with your direct CDP.
2. **agent-browser, distilled rather than forked.**
   - It is Rust, so the useful part is the algorithms: backendNodeId-keyed refs that never reset, `delta` with a 70% fallback, and argv-array batch.
   - Apache-2.0 allows porting.
3. **Playwright MCP.**
   - Its ref design is good, but extracting it is hard: about 8.7k LOC tied to playwright-core internals and injected aria-ref code.
   - Worth honourable mention: dev-browser (MIT, Puppeteer, 5 tools, stable refs, `--connect`), whose code was not read.

**Benchmark additions**
1. **Chrome DevTools MCP (or `brave-mcp`) via `--browserUrl` against your Brave.** It is the strongest adoption alternative you have not measured, and the only one whose action responses can omit snapshots by default.
2. **agent-browser again with `delta: true` and the `core,debug` profiles (for batch).** This shows whether its change-only path beats yours once `structuredContent` duplication is excluded or stripped. If you would rather measure a new tool, `@playwright/cli` is Microsoft's own token-lean answer for coding agents.

## Coverage
1. Chrome DevTools MCP + Puppeteer reference — settled
2. Playwright family (@playwright/mcp, @playwright/cli, ExecuteAutomation) — settled
3. agent-browser + raw-CDP / new entrants — settled
4. Browser Use / Stagehand / Browserbase — settled
5. Extension-based — settled
6. Cloud — settled
7. Brave CDP attach constraints (added) — partial: whether Brave's default profile refuses CDP is not confirmed by any primary source

Code reads: `clone-distiller/ChromeDevTools-chrome-devtools-mcp-2026-10-10.md`, `clone-distiller/microsoft-playwright-2026-10-10.md`, `clone-distiller/vercel-labs-agent-browser-2026-10-10.md`

Digging ended: round 4 settled nothing and added no sub-area.

## Verification
40 facts across 12 source pages were checked by fetch with quotes, and all were confirmed. One clarification: Browserbase MCP needs your own API key only for a non-default model; the README's wording was corrected to match. Facts from code carry links pinned to a commit and were not fetched again. No fact is NOT ON PAGE or UNCHECKED.

## Open rabbit holes
- Whether Brave's default user-data-dir ignores `--remote-debugging-port`, and whether the Chromium check is gated on Chrome branding — dug, unsettled.
- Claude in Chrome on Brave: docs vs support article — dug, unsettled (DISPUTED).
- Whether the MCP SDK's `RegisteredTool.disable()` hides Chrome DevTools MCP's disabled tools from tools/list, which decides whether it lists ~30 or 64 tools — undug.
- tools/list size in characters for Chrome DevTools MCP and dev-browser — undug. The isagentready 17k-token figure is third-party.
- dev-browser code size and whether it has diff snapshots or size limits — undug.
- Stagehand: is v4 released (GitHub shows 3.7.3 as Latest) — dug, unsettled.
- Steel `STEEL_CONNECT_URL` with an arbitrary CDP endpoint — undug.
- Whether Brave has a `brave://inspect` remote-debugging toggle and a Chrome 144-style consent prompt — dug, unsettled. The 2024 Brave issue says the settings switch was removed.
- The lineage of the brave-devtools-mcp forks (triuzzi vs rwinkelman vs neolivz) — dug, unsettled. npm `brave-mcp` points to triuzzi.
- The security of an open, unauthenticated CDP port on a logged-in profile — undug.
