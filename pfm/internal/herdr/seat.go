package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// SupportedProtocol is the native-seat wire contract, independent of release lifetime safety.
const SupportedProtocol = 22

// InputNotSentError marks a failure before a request could write any bytes.
// An ordinary transport error, especially a failed write/read, is not this proof.
type InputNotSentError struct{ Cause error }

func (err *InputNotSentError) Error() string { return err.Cause.Error() }
func (err *InputNotSentError) Unwrap() error { return err.Cause }

const maxSeatRequest = 1 << 20

// RequestTooLargeError proves that the encoded request was refused before dialing.
type RequestTooLargeError struct {
	Method string
	Bytes  int
}

func (err *RequestTooLargeError) Error() string {
	return fmt.Sprintf(
		"herdr %s request is %d encoded bytes; limit is %d; nothing was sent",
		err.Method,
		err.Bytes,
		maxSeatRequest,
	)
}

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
	if len(line) > maxSeatRequest {
		return nil, &RequestTooLargeError{Method: method, Bytes: len(line)}
	}
	limit := ioTimeout
	if method == "pane.read" {
		var read struct {
			Params struct {
				Source string `json:"source"`
				Format string `json:"format"`
			} `json:"params"`
		}
		if err := json.Unmarshal(line, &read); err != nil {
			return nil, fmt.Errorf("inspect encoded herdr pane.read request: %w", err)
		}
		// Plain recent snapshots can traverse alternate-screen history (15s) and
		// restore the viewport (5s). Visible and ANSI reads remain passive snapshots.
		if read.Params.Source == "recent" && read.Params.Format != "ansi" {
			limit = 21 * time.Second
		}
	}
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

// ValidateNotificationShown distinguishes delivery from transport success.
func ValidateNotificationShown(body json.RawMessage) error {
	var ack struct {
		Type   string `json:"type"`
		Shown  *bool  `json:"shown"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &ack); err != nil {
		return fmt.Errorf("decode Herdr notification: %w", err)
	}
	if ack.Type != "notification_show" || ack.Shown == nil || ack.Reason == "" ||
		(*ack.Shown && ack.Reason != "shown") || (!*ack.Shown && ack.Reason == "shown") {
		return fmt.Errorf("unexpected Herdr notification acknowledgement: %s", body)
	}
	if !*ack.Shown {
		return &NotificationNotShownError{Reason: ack.Reason}
	}
	return nil
}

// NotificationRateLimited is the not-shown reason Herdr gives a notification
// it refused because others came too fast; a later attempt can be shown.
const NotificationRateLimited = "rate_limited"

// NotificationNotShownError is a well-formed acknowledgement whose
// notification Herdr did not show, with Herdr's reason.
type NotificationNotShownError struct {
	Reason string
}

func (e *NotificationNotShownError) Error() string {
	return fmt.Sprintf("herdr notification not shown (%s)", e.Reason)
}

// RemoveReloadFiles removes a retired native seat's reload schedule record and
// log from sidDir; a file already gone is fine. An id that
// reload.LockWorkerSchedule refuses to name a file with never had any. Every
// door that retires a Herdr seat calls it before the seat row goes, so a
// failure leaves the seat retryable.
func RemoveReloadFiles(sidDir, seatID string) error {
	if seatID == "" || seatID == "." || seatID == ".." || strings.ContainsAny(seatID, "/\x00") {
		return nil
	}
	if sidDir == "" {
		return errors.New("remove seat reload files: SID directory is unset")
	}
	var errs []error
	for _, suffix := range []string{".pid", ".log"} {
		path := filepath.Join(sidDir, paths.SIDReloadHerdrPrefix+seatID+suffix)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}
