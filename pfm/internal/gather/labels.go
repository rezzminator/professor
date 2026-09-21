package gather

import (
	"context"
	"sync"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
)

// labelCaptureLimit bounds how many capture-pane forks run at once. Every live
// claude pane is captured on every gather pass, and an unbounded fan-out over a
// forty-chat fleet is forty simultaneous forks for a name convergence nobody is
// waiting on.
const labelCaptureLimit = 8

// PaneLabel is one claude pane's 🔖 statusline label, or the record that its
// capture could not be read. Failed is not "no label": a capture that failed
// says nothing about the pane, and a window holding one must keep the name it
// has rather than take a sibling pane's.
type PaneLabel struct {
	Socket   string
	WindowID string
	PaneID   string
	Label    string
	Failed   bool
}

// CaptureClaudeLabels reads the 🔖 label off every live claude pane.
//
// A pane is skipped outright when it cannot carry a chat's label: a squatter
// (its session name is not the socket name — session name IS the chat
// discriminator everywhere in the fleet), or a viewport (a pane running tmux
// against another chat's socket, whose screen is the INNER chat's statusline).
func CaptureClaudeLabels(
	ctx context.Context,
	capturer PaneCapturer,
	panes []ProbePane, configured ...[]string,
) []PaneLabel {
	if capturer == nil {
		return nil
	}
	candidates := make([]ProbePane, 0, len(panes))
	for index := range panes {
		pane := panes[index]
		if id, ok := pfmengine.FromSocket(pane.Socket); !ok || id != pfmengine.Claude {
			continue
		}
		if pane.SessionName != pane.Socket || pane.WindowID == "" {
			continue
		}
		if pane.CurrentCommand == "tmux" {
			continue
		}
		candidates = append(candidates, pane)
	}
	if len(candidates) == 0 {
		return nil
	}

	labels := make([]PaneLabel, len(candidates))
	var waitGroup sync.WaitGroup
	slots := make(chan struct{}, labelCaptureLimit)
	for index := range candidates {
		pane := candidates[index]
		index := index
		waitGroup.Add(1)
		slots <- struct{}{}
		go func() {
			defer waitGroup.Done()
			defer func() { <-slots }()
			result := PaneLabel{
				Socket:   pane.Socket,
				WindowID: pane.WindowID,
				PaneID:   pane.PaneID,
			}
			capture, err := capturer.CapturePane(ctx, pane.Socket, pane.PaneID)
			if err != nil {
				result.Failed = true
			} else {
				var emojis []string
				if len(configured) != 0 {
					emojis = configured[0]
				}
				result.Label = naming.BookmarkLabelFor(capture, emojis)
			}
			labels[index] = result
		}()
	}
	waitGroup.Wait()
	return labels
}
