package herdr

import (
	"context"
	"fmt"
	"strings"
)

// Running is every running holder herdr-shield binds to a pane of the Herdr
// server at socket.
func (shield *Shield) Running(ctx context.Context, socket string) ([]ShieldHolder, error) {
	holders, _, err := shield.list(ctx, socket)
	if err != nil {
		return nil, err
	}
	running := make([]ShieldHolder, 0, len(holders))
	for _, holder := range holders {
		if holder.running() && holder.Binding.Socket == socket && holder.Binding.Pane != "" {
			running = append(running, holder)
		}
	}
	return running, nil
}

// innermostHolder picks, among holders bound to one pane, the one a
// `herdr-shield attach` moved there from inside another held shell: each
// outer holder's foreground is the attach client of the one inside it, so the
// pane's shell is the one holder whose foreground is no attach client.
func (shield *Shield) innermostHolder(pane string, bound []*ShieldHolder) (*ShieldHolder, error) {
	var innermost *ShieldHolder
	ids := make([]string, 0, len(bound))
	for _, holder := range bound {
		ids = append(ids, holder.ID)
		foreground, err := shield.innerForeground(*holder)
		if err != nil {
			return nil, fmt.Errorf("herdr pane %s: herdr-shield holder %s: %w", pane, holder.ID, err)
		}
		if shieldAttached(foreground) {
			continue
		}
		if innermost != nil {
			return nil, fmt.Errorf("herdr pane %s: herdr-shield binds holders %s and %s, neither attaching the other",
				pane, innermost.ID, holder.ID)
		}
		innermost = holder
	}
	if innermost == nil {
		return nil, fmt.Errorf("herdr pane %s: herdr-shield holders %s all attach another holder",
			pane, strings.Join(ids, ", "))
	}
	return innermost, nil
}
