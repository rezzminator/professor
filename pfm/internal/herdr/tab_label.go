package herdr

import (
	"context"
	"fmt"
)

// LabelSoleTab gives tab the label when one pane sits in it alone: a tab
// shared with other panes keeps its label, one already carrying the label is
// left alone, and an empty tab (a pane Herdr lists in no tab) labels nothing.
// Herdr's tab bar shows the tab's label, never a pane's title, so this is the
// one rule every chat-name writer labels a tab by. seq numbers each request.
func LabelSoleTab(ctx context.Context, socket, tab, label string, seq func() uint64) error {
	if tab == "" {
		return nil
	}
	get, err := TabGet{Tab: tab, Seq: seq()}.Line()
	if err != nil {
		return err
	}
	results, err := Call(ctx, socket, get)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	info, err := ParseTab(results[0])
	if err != nil {
		return err
	}
	if info.PaneCount != 1 || info.Label == TabLabel(label) {
		return nil
	}
	rename, err := TabRename{Tab: tab, Label: label, Seq: seq()}.Line()
	if err != nil {
		return err
	}
	if err := Send(ctx, socket, rename); err != nil {
		return fmt.Errorf("relabel %q: %w", label, err)
	}
	return nil
}
