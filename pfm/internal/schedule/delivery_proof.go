package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// A receipt closes the process-death window between submission and the database
// mark. It contains no prompt. One file per schedule is replaced at each fire.
// The fire lock serializes ticks, including a timer catching up. Schedule
// removal may also discard a receipt after deleting its row.
type deliveryReceipt struct {
	Tag       string    `json:"tag"`
	Attempted time.Time `json:"attempted"`
	Delivered bool      `json:"delivered"`
}

type fireTimeKey struct{}

type fireTiming struct {
	tick, started time.Time
}

const scheduleRetryGrace = 5 * time.Minute

func fireTag(r fleetdb.Schedule) string {
	return fmt.Sprintf("[pfm-schedule id=%d fire=%s]", r.ID, r.NextFire.UTC().Format(time.RFC3339Nano))
}

func scheduleReceiptPath(stateDB string, id int64) string {
	return filepath.Join(filepath.Dir(stateDB), "schedule-delivery", fmt.Sprintf("%d.json", id))
}

func readReceipt(path, tag string) (deliveryReceipt, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return deliveryReceipt{}, nil
	}
	if err != nil {
		return deliveryReceipt{}, fmt.Errorf("read delivery receipt: %w", err)
	}
	var receipt deliveryReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return deliveryReceipt{}, fmt.Errorf("decode delivery receipt: %w", err)
	}
	if receipt.Tag == "" || receipt.Attempted.IsZero() {
		return deliveryReceipt{}, fmt.Errorf("invalid delivery receipt %s", path)
	}
	if receipt.Tag != tag {
		return deliveryReceipt{}, nil
	}
	return receipt, nil
}

func writeReceipt(path string, receipt deliveryReceipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode delivery receipt: %w", err)
	}
	if err := atomicfile.Write(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("save delivery receipt: %w", err)
	}
	return nil
}

func removeScheduleReceipt(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove delivery receipt: %w", err)
	}
	return nil
}

func (deliverer *chatDeliverer) transcriptProof(ctx context.Context, r fleetdb.Schedule) (bool, error) {
	chat, err := pfmchat.Target(ctx, r.SessionID, deliverer.runtime)
	if err != nil {
		return false, fmt.Errorf("resolve transcript: %w", err)
	}
	if chat.Path == "" {
		return false, pfmchat.ErrNoTranscript
	}
	entries, err := transcript.All(ctx, chat.Path, string(chat.Engine))
	if err != nil {
		return false, fmt.Errorf("read transcript: %w", err)
	}
	for _, entry := range entries {
		if entry.Role == transcript.RoleUser && strings.Contains(entry.Text, fireTag(r)) {
			return true, nil
		}
	}
	return false, nil
}

func unprovenWake(receipt deliveryReceipt, reason string) error {
	return fmt.Errorf("unproven since %s: %s", receipt.Attempted.UTC().Format(time.RFC3339), reason)
}

func (deliverer *chatDeliverer) submitSchedule(ctx context.Context, r fleetdb.Schedule, target, message string) error {
	path := scheduleReceiptPath(deliverer.runtime.Paths.StateDB, r.ID)
	proven, proofErr := deliverer.transcriptProof(ctx, r)
	if proven {
		return nil // Independent proof can reconcile even a damaged receipt.
	}
	receipt, err := readReceipt(path, fireTag(r))
	if err != nil {
		return err
	}
	if receipt.Delivered {
		return nil
	}
	// A slow earlier wake or transcript scan must not spend this attempt's
	// retry grace. Keep the tick's clock basis while adding actual elapsed time.
	now := obs.Clock(ctx).Now()
	if timing, ok := ctx.Value(fireTimeKey{}).(fireTiming); ok {
		now = timing.tick.Add(now.Sub(timing.started))
	}
	retrying := !receipt.Attempted.IsZero()
	if retrying {
		if proofErr != nil {
			return unprovenWake(receipt, "transcript unreadable: "+proofErr.Error())
		}
		if now.Sub(receipt.Attempted) < scheduleRetryGrace {
			return unprovenWake(
				receipt,
				"transcript has no fire tag yet; waiting at least one 5-minute tick before an idle-only retry",
			)
		}
	}
	previous := receipt
	receipt = deliveryReceipt{Tag: fireTag(r), Attempted: now}
	if err := writeReceipt(path, receipt); err != nil {
		return err // No input without a durable attempted receipt.
	}
	res, callErr := deliverer.call(ctx, r, pfmcall.Request{
		Target: target, Message: message, RequireIdle: retrying,
	})
	if callErr == nil && res.Code == 0 && res.Typed && (res.Status == "delivered" || res.Status == "queued") {
		receipt.Delivered = true
		return writeReceipt(path, receipt)
	}
	// An uncertain acknowledgement may have accepted the prompt even with rc 6.
	// Read the engine's record, never the call's own assertion of delivery.
	proven, proofErr = deliverer.transcriptProof(ctx, r)
	if proven {
		receipt.Delivered = true
		return writeReceipt(path, receipt)
	}
	if callErr == nil && !res.Typed {
		if retrying {
			if err := writeReceipt(path, previous); err != nil {
				return err
			}
			return unprovenWake(previous, "target not idle at an empty composer: "+res.Message)
		}
		if err := removeScheduleReceipt(path); err != nil {
			return err
		}
		return fmt.Errorf("call into %s: code %d: %s", target, res.Code, res.Message)
	}
	reason := fmt.Sprintf("submission %s (code %d): %s", res.Status, res.Code, res.Message)
	if callErr != nil {
		reason = "submission uncertain: " + callErr.Error()
	}
	if proofErr != nil {
		reason = "transcript unreadable: " + proofErr.Error() + "; " + reason
	}
	return unprovenWake(receipt, reason)
}

// ForgetDelivery runs only after the store has durably marked a fire, so a
// failed mark retains the receipt and the next tick cannot submit twice.
func (deliverer *chatDeliverer) ForgetDelivery(r fleetdb.Schedule) error {
	return removeScheduleReceipt(scheduleReceiptPath(deliverer.runtime.Paths.StateDB, r.ID))
}
