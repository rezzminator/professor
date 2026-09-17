package fleetdb

import (
	"context"
	"errors"
	"fmt"
)

type rowSet interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

func collectRows[T any](
	rows rowSet,
	scan func(rowSet) (T, error),
	scanContext, iterationContext, closeContext string,
) (result []T, returnErr error) {
	result = make([]T, 0)
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("%s: %w", closeContext, err))
		}
	}()
	for rows.Next() {
		value, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", scanContext, err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", iterationContext, err)
	}
	return result, nil
}

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
	return collectRows(rows, func(rows rowSet) (CommsEvent, error) {
		var event CommsEvent
		err := rows.Scan(
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
		)
		return event, err
	}, "scan comms event", "iterate comms events", "close comms rows")
}
