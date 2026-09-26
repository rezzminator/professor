package hookentry

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type orchestratorWaitHookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

const orchestratorWaitReason = "Waiting needs no command. End your message with one line and no tool call: while an agent you spawned is still running you do not return, and its return wakes you."

var (
	orchestratorWaitEchoPrintf = regexp.MustCompile(`^(echo|printf)(\s.*)?$`)
	orchestratorWaitForbidden  = regexp.MustCompile("[><|;&$`\n]")
	orchestratorWaitSleep      = regexp.MustCompile(`^sleep\s+\d+(\.\d+)?[smh]?$`)
)

// OrchestratorWait is the fail-open PreToolUse hook. It denies a Bash call
// whose whole command only waits (a no-op: echo/printf with plain literal
// arguments, "true", ":", or "sleep N"), so an orchestrator agent ends its
// message instead of burning a call on a command that changes nothing.
func OrchestratorWait(input io.Reader, stdout, stderr io.Writer) int {
	raw, err := io.ReadAll(input)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal orchestrator-wait: read hook payload (fail-open): %v\n", err)
		return 0
	}
	if len(raw) == 0 {
		return 0
	}
	var hook orchestratorWaitHookInput
	if err := json.Unmarshal(raw, &hook); err != nil {
		fmt.Fprintf(stderr, "pfm internal orchestrator-wait: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	if hook.ToolName != "Bash" || !isNoOpWaitCommand(hook.ToolInput.Command) {
		return 0
	}
	if err := json.NewEncoder(stdout).Encode(preToolUseDenyResponse(orchestratorWaitReason)); err != nil {
		return 1
	}
	return 0
}

// isNoOpWaitCommand reports whether the whole trimmed command is one simple
// command that only waits: echo/printf with plain literal arguments (no
// redirection, pipe, command separator, substitution, backtick or newline
// anywhere in the command), "true", ":", or "sleep N" (digits with an
// optional decimal and an optional s/m/h suffix). Anything else — including
// a second command chained on, a redirect, or a loop around a sleep — is not
// a no-op and is left alone.
func isNoOpWaitCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	switch trimmed {
	case "true", ":":
		return true
	}
	if orchestratorWaitSleep.MatchString(trimmed) {
		return true
	}
	return orchestratorWaitEchoPrintf.MatchString(trimmed) && !orchestratorWaitForbidden.MatchString(trimmed)
}
