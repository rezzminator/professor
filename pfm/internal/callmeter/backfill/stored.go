package backfill

// The store lookups one transcript's write runs before its transaction: which
// calls are new or gain a column, and which still carry the hook's
// provisional request key.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// callColumns are the calls columns backfill can provide, each with whether a
// Call provides it: a stored row is filled when one of them is NULL there.
var callColumns = []struct {
	name  string
	given func(*callmeter.Call) bool
}{
	{"session_id", func(c *callmeter.Call) bool { return c.SessionID != nil }},
	{"agent_id", func(c *callmeter.Call) bool { return c.AgentID != nil }},
	{"agent_type", func(c *callmeter.Call) bool { return c.AgentType != nil }},
	{"request_id", func(c *callmeter.Call) bool { return c.RequestID != nil }},
	{"ts", func(c *callmeter.Call) bool { return c.TS != nil }},
	{"tool", func(c *callmeter.Call) bool { return c.Tool != nil }},
	{"input", func(c *callmeter.Call) bool { return c.Input != nil }},
	{"cwd", func(c *callmeter.Call) bool { return c.Cwd != nil }},
	{"failed", func(c *callmeter.Call) bool { return c.Failed != nil }},
	{"error", func(c *callmeter.Call) bool { return c.Error != nil }},
	{"bytes_real", func(c *callmeter.Call) bool { return c.BytesReal != nil }},
	{"bytes_delivered", func(c *callmeter.Call) bool { return c.BytesDelivered != nil }},
	{"persisted_path", func(c *callmeter.Call) bool { return c.PersistedPath != nil }},
	{"file_path", func(c *callmeter.Call) bool { return c.FilePath != nil }},
	{"file_bytes_before", func(c *callmeter.Call) bool { return c.FileBytesBefore != nil }},
	{"read_start", func(c *callmeter.Call) bool { return c.ReadStart != nil }},
	{"read_lines", func(c *callmeter.Call) bool { return c.ReadLines != nil }},
	{"read_total_lines", func(c *callmeter.Call) bool { return c.ReadTotalLines != nil }},
	{"source", func(c *callmeter.Call) bool { return c.Source != nil }},
	{"config_dir", func(c *callmeter.Call) bool { return c.ConfigDir != nil }},
}

// pendingKeys maps each provisional request key a stored call carries to the
// message id this transcript gives that call, in first-seen order.
type pendingKeys struct {
	message map[string]string
	order   []string
}

// storedPending finds the calls whose stored request_id is still the hook's
// provisional pending:{tool_use_id} key while the transcript names their
// message (docs/design/hooks/callmeter.md § Backfill).
func storedPending(ctx context.Context, store *callmeter.Store, calls []*callmeter.Call) (pendingKeys, error) {
	keys := pendingKeys{message: map[string]string{}}
	var named []*callmeter.Call
	for _, c := range calls {
		if c.RequestID != nil {
			named = append(named, c)
		}
	}
	for start := 0; start < len(named); start += classifyChunk {
		chunk := named[start:min(start+classifyChunk, len(named))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, callmeter.PendingPrefix+"%")
		marks := make([]string, len(chunk))
		messages := map[string]string{}
		for i, c := range chunk {
			args = append(args, c.ToolUseID)
			marks[i] = "?"
			messages[c.ToolUseID] = *c.RequestID
		}
		query := "SELECT tool_use_id, request_id FROM calls WHERE request_id LIKE ? AND tool_use_id IN (" +
			strings.Join(marks, ", ") + ")"
		found, err := store.DB().QueryContext(ctx, query, args...)
		if err != nil {
			return keys, fmt.Errorf("look up pending calls: %w", err)
		}
		for found.Next() {
			var id, provisional string
			if err := found.Scan(&id, &provisional); err != nil {
				return keys, errors.Join(fmt.Errorf("scan pending call: %w", err), found.Close())
			}
			if _, seen := keys.message[provisional]; !seen {
				keys.order = append(keys.order, provisional)
				keys.message[provisional] = messages[id]
			}
		}
		if err := errors.Join(found.Err(), found.Close()); err != nil {
			return keys, fmt.Errorf("read pending calls: %w", err)
		}
	}
	return keys, nil
}

// classifyChunk bounds the ids of one lookup query.
const classifyChunk = 400

// countCallChanges counts, before calls are upserted with FillEmpty, how many are new
// rows and how many existing rows will gain a column they hold as NULL.
func countCallChanges(
	ctx context.Context,
	store *callmeter.Store,
	calls []*callmeter.Call,
) (inserted, filled int, err error) {
	nulls := map[string][]bool{}
	selects := make([]string, len(callColumns))
	for i, col := range callColumns {
		selects[i] = col.name + " IS NULL"
	}
	for start := 0; start < len(calls); start += classifyChunk {
		chunk := calls[start:min(start+classifyChunk, len(calls))]
		args := make([]any, len(chunk))
		marks := make([]string, len(chunk))
		for i, c := range chunk {
			args[i], marks[i] = c.ToolUseID, "?"
		}
		query := fmt.Sprintf("SELECT tool_use_id, %s FROM calls WHERE tool_use_id IN (%s)",
			strings.Join(selects, ", "), strings.Join(marks, ", "))
		if err := readStoredNulls(ctx, store, query, args, nulls); err != nil {
			return 0, 0, err
		}
	}
	for _, c := range calls {
		stored, ok := nulls[c.ToolUseID]
		if !ok {
			inserted++
			continue
		}
		for i, col := range callColumns {
			if stored[i] && col.given(c) {
				filled++
				break
			}
		}
	}
	return inserted, filled, nil
}

func readStoredNulls(
	ctx context.Context,
	store *callmeter.Store,
	query string,
	args []any,
	nulls map[string][]bool,
) error {
	found, err := store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("look up stored calls: %w", err)
	}
	for found.Next() {
		var id string
		flags := make([]int64, len(callColumns))
		targets := []any{&id}
		for i := range flags {
			targets = append(targets, &flags[i])
		}
		if err := found.Scan(targets...); err != nil {
			return errors.Join(fmt.Errorf("scan stored call: %w", err), found.Close())
		}
		isNull := make([]bool, len(flags))
		for i, flag := range flags {
			isNull[i] = flag != 0
		}
		nulls[id] = isNull
	}
	if err := errors.Join(found.Err(), found.Close()); err != nil {
		return fmt.Errorf("read stored calls: %w", err)
	}
	return nil
}
