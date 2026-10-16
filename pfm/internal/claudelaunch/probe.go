package claudelaunch

import (
	"strings"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// ProbeEnv isolates the doctor's harness-prompt capture from the live chat.
// Nonessential traffic is switched off so remote feature flags keep their
// shipped defaults: a fresh config dir fetches them anew each run, and the
// fetched values switch prompt variants between otherwise identical captures.
func ProbeEnv(environ []string, sinkURL, configDir string) []string {
	stripped := make(map[string]bool, len(hygiene))
	for _, name := range hygiene {
		stripped[name] = true
	}
	stripped["CLAUDE_CODE_USE_BEDROCK"] = true
	stripped["CLAUDE_CODE_USE_VERTEX"] = true
	// The capture runs no turn: it is never the chat bound to the parent's Herdr pane.
	for _, name := range herdr.Env() {
		stripped[name] = true
	}
	result := make([]string, 0, len(environ)+6)
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if !stripped[name] && !strings.HasPrefix(name, "ANTHROPIC_BEDROCK_") &&
			!strings.HasPrefix(name, "ANTHROPIC_VERTEX_") {
			result = append(result, entry)
		}
	}
	return append(result,
		"ANTHROPIC_BASE_URL="+sinkURL,
		"ANTHROPIC_API_KEY=pfm-doctor-sink",
		"ANTHROPIC_AUTH_TOKEN=pfm-doctor-sink",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=0",
		"FORCE_PROMPT_CACHING_5M=1",
		envDisableNonessentialTraffic+"=1",
		configDirEnv+"="+configDir,
	)
}
