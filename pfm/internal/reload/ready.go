package reload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// ready waits until the native chat in the pane is idle with an empty
// composer: before /exit (stash parks a draft), and once the reborn chat runs
// (reborn) before its --then turn.
func (n *nativeReload) ready(ctx context.Context, stash, reborn bool) error {
	stable := 0
	pane := n.pane()
	// agentUnread is the last Herdr agent read that told nothing: it waits
	// without reading the screen and names itself if the tries run out.
	var agentUnread error
	for attempt := 0; attempt < n.options.IdleTries; attempt++ {
		screen, err := pane.capture(ctx)
		if err != nil {
			return err
		}
		if err := refuseSelector(ctx, pane, screen); err != nil {
			return fmt.Errorf("native reload: %w", err)
		}
		if n.request.Engine == pfmengine.Claude {
			body, err := herdr.SeatRequest(
				ctx,
				n.target.SocketPath,
				"agent.get",
				map[string]string{"target": n.target.Pane},
			)
			var refusal *herdr.Refusal
			if reborn && errors.As(err, &refusal) && refusal.Code == herdr.AgentNotFound {
				// The reborn Claude runs before Herdr registers it: --then waits,
				// where a refusal before /exit means the target is gone.
				agentUnread = fmt.Errorf("native reload read agent: %w", err)
				stable = 0
				if err := n.options.Clock.Sleep(ctx, n.options.Poll); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return fmt.Errorf("native reload read agent: %w", err)
			}
			var state struct {
				Type  string `json:"type"`
				Agent *struct {
					Status string `json:"agent_status"`
				} `json:"agent"`
			}
			agentUnread = nil
			if err := json.Unmarshal(body, &state); err != nil {
				agentUnread = fmt.Errorf("decode native reload agent: %w", err)
			} else if state.Type != "agent_info" || state.Agent == nil {
				agentUnread = errors.New("native reload malformed agent_info")
			}
			// Herdr vetoes only a chat waiting on its user; one it has not
			// classified yet waits; idleness is the screen's call, as on the tmux path.
			// Claude stays working while background shells or sub-agents run
			// behind an idle composer, and only the footer shows a live turn.
			// An unread or unrecognized state polls without reading the screen.
			settled := false
			if agentUnread == nil {
				switch status := state.Agent.Status; status {
				case "blocked":
					return fmt.Errorf("native reload target is %s", status)
				case "unknown":
					// Herdr holds a Claude it has just found in "unknown" through its
					// startup grace (3 s); a reload typed then waits it out.
					agentUnread = errors.New("native reload target is unknown")
				case "idle", "done", "working":
					settled = true
				default:
					agentUnread = fmt.Errorf("native reload unknown agent state %q", status)
				}
			}
			if !settled {
				stable = 0
				if err := n.options.Clock.Sleep(ctx, n.options.Poll); err != nil {
					return err
				}
				continue
			}
		}
		if !composerDrawn(screen) || pfmcall.IsFooterBusy(n.request.Engine, screen) {
			stable = 0
		} else {
			empty, draft, err := reloadComposerEmpty(screen, pane.styled(ctx))
			if err != nil {
				return err
			}
			if !empty {
				if !stash {
					return fmt.Errorf("native reload: %w", composerDraftError(draft))
				}
				if err := pane.key(ctx, "C-s"); err != nil {
					return fmt.Errorf("stash native reload draft: %w", err)
				}
				stash = false
				stable = 0
			} else {
				stable++
				if stable >= 2 {
					return nil
				}
			}
		}
		if err := n.options.Clock.Sleep(ctx, n.options.Poll); err != nil {
			return err
		}
	}
	notSent := "/exit was not typed"
	if reborn {
		notSent = "the --then message was not sent"
	}
	if agentUnread != nil {
		return fmt.Errorf("native reload: Herdr never reported the chat's state (%w); %s", agentUnread, notSent)
	}
	return fmt.Errorf("native reload: chat still busy or input box not ready; %s", notSent)
}
