package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

// claudeRenameConfirmTries and claudeRenameConfirmPoll bound the read-back of
// a live Claude rename. Claude's /rename appends a custom-title record to the
// chat's own transcript — the witness reload --new reads back too — the way
// Codex's thread/name/set lands in the session index RenameCodex reads.
var (
	claudeRenameConfirmTries = 20
	claudeRenameConfirmPoll  = 250 * time.Millisecond
)

// unconfirmedClaudeName reads a Claude chat's transcript until it wears name
// as its custom title. "" is a confirmed rename; anything else is the warning
// naming why the rename is unverified. "Could not look" and "looked, not
// there" read differently, and neither fails the verb: the /rename was
// delivered, and a chat mid-turn may still apply it when its turn ends.
func unconfirmedClaudeName(ctx context.Context, chat headless.Chat, name string) string {
	if chat.Path == "" {
		return "could not verify the Claude rename — the chat's transcript is not known; the chat may be named, check it with pfm ls"
	}
	var readErr error
	for attempt := 0; attempt < claudeRenameConfirmTries; attempt++ {
		if attempt > 0 {
			if err := clock.Real.Sleep(ctx, claudeRenameConfirmPoll); err != nil {
				return fmt.Sprintf(
					"could not verify the Claude rename — stopped waiting for it: %v; check it with pfm ls",
					err,
				)
			}
		}
		title, err := reload.TranscriptTitle(chat.Path)
		readErr = err
		if err == nil && strings.TrimSpace(title) == name {
			return ""
		}
	}
	if readErr != nil {
		return fmt.Sprintf(
			"could not verify the Claude rename — %v; the chat may be named, check it with pfm ls",
			readErr,
		)
	}
	return fmt.Sprintf(
		"the Claude rename was typed but NOT confirmed — no custom-title record naming it reached %s within %s (a chat mid-turn may still apply it when its turn ends); check it with pfm ls",
		chat.Path,
		time.Duration(claudeRenameConfirmTries-1)*claudeRenameConfirmPoll,
	)
}
