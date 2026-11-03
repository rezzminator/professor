package mockengine

import (
	"fmt"
)

func (session *claudeSession) headlessStep(step Step) error {
	switch step.Type {
	case StepToolCall:
		_, err := session.tool(step)
		return err
	case StepBackgroundHandoff:
		return session.backgroundHandoff(step)
	case StepBackgroundAgent:
		_, err := session.background(step)
		return err
	case StepCompact:
		return session.compact(step)
	case StepMCP:
		return session.mcp(step)
	case StepNotification:
		return session.notification(step)
	case StepHook:
		return session.hook(step)
	case StepPermission, StepQuestion:
		if err := session.requestAttention(step); err != nil {
			return err
		}
		approved, decided := session.attentionDecision()
		if !decided {
			return fmt.Errorf("headless %s requires an explicit hook decision", step.Type)
		}
		if !approved {
			return fmt.Errorf("headless %s denied", step.Type)
		}
		return session.answerAttention(step, true)
	case StepHold:
		return waitHold(session.proc.ctx, step.UntilGone)
	default:
		return fmt.Errorf("unscripted headless step %s", step.Type)
	}
}
