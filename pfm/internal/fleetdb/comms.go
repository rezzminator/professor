package fleetdb

import (
	"context"
	"errors"
	"fmt"
)

const (
	KindInject = "inject"
	KindSpawn  = "spawn"
)

// CommsEvent is one durable chat-to-chat communication event.
type CommsEvent struct {
	ID             int64
	AtNS           int64
	Kind           string
	SenderSession  string
	SenderLabel    string
	SenderUUID     string
	Target         string
	ReceiverSocket string
	ReceiverPane   string
	// GroupName and Members are historical: they carried the chat-group
	// feature's ledger rows (kind "group") before its removal. The columns
	// and these fields stay so existing rows keep scanning; nothing writes
	// a non-empty value into either anymore.
	GroupName string
	Members   string
	Message   string
}

// RecordComms appends one event to the shared operator ledger.
func (s *Store) RecordComms(ctx context.Context, event CommsEvent) error {
	if s.db == nil {
		return fmt.Errorf("record comms event: %w", s.degraded)
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO comms(
  at_ns,kind,sender_session,sender_label,sender_uuid,target,
  receiver_socket,receiver_pane,group_name,members,message
) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		event.AtNS,
		event.Kind,
		event.SenderSession,
		event.SenderLabel,
		event.SenderUUID,
		event.Target,
		event.ReceiverSocket,
		event.ReceiverPane,
		event.GroupName,
		event.Members,
		event.Message,
	); err != nil {
		return fmt.Errorf("record comms event: %w", err)
	}
	return nil
}

// CommsSince returns the newest events in the requested nanosecond window.
func (s *Store) CommsSince(
	ctx context.Context,
	sinceNS int64,
	limit int,
) ([]CommsEvent, error) {
	if s.db == nil {
		return nil, fmt.Errorf("query comms events: %w", s.degraded)
	}
	result := make([]CommsEvent, 0)
	if limit <= 0 {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,at_ns,kind,sender_session,sender_label,sender_uuid,target,
       receiver_socket,receiver_pane,group_name,members,message
FROM comms
WHERE at_ns >= ?
ORDER BY at_ns DESC,id DESC
LIMIT ?`, sinceNS, limit)
	if err != nil {
		return nil, fmt.Errorf("query comms events: %w", err)
	}
	for rows.Next() {
		var event CommsEvent
		if err := rows.Scan(
			&event.ID,
			&event.AtNS,
			&event.Kind,
			&event.SenderSession,
			&event.SenderLabel,
			&event.SenderUUID,
			&event.Target,
			&event.ReceiverSocket,
			&event.ReceiverPane,
			&event.GroupName,
			&event.Members,
			&event.Message,
		); err != nil {
			scanErr := fmt.Errorf("scan comms event: %w", err)
			if closeErr := rows.Close(); closeErr != nil {
				return nil, errors.Join(scanErr, fmt.Errorf("close comms rows: %w", closeErr))
			}
			return nil, scanErr
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		iterationErr := fmt.Errorf("iterate comms events: %w", err)
		if closeErr := rows.Close(); closeErr != nil {
			return nil, errors.Join(iterationErr, fmt.Errorf("close comms rows: %w", closeErr))
		}
		return nil, iterationErr
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close comms rows: %w", err)
	}
	return result, nil
}
