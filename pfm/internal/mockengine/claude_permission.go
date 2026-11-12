package mockengine

// permissionOptions are the rows of Claude Code's tool permission dialog; the
// first allows the call, the last rejects it as Escape does.
var permissionOptions = []string{"Yes", "No, and tell Claude what to do differently (esc)"}

// claudeToolRejection is the tool_result Claude Code writes for a call its
// human rejected at the permission dialog.
const claudeToolRejection = "The user doesn't want to proceed with this tool use. The tool use was rejected " +
	"(eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait " +
	"for the user to tell you how to proceed."

// claudeToolInterruptText is the user record that follows the rejection; it
// starts with internal/transcript ClaudeInterruptMarker.
const claudeToolInterruptText = "[Request interrupted by user for tool use]"

// permissionAsk is the tool call a permission dialog holds: its step, its
// tool_use record and the script its turn goes on with once allowed.
type permissionAsk struct {
	step    Step
	use     toolUse
	running *script
}

// askPermission plays a permission step's opening: PreToolUse, the tool_use
// record and PermissionRequest. A PreToolUse hook that denies the call ends it
// at once with its reason, as Claude Code shows no dialog then.
func (session *claudeSession) askPermission(step Step, running *script) (string, error) {
	use, err := session.useTool(step)
	if err != nil {
		return "", err
	}
	if _, found := use.denied(); found {
		return session.finishTool(step, use)
	}
	payload := session.payload(hookPermissionRequest)
	payload.ToolName, payload.ToolInput, payload.ToolUseID = step.Tool, use.part.Input, use.part.ID
	if _, err := session.fire(hookPermissionRequest, step.Tool, payload); err != nil {
		return "", err
	}
	session.asking = &permissionAsk{step: step, use: use, running: running}
	return "", nil
}

// answerPermission closes the open dialog. Allowed, the tool runs (PostToolUse,
// its result) and the script its turn goes on with is returned; rejected, the
// rejection tool_result and the interrupt record are written and no hook fires.
func (session *claudeSession) answerPermission(allow bool) (*script, error) {
	ask := session.asking
	session.asking = nil
	if ask == nil {
		return nil, nil
	}
	if allow {
		_, err := session.finishTool(ask.step, ask.use)
		return ask.running, err
	}
	rejection := contentPart{
		Type:      blockToolResult,
		ToolUseID: ask.use.part.ID,
		Content:   claudeToolRejection,
		IsError:   true,
	}
	if err := session.toolResult(rejection); err != nil {
		return nil, err
	}
	record := session.userRecord("", false)
	record.PromptSource = ""
	record.Message.Content = []contentPart{{Type: blockText, Text: claudeToolInterruptText}}
	return nil, session.write(record)
}

// permissionAsker is an engine that plays Claude Code's permission dialog.
type permissionAsker interface {
	askPermission(Step, *script) (string, error)
	answerPermission(bool) (*script, error)
}

// openPermission plays a permission step on the pane: the call's row and the
// dialog over it, or its hook denial; an engine without the dialog refuses
// the step by name.
func (loop *paneLoop) openPermission(running *script, step Step) (int, bool) {
	engine, plays := loop.engine.(permissionAsker)
	if !plays {
		warn(loop.proc.stderr, "%s plays no %s step", loop.proc.engine, StepPermission)
		return ExitUnpinned, true
	}
	denied, err := engine.askPermission(step, running)
	if err != nil {
		warn(loop.proc.stderr, "permission %s: %v", step.Tool, err)
		return ExitUnpinned, true
	}
	screen := loop.screen
	screen.footer = ""
	if denied != "" {
		for _, row := range toolRows(step.Tool, denied) {
			screen.say(row)
		}
		loop.paint()
		return 0, false
	}
	screen.say("⏺ " + step.Tool)
	screen.menu = &Step{Type: StepPermission, Tool: step.Tool, Options: permissionOptions}
	screen.selected = 1
	loop.paint()
	return 0, false
}

// closePermission answers the open permission dialog: Yes goes on with the
// turn, No or Escape ends it interrupted.
func (loop *paneLoop) closePermission(allow bool) (int, bool) {
	screen := loop.screen
	screen.menu = nil
	running, err := loop.engine.(permissionAsker).answerPermission(allow)
	if err != nil {
		warn(loop.proc.stderr, "answer permission: %v", err)
	}
	if !allow || running == nil {
		screen.say("  ⎿  Interrupted · What should Claude do instead?")
		loop.paint()
		return 0, false
	}
	screen.say("  ⎿  done")
	loop.paint()
	return loop.turn(running)
}
