package index

// The OpenCode 2.x store, read as 1.x. OpenCode 2.x (anomalyco/opencode,
// packages/core/src/database/schema.gen.ts) renamed `session` to `session_v2` and
// keeps a message's kind in session_message.type and its data without id or kind:
// a prompt is {text, files, agents, time}, an answer {agent, model {providerID, id,
// variant}, content, cost, tokens, finish, error {type, message}, time}; an answer's
// content items move into session_part rows keyed (message_id, ordinal), any left
// inline in data.content read after them. A tool item is {id, name, state {status,
// input, content, error {type, message}, metadata}, time {created, completed}}, its
// names renamed from 1.x (bash shell, task subagent, apply_patch patch; a subagent's
// subagent_type its agent). openCodeV2Relations inverts that map (packages/core/src/
// database/v1-migration.bun.ts), so every reader decodes one shape:
//
//   - session: session_v2, a null title as "".
//   - message: the user and assistant rows only (a synthetic, compaction or system row
//     is no 1.x message); a prompt carries its session's current agent and model, the
//     latest prompt's in 1.x, as 2.x keeps neither on the prompt; an answer's model id
//     reads as modelID, its variant "default" as none, its error as {name, data {message}}.
//   - part: a prompt's text as one text part `{message}/0`; an answer's session_part rows
//     `{message}/p{ordinal}`, then its inline items `{message}/q{index}`; a tool's call id,
//     1.x name, text content as output, error message and time, a reasoning's time.
//   - a member 2.x holds as null reads as absent (json_patch drops it); a row whose data
//     is no JSON keeps its text, so the mirror's validation counts it malformed.

// openCodeV2Relations presents a 2.x store as 1.x's session, message and part.
const openCodeV2Relations = `
WITH session AS (
    SELECT id, project_id, parent_id, COALESCE(title, '') AS title, directory,
           time_created, time_updated, time_archived
      FROM session_v2
),
message AS (
    SELECT m.id, m.session_id, m.time_created, m.time_updated,
           CASE
             WHEN NOT json_valid(m.data) THEN m.data
             WHEN m.type = 'user' THEN json_object(
               'role', 'user',
               'agent', COALESCE(s.agent, ''),
               'model', json_object(
                 'providerID', COALESCE(CASE WHEN json_valid(s.model) THEN s.model ->> '$.providerID' END, ''),
                 'modelID', COALESCE(CASE WHEN json_valid(s.model) THEN s.model ->> '$.id' END, '')))
             ELSE json_patch('{}', json_object(
               'role', 'assistant',
               'agent', json(m.data -> '$.agent'),
               'providerID', json(m.data -> '$.model.providerID'),
               'modelID', json(m.data -> '$.model.id'),
               'variant', NULLIF(m.data ->> '$.model.variant', 'default'),
               'finish', json(m.data -> '$.finish'),
               'cost', json(m.data -> '$.cost'),
               'tokens', json(m.data -> '$.tokens'),
               'error', json(CASE WHEN json_type(m.data, '$.error') = 'object'
                 THEN json_object('name', m.data ->> '$.error.type',
                                  'data', json_object('message', m.data ->> '$.error.message'))
                 ELSE m.data -> '$.error' END)))
           END AS data
      FROM session_message m
      LEFT JOIN session_v2 s ON s.id = m.session_id
     WHERE m.type IN ('user', 'assistant')
),
opencode_v2_content AS (
    SELECT message_id || printf('/p%08d', ordinal) AS id, message_id, session_id,
           time_created, time_updated, data AS v
      FROM session_part
    UNION ALL
    SELECT m.id || printf('/q%08d', c.key), m.id, m.session_id, m.time_created, m.time_updated, c.value
      FROM session_message m,
           json_each(CASE
             WHEN m.type IS NOT 'assistant' THEN '[]'
             WHEN NOT json_valid(m.data) THEN '[]'
             WHEN json_type(m.data, '$.content') IS NOT 'array' THEN '[]'
             ELSE m.data -> '$.content' END) c
),
part AS (
    SELECT m.id || '/0' AS id, m.id AS message_id, m.session_id, m.time_created, m.time_updated,
           json_patch('{}', json_object('type', 'text', 'text', json(m.data -> '$.text'))) AS data
      FROM session_message m
     WHERE m.type = 'user'
       AND CASE WHEN json_valid(m.data)
                THEN json_type(m.data, '$.text') IS NOT 'text' OR m.data ->> '$.text' <> '' END
    UNION ALL
    SELECT id, message_id, session_id, time_created, time_updated,
           CASE
             WHEN NOT json_valid(v) THEN v
             WHEN json_type(v) IS NOT 'object' THEN v
             WHEN v ->> '$.type' = 'tool' THEN json_patch('{}', json_object(
               'type', 'tool',
               'callID', json(v -> '$.id'),
               'tool', CASE v ->> '$.name' WHEN 'shell' THEN 'bash' WHEN 'subagent' THEN 'task'
                         WHEN 'patch' THEN 'apply_patch' ELSE v ->> '$.name' END,
               'state', json_object(
                 'status', json(v -> '$.state.status'),
                 'input', json(CASE WHEN v ->> '$.name' = 'subagent' AND json_type(v, '$.state.input') = 'object'
                   THEN json_set(v -> '$.state.input', '$.subagent_type', json(v -> '$.state.input.agent'))
                   ELSE v -> '$.state.input' END),
                 'output', (SELECT group_concat(CASE WHEN e.type = 'object' AND e.value ->> '$.type' = 'text'
                                                     THEN e.value ->> '$.text' END, char(10))
                              FROM json_each(v, '$.state.content') e),
                 'error', v ->> '$.state.error.message',
                 'metadata', json(v -> '$.state.metadata'),
                 'time', json_object('start', json(v -> '$.time.created'), 'end', json(v -> '$.time.completed')))))
             WHEN v ->> '$.type' = 'reasoning' THEN json_patch('{}', json_object(
               'type', 'reasoning',
               'text', json(v -> '$.text'),
               'time', json_object('start', json(v -> '$.time.created'), 'end', json(v -> '$.time.completed'))))
             ELSE v
           END
      FROM opencode_v2_content
)`
