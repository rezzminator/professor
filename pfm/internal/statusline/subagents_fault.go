package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxSubagentInput caps the row context ServeSubagents reads: far past any
// real panel (a task row is about 1 KiB), so a payload over it is a fault to
// show, never a size to trim.
const maxSubagentInput = 16 << 20

// readSubagentInput reads stdin whole up to maxSubagentInput. What it read
// comes back with any error, so the rows it holds can still show that error;
// input past the cap is an error, not a silent cut.
func readSubagentInput(stdin io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(stdin, maxSubagentInput+1))
	if err != nil {
		return raw, fmt.Errorf("read input: %w", err)
	}
	if len(raw) > maxSubagentInput {
		return raw[:maxSubagentInput], fmt.Errorf(
			"read input: over %d MiB, the rows past it are not shown",
			maxSubagentInput>>20,
		)
	}
	return raw, nil
}

// failSubagents shows a fault on every task row whose id raw still holds and
// logs it; input without one has no row to show it on, and the log says so.
func failSubagents(stage string, raw []byte, cause error, stdout, stderr io.Writer) int {
	ids := subagentTaskIDs(raw)
	if len(ids) == 0 {
		fmt.Fprintf(stderr, "pfm statusline --subagents: %s (fail-open): no task id to show it on: %v\n", stage, cause)
		return 0
	}
	fmt.Fprintf(stderr, "pfm statusline --subagents: %s (rows show the error): %v\n", stage, cause)
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	content := rowOpen + cBad + "pfm statusline: " + strings.Join(strings.Fields(cause.Error()), " ") + reset
	for _, id := range ids {
		if err := encoder.Encode(subagentRow{ID: id, Content: content}); err != nil {
			fmt.Fprintf(stderr, "pfm statusline --subagents: encode error row %s: %v\n", id, err)
			break
		}
	}
	if _, err := io.WriteString(stdout, out.String()); err != nil {
		fmt.Fprintf(stderr, "pfm statusline --subagents: write output (fail-open): %v\n", err)
	}
	return 0
}

// subagentTaskIDs lists the task ids raw holds, in order, once each, reading
// the payload as a stream: a payload torn off after some tasks, or with one
// field of the wrong type, still yields every id before the tear.
func subagentTaskIDs(raw []byte) []string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if open, err := decoder.Token(); err != nil || open != json.Delim('{') {
		return nil
	}
	var ids []string
	seen := map[string]struct{}{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return ids
		}
		if key != "tasks" {
			var skipped json.RawMessage
			if decoder.Decode(&skipped) != nil {
				return ids
			}
			continue
		}
		if open, err := decoder.Token(); err != nil || open != json.Delim('[') {
			return ids
		}
		for decoder.More() {
			var task struct {
				ID string `json:"id"`
			}
			if err := decoder.Decode(&task); err != nil {
				var wrongType *json.UnmarshalTypeError
				if errors.As(err, &wrongType) {
					continue // the task is whole, its id just is not a string
				}
				return ids
			}
			if _, dup := seen[task.ID]; task.ID != "" && !dup {
				seen[task.ID] = struct{}{}
				ids = append(ids, task.ID)
			}
		}
		return ids
	}
	return ids
}
