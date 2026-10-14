CREATE TABLE comms_v5 (
    id INTEGER PRIMARY KEY,
    at_ns INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('call','group','spawn')),
    sender_session TEXT NOT NULL DEFAULT '',
    sender_label TEXT NOT NULL DEFAULT '',
    sender_uuid TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL DEFAULT '',
    receiver_socket TEXT NOT NULL DEFAULT '',
    receiver_pane TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    members TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL
);
INSERT INTO comms_v5 (
    id, at_ns, kind, sender_session, sender_label, sender_uuid, target,
    receiver_socket, receiver_pane, group_name, members, message
)
SELECT id, at_ns, CASE kind WHEN 'inject' THEN 'call' ELSE kind END,
    sender_session, sender_label, sender_uuid, target, receiver_socket,
    receiver_pane, group_name, members, message
FROM comms;
DROP TABLE comms;
ALTER TABLE comms_v5 RENAME TO comms;
CREATE INDEX comms_at ON comms(at_ns);
