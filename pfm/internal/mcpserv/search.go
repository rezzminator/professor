package mcpserv

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/chat"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// find is chat_find: chat.Find under the tool's candidate limit (default 10,
// maximum 50), projected onto the wire candidate. Only the stdio server knows
// its caller ambiently (its process runs inside the asking chat), so only it
// leaves the asking session out; SelfID names what was left out, so an answer
// is never mistaken for one that looked everywhere. A search that matched
// nothing is an answer (count 0), not a tool failure.
func (current *backend) find(ctx context.Context, input FindInput) (FindOutput, error) {
	if current.chat == nil {
		return FindOutput{}, fmt.Errorf("chat_find verb is not configured")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 50 {
		return FindOutput{}, fmt.Errorf("limit must be between 1 and 50")
	}
	self := ""
	if !input.IncludeSelf && current.allowAmbientIdentity {
		self = chat.AskingSession()
	}
	matches, err := current.chat.Find(ctx, chat.FindRequest{Excerpt: input.Excerpt, Self: self})
	if err != nil && !errors.Is(err, chat.ErrNoExcerptMatch) {
		return FindOutput{}, fmt.Errorf("chat_find: %w", err)
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	candidates := make([]FindCandidate, 0, len(matches))
	for _, match := range matches {
		candidates = append(candidates, FindCandidate{
			ID: match.ID, Path: match.Path, Engine: string(pfmengine.Claude),
			Date: match.Last, Hits: match.Hits, Confirmed: true,
		})
	}
	output := FindOutput{
		Candidates: candidates, Count: len(candidates),
		Needles: chat.ExcerptNeedles(input.Excerpt), SelfID: self,
	}
	return output, nil
}
