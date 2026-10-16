# RR — How people make Claude Code speak its output aloud (TTS), ranked

Question: how to make the ClaudeCode talk, in voice, find people's best approaches

## Answer

The approach people most often use is a `Stop` hook. It reads the reply from the `last_assistant_message` field that [Stop hooks receive](https://code.claude.com/docs/en/hooks), and the simplest version is one line piping that text into macOS `say` with `"async": true` [(sodiumsun)](https://dev.to/sodiumsun/make-claude-code-talk-back-with-a-text-to-speech-stop-hook-5cc3). The better-sounding variants summarise the reply first: [say-summary](https://raw.githubusercontent.com/team-attention/plugins-for-claude-natives/main/plugins/say-summary/README.md) has Claude Haiku condense it to 3-10 words, and [claude-spoken-briefs](https://github.com/pcamarajr/claude-spoken-briefs) voices a Haiku brief locally with Kokoro-82M. For two-way voice, [VoiceMode](https://github.com/mbailey/voicemode) is an MCP server built on local Whisper.cpp and Kokoro. Anthropic's own [`/voice`](https://code.claude.com/docs/en/voice-dictation) handles spoken prompts only; no Anthropic page found describes Claude Code speaking replies aloud.

## Map

### 1. Hook wiring (official mechanics)
- Stop hooks receive, in addition to the common input fields, "`stop_hook_active`, `last_assistant_message`, `background_tasks`, and `session_crons`". The docs add: "The `last_assistant_message` field contains the text content of Claude's final response, so hooks can access it without parsing the transcript file." [hooks reference](https://code.claude.com/docs/en/hooks)
- Do not read the reply from the transcript: `transcript_path` "may not yet include the current turn's most recent messages when a hook fires". [hooks reference](https://code.claude.com/docs/en/hooks). Older transcript-based hooks hit this race: brtkwr needed "A `sleep 1` before the `jq` read" to stop hearing the previous turn. [brtkwr](https://brtkwr.com/posts/2026-04-17-making-claude-code-speak-responses-with-a-stop-hook/)
- `async` on a command hook: "If `true`, runs in the background without blocking." [hooks reference](https://code.claude.com/docs/en/hooks). The claim that under `claude -p` async hooks still running at teardown get killed is UNCHECKED (see Verification). melderan separately reports that headless runs stay silent. [melderan/claude-code-tts](https://github.com/melderan/claude-code-tts)
- Notification hooks receive "`message` with the notification text, an optional `title`, and `notification_type`". [hooks reference](https://code.claude.com/docs/en/hooks). The `idle_prompt` type fires only after about 60 s of inactivity, so Notification suits "needs your input" alerts, not per-reply speech.
- On API-error turns `Stop` doesn't fire: "Claude Code fires `StopFailure` instead, so add the same command there." [sodiumsun](https://dev.to/sodiumsun/make-claude-code-talk-back-with-a-text-to-speech-stop-hook-5cc3)
- `MessageDisplay` fires while assistant text streams, with `delta`/`final` fields, and is display-only. [hooks reference](https://code.claude.com/docs/en/hooks). In principle it could drive sentence-by-sentence speech, but no source does this (open rabbit hole).

### 2. Approaches by mechanism

| # | Approach | Wiring | Engine | Setup | Latency / cost | Maturity (GitHub API) | Pitfalls |
|---|---|---|---|---|---|---|---|
| A | Raw `say` one-liner ([sodiumsun](https://dev.to/sodiumsun/make-claude-code-talk-back-with-a-text-to-speech-stop-hook-5cc3), [brtkwr](https://brtkwr.com/posts/2026-04-17-making-claude-code-speak-responses-with-a-stop-hook/)) | `Stop` (+`StopFailure`), `async: true` | macOS `say` | Minutes; nothing to install on a Mac | `say` is "instant" but "sounds robotic — flat prosody, no natural pacing" [(mandalivia)](https://www.mandalivia.com/obsidian/adding-voice-mode-to-claude-code-with-a-stop-hook/); free | Blog recipes | Reads markdown, paths and code literally ([heard.dev](https://heard.dev/compare/claude-code-text-to-speech), vendor); `pkill -x say` kills other sessions' speech [(brtkwr)](https://brtkwr.com/posts/2026-04-17-making-claude-code-speak-responses-with-a-stop-hook/) |
| B | Haiku-condensed headline: [say-summary](https://raw.githubusercontent.com/team-attention/plugins-for-claude-natives/main/plugins/say-summary/README.md) | `Stop` plugin hook, `python3 ${CLAUDE_PLUGIN_ROOT}/scripts/say-summary.py`, timeout 30 | Haiku summary, macOS `say` at "190 words per minute" | `/plugin marketplace add team-attention/plugins-for-claude-natives`, `/plugin install say-summary`, then a setup script | One extra Haiku call per reply | Marketplace repo: 826 stars, pushed 2026-04-20 [(API)](https://api.github.com/repos/team-attention/plugins-for-claude-natives) | Summarises only when the reply is "longer than 10 words"; needs `claude-agent-sdk` |
| C | Haiku brief + local neural voice: [claude-spoken-briefs](https://github.com/pcamarajr/claude-spoken-briefs), [claude-speak](https://github.com/kyleoliveiro/claude-speak) | `Stop`; claude-speak hands off to a detached process and "returns in about 50 ms" | `claude -p` Haiku summary, Kokoro-82M local | `uv`; first model download ~335 MB (claude-speak) | Summary "takes about 3 seconds"; replies under ~280 chars are read in full (claude-speak); no TTS bill | 1 star each; pushed 2026-10-05 and 2026-09-27 [(API)](https://api.github.com/repos/pcamarajr/claude-spoken-briefs) | Extra model call every reply; claude-spoken-briefs speaks short plain replies verbatim |
| D | Claude-written tag line: [cris-m/claude_voice](https://github.com/cris-m/claude_voice), [abandisch/claude-code-voice](https://github.com/abandisch/claude-code-voice) | Instruction makes Claude append a summary line (`TTS_SUMMARY` / `🔊`); `Stop` extracts and speaks it | Kokoro (cris-m default; abandisch in Docker, falls back to `say`) | Medium; Docker build 5-10 min (abandisch) | No extra model call | 4 and 0 stars | Depends on Claude following an instruction; the output-styles doc says a style "doesn't guarantee that something always happens" [(output styles)](https://code.claude.com/docs/en/output-styles) |
| E | Strip-and-read daemon: [melderan/claude-code-tts](https://github.com/melderan/claude-code-tts) | `Stop` + optional `PostToolUse` narration | Piper | Medium | Queued daemon | 2 stars, pushed 2026-10-15 [(API)](https://api.github.com/repos/melderan/claude-code-tts) | Strips fenced and inline code and verbalises paths; headless `-p` runs stay silent |
| F | LLM "done" announcement: [disler/claude-code-hooks-mastery](https://raw.githubusercontent.com/disler/claude-code-hooks-mastery/main/.claude/hooks/stop.py) | `Stop` / `SubagentStop` / `Notification`; speaks only `if args.notify:` | "Priority order: ElevenLabs > OpenAI > pyttsx3", picked by which API key is set | Clone the hooks setup | Cloud cost if keys are set | 3,942 stars, pushed 2026-03-04 [(API)](https://api.github.com/repos/disler/claude-code-hooks-mastery) | Speaks a generated completion message (`get_llm_completion_message()`), not the reply's content |
| G | Two-way MCP: [VoiceMode](https://github.com/mbailey/voicemode) | MCP `converse` tool the agent calls; `claude mcp add --scope user voicemode -- uvx --refresh --from voice-mode voicemode-mcp-launcher` | "Whisper.cpp - Local speech-to-text", "Kokoro - Local text-to-speech"; OpenAI key optional as fallback | Medium | Local and free by default | 1,402 stars, pushed 2026-10-06, v8.12.0 on 2026-07-21 [(API)](https://api.github.com/repos/mbailey/voicemode) | Speaks when the agent calls the tool, not automatically |
| H | Speak-tool MCP: [blacktop/mcp-tts](https://github.com/blacktop/mcp-tts), [ybouhjira/claude-code-tts](https://github.com/ybouhjira/claude-code-tts) | MCP tool; ybouhjira also ships a Stop hook that "speaks the first sentence" | mcp-tts "Uses the macOS `say` binary"; ybouhjira uses OpenAI TTS | `go install` / plugin | OpenAI bills per character | 74 stars, pushed 2026-10-14; 21 stars, pushed 2026-01-25 | ybouhjira's page contradicts itself on first sentence vs whole reply (DISPUTED within the page) |
| I | Others opened (stars only): [husniadil/cc-hooks](https://github.com/husniadil/cc-hooks) (gTTS/ElevenLabs, 19), [aperepel/claude-mlx-tts](https://github.com/aperepel/claude-mlx-tts) (Chatterbox on MLX, "10-15 word spoken update", 11), [alphabet/speak](https://github.com/alphabet/speak) (`say`/espeak/SAPI plus Notification, 2), [praneybehl/claude-code-voice-hook](https://github.com/praneybehl/claude-code-voice-hook) (OpenAI-compatible local server, 1,500-char cap, 1) | | | | | | alphabet/speak's Kokoro option reloads the model on every hook call |

Adoption signal: GitHub stars are the only measure available. No usage data was found, and Reddit returned no threads.

### 3. TTS engines
- **macOS `say`:** built in and instant, but sounds robotic [(mandalivia)](https://www.mandalivia.com/obsidian/adding-voice-mode-to-claude-code-with-a-stop-hook/). Downloaded Enhanced/Premium voices work, e.g. `say -v "Samantha (Enhanced)"` [(arach.io)](https://arach.io/til/macos-premium-voices/). Some voices "are not installed by default" [(ss64)](https://ss64.com/mac/say.html).
- **Kokoro-82M (local):** about 0.16× real-time factor on an M3 Pro CPU [(VisionStory)](https://www.visionstory.ai/open-source/kokoro-tts). A per-call cold start "takes 2-3 seconds — every single time" unless a resident server keeps the model loaded [(mandalivia)](https://www.mandalivia.com/obsidian/adding-voice-mode-to-claude-code-with-a-stop-hook/); claude-speak keeps "Kokoro loaded" on a Unix socket [(claude-speak)](https://github.com/kyleoliveiro/claude-speak).
- **Piper (local):** rhasspy/piper "was archived by the owner on Oct 6, 2025" and development moved to OHF-Voice/piper1-gpl (GPL-3.0) [(rhasspy/piper)](https://github.com/rhasspy/piper). macOS support isn't stated on the official pages.
- **OpenAI TTS (cloud):** tts-1 costs $15.00 per 1M characters [(OpenAI pricing)](https://developers.openai.com/api/docs/pricing). For gpt-4o-mini-tts one user reports "about 250ms time-to-first-byte" and "~$0.015 per 1K characters", and settled on it for a voice hook [(mandalivia)](https://www.mandalivia.com/obsidian/adding-voice-mode-to-claude-code-with-a-stop-hook/).
- **ElevenLabs (cloud):** "$0.04 (Flash/Turbo)" per 1,000 characters, with "Ultra-low latency (~75ms)" [(ElevenLabs pricing)](https://elevenlabs.io/pricing/api). The official ElevenLabs MCP repo was archived on Aug 20, 2026 and saves audio to files rather than playing it [(elevenlabs-mcp)](https://github.com/elevenlabs/elevenlabs-mcp).
- **edge-tts (cloud):** free, and uses "Microsoft Edge's online text-to-speech service" [(edge-tts)](https://github.com/rany2/edge-tts). Terms-of-service risk is unverified.
- **Cartesia Sonic:** described by the vendor as its "fastest" model [(Cartesia docs)](https://docs.cartesia.ai/build-with-cartesia/models). A 40 ms time-to-first-audio figure comes only from a third-party review [(texttolab)](https://texttolab.com/blog/cartesia-ai-review) and is unverified.
- **pyttsx3:** offline fallback; "like a robot from 1995" [(stacktoheap)](https://stacktoheap.com/blog/2025/08/03/having-fun-with-claude-code-hooks/).

### 4. Making text speakable
- **Small-model condense:** Haiku through `claude -p` with "no tools, no MCP servers, no hooks and no session persistence" [(claude-spoken-briefs)](https://github.com/pcamarajr/claude-spoken-briefs); 3-10 words in [say-summary](https://raw.githubusercontent.com/team-attention/plugins-for-claude-natives/main/plugins/say-summary/README.md). Cost is about 3 s per reply [(claude-speak)](https://github.com/kyleoliveiro/claude-speak). None of the pages shows its summary prompt.
- **Claude-written tag line:** cris-m "Extracts and speaks TTS_SUMMARY from responses" and re-injects the instruction through an InstructionsLoaded hook so it "survives context compression" [(cris-m)](https://github.com/cris-m/claude_voice).
- **Regex strip and truncate:** "A short `sed` pass keeps the visible text and drops the link target, backticks, bold/italic markers, and bare URLs", plus awk to take the last non-empty line [(brtkwr)](https://brtkwr.com/posts/2026-04-17-making-claude-code-speak-responses-with-a-stop-hook/); `head -c 600` [(sodiumsun)](https://dev.to/sodiumsun/make-claude-code-talk-back-with-a-text-to-speech-stop-hook-5cc3). melderan strips code blocks and verbalises file paths [(melderan)](https://github.com/melderan/claude-code-tts).
- **Overlap control:** a per-session PID file: "I stash the `say` PID in `/tmp/speak-response-<id>.pid` and kill that PID on the next turn, leaving other sessions alone" [(brtkwr)](https://brtkwr.com/posts/2026-04-17-making-claude-code-speak-responses-with-a-stop-hook/). Alternatives are a system-wide lock [(ktaletsk PR #4)](https://github.com/ktaletsk/claude-code-tts/pull/4) or a queued daemon [(melderan)](https://github.com/melderan/claude-code-tts).

### 5. Two-way voice (secondary)
- **Official:** "Speak your prompts instead of typing them in the Claude Code CLI." `/voice` turns it on with hold-to-record or tap-to-record. It needs a Claude.ai account, and "Voice dictation streams your recorded audio to Anthropic's servers for transcription. Audio is not processed locally." [(voice dictation docs)](https://code.claude.com/docs/en/voice-dictation). The page doesn't mention spoken output.
- **VoiceMode:** full spoken loop over MCP, local Whisper.cpp and Kokoro [(VoiceMode)](https://github.com/mbailey/voicemode).
- **Third-party dictation:** [Superwhisper](https://superwhisper.com/claude-code) (macOS), [keybind-whisper](https://github.com/samhaaf/keybind-whisper) ("Offline push-to-talk dictation for macOS", whisper.cpp), and Apple Dictation [(Apple)](https://support.apple.com/guide/mac-help/use-dictation-mh40584/mac).

### 6. Community signal
- Blogs converge on a Stop hook with `say` and async ([sodiumsun](https://dev.to/sodiumsun/make-claude-code-talk-back-with-a-text-to-speech-stop-hook-5cc3), [brtkwr](https://brtkwr.com/posts/2026-04-17-making-claude-code-speak-responses-with-a-stop-hook/), [souliane](https://dev.to/souliane/text-to-speech-for-claude-code-hear-what-the-agent-is-doing-3mom), [yetmike](https://yetmike.com/blog/claude-code-audio-notifications)). Writers who upgrade move to local Kokoro or cloud OpenAI [(mandalivia)](https://www.mandalivia.com/obsidian/adding-voice-mode-to-claude-code-with-a-stop-hook/).
- yetmike warns that without async "the hook blocks Claude from showing the response until the sound finishes playing" [(yetmike)](https://yetmike.com/blog/claude-code-audio-notifications).
- On HN, a canned-sound hook thread (Peon) got "Once the novelty wore off, I found it more useful to hear per-project, event-specific messages" and complaints about `curl | bash` installs [(HN 46985151)](https://news.ycombinator.com/item?id=46985151).

## Ranked top 3

1. **A Stop hook on `last_assistant_message` piped to `say`, async, with stripping and a cap (approach A).** It is the most-cited pattern in the community, takes zero installs on a Mac, starts speaking instantly, costs nothing, and uses the field the docs recommend for this purpose. Its weaknesses are the robotic voice and markdown read literally. Fix both with sed stripping, an Enhanced voice, and a per-session PID kill.
2. **Stop hook plus a Haiku condense (approach B, say-summary, or C with Kokoro).** This is the best listening experience: a short spoken headline instead of a wall of text. say-summary sits in a 826-star marketplace and installs with two commands. The cost is about 3 s and an extra model call per reply.
3. **VoiceMode MCP.** It is the most-starred, actively maintained voice project (1,402 stars, pushed 2026-10-06), runs locally on a Mac, and gives a full spoken conversation loop. It ranks below the hooks for speaking output because the agent decides when to call `converse`; speech isn't automatic on every reply.

### Minimal recipe for the top pick (as found in sources)

The hook entry for `~/.claude/settings.json`. The command and `async` field are from [sodiumsun](https://dev.to/sodiumsun/make-claude-code-talk-back-with-a-text-to-speech-stop-hook-5cc3), which also says to add the same command under `StopFailure`:

```json
{
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command",
                     "command": "jq -r '.last_assistant_message // empty' | head -c 600 | say",
                     "async": true } ] }
    ]
  }
}
```

The upgrade path, as a script in the style of [brtkwr](https://brtkwr.com/posts/2026-04-17-making-claude-code-speak-responses-with-a-stop-hook/):
1. Read `.last_assistant_message` (and `.session_id`) from stdin with jq.
2. Strip markdown with sed (link targets, backticks, bold/italic markers, bare URLs).
3. Kill the PID stored in `/tmp/speak-response-<id>.pid`.
4. Run `say` in the background and write its PID to that file.

The surrounding JSON nesting (`hooks` → `Stop` → `hooks` array) follows the hooks reference's documented shape. The page quotes only the command line verbatim.

## Coverage
- Hook wiring: settled
- Repo catalog & maturity: settled (dates are GitHub `pushed_at`, not commit dates)
- TTS engines: settled (some latency figures come from single third-party posts)
- Speakable text: settled
- MCP & two-way voice: settled
- Community practice: partial. HN and blogs are covered; Reddit returned nothing and no IndyDevDan post was found.

Digging ended: every sub-area settled after round 2. The community line was met on HN and blogs; Reddit is a named gap.

## Verification
22 facts checked across 10 source pages. 20 were confirmed by quote, and two hooks-reference facts went unchecked:
- UNCHECKED: "async: true runs the hook in the background while Claude continues working". The 200000+ chunk was saved to a file I couldn't read. The table-row wording "runs in the background without blocking" was quoted in the opening fetch.
- UNCHECKED: "in `-p` mode Claude Code kills async hooks still running at teardown" (same fetch failure).
- NOT ON PAGE: none.

## Open rabbit holes
- `MessageDisplay` streaming TTS with sentence buffering: undug. No source does it.
- The summary prompts used by say-summary, claude-spoken-briefs and claude-speak: dug, unsettled (not shown on their pages).
- Reddit r/ClaudeAI and r/ClaudeCode practice: dug, unsettled (nothing found).
- IndyDevDan videos on TTS hooks: dug, unsettled (nothing found).
- Unopened repos: nickpending/clarvis, jvosloo/claude-voice, ra-yavuz/claude-can-speak, guc-net/simple-tts, antonyjaen/claude-speak, shreyas-s-rao/claude-code-narrator, EljieGoc-ong/claude-voice: undug.
- SubagentStop speech and overlap from parallel subagents: undug.
- A desktop-app read-aloud button (anthropics/claude-code issue 58429): undug, snippet only.
- Kokoro-FastAPI memory leak under sustained use: undug, snippet only.
- gpt-4o-mini-tts deprecation status and per-minute cost: undug.
- Whether edge-tts violates Microsoft's terms of service, and how stable it is: undug.
- Piper on Apple Silicon: undug.
- Rules for code-heavy replies (skip vs summarise): dug, unsettled.
