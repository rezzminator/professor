package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// SeatRequest is the existing socket facade for native-seat commands. Agent waits
// use the caller's deadline rather than the reporter's short I/O timeout.
func SeatRequest(ctx context.Context, socket, method string, params any) (json.RawMessage, error) {
	if socket == "" || method == "" {
		return nil, fmt.Errorf("herdr seat request: socket and method are required")
	}
	line, err := json.Marshal(request{ID: "pfm-seat", Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("encode herdr %s: %w", method, err)
	}
	limit := ioTimeout
	if method == "agent.wait" {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, fmt.Errorf("herdr agent.wait: caller deadline is required")
		}
		limit = time.Until(deadline)
	}
	result, err := callOneWithin(ctx, socket, line, limit)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 || string(result) == "null" {
		return nil, fmt.Errorf("herdr %s: response has no result", method)
	}
	return result, nil
}
