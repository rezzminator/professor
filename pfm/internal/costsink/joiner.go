package costsink

import (
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/costledger"
)

// retainFinished bounds how long a request's state is remembered after it was written, so a half
// arriving after its partner was flushed alone still upgrades that row instead of being lost.
const retainFinished = 15 * time.Minute

type joinState int

const (
	pendingLog joinState = iota
	pendingSpan
	flushedLog
	flushedSpan
	joined
)

type entry struct {
	state joinState
	log   LogHalf
	span  SpanHalf
	at    time.Time
}

// Joiner pairs the two halves of an API call on request_id and hands each finished row to emit.
// A half whose partner has not arrived after the wait is emitted alone (a log half with agent "?",
// a span half with a null cost); a partner arriving later appends the upgraded log+span row. A
// half offered twice is written once.
type Joiner struct {
	mu      sync.Mutex
	wait    time.Duration
	emit    func(costledger.Row)
	entries map[string]*entry
}

// NewJoiner returns a Joiner that waits the given time for a half's partner.
func NewJoiner(wait time.Duration, emit func(costledger.Row)) *Joiner {
	return &Joiner{wait: wait, emit: emit, entries: map[string]*entry{}}
}

// AddLog offers a log half.
func (j *Joiner) AddLog(half LogHalf, now time.Time) {
	j.add(half.RequestID, now, pendingLog, pendingSpan, flushedSpan, func(e *entry) { e.log = half })
}

// AddSpan offers a span half.
func (j *Joiner) AddSpan(half SpanHalf, now time.Time) {
	j.add(half.RequestID, now, pendingSpan, pendingLog, flushedLog, func(e *entry) { e.span = half })
}

// add records a half (set stores it) of request id: a first half waits in state pending; a half
// whose partner waits or was flushed alone joins it and emits the log+span row; a repeat is dropped.
func (j *Joiner) add(id string, now time.Time, pending, partnerPending, partnerFlushed joinState, set func(*entry)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, seen := j.entries[id]
	switch {
	case !seen:
		e = &entry{state: pending, at: now}
		set(e)
		j.entries[id] = e
	case e.state == partnerPending || e.state == partnerFlushed:
		set(e)
		e.state, e.at = joined, now
		j.emit(logSpanRow(e.log, e.span))
	}
}

// Flush emits every half that has waited longer than the wait and forgets finished requests older
// than the retention.
func (j *Joiner) Flush(now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for id, e := range j.entries {
		switch {
		case e.state == pendingLog && now.Sub(e.at) > j.wait:
			j.flushAlone(e, now)
		case e.state == pendingSpan && now.Sub(e.at) > j.wait:
			j.flushAlone(e, now)
		case e.state >= flushedLog && now.Sub(e.at) > retainFinished:
			delete(j.entries, id)
		}
	}
}

// Drain emits every pending half now: the chat is gone and no partner will arrive.
func (j *Joiner) Drain() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, e := range j.entries {
		if e.state == pendingLog || e.state == pendingSpan {
			j.flushAlone(e, e.at)
		}
	}
}

func (j *Joiner) flushAlone(e *entry, now time.Time) {
	if e.state == pendingLog {
		e.state = flushedLog
		j.emit(logOnlyRow(e.log))
	} else {
		e.state = flushedSpan
		j.emit(spanOnlyRow(e.span))
	}
	e.at = now
}

func firstFilled(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func logSpanRow(l LogHalf, s SpanHalf) costledger.Row {
	return costledger.Row{
		RequestID: l.RequestID, TS: firstFilled(l.TS, s.EndTS), SessionID: l.SessionID,
		AgentID: s.AgentID, ParentAgentID: s.ParentAgentID,
		QuerySource: firstFilled(l.QuerySource, s.QuerySource), Model: firstFilled(l.Model, s.Model),
		InputTokens: l.InputTokens, OutputTokens: l.OutputTokens,
		CacheReadTokens: l.CacheReadTokens, CacheCreationTokens: l.CacheCreationTokens,
		CostUSDMicros: l.CostMicros, Speed: firstFilled(l.Speed, s.Speed), Effort: firstFilled(l.Effort, s.Effort),
		Join: costledger.JoinLogSpan,
	}
}

func logOnlyRow(l LogHalf) costledger.Row {
	return costledger.Row{
		RequestID: l.RequestID, TS: l.TS, SessionID: l.SessionID, AgentID: costledger.UnknownAgent,
		QuerySource: l.QuerySource, Model: l.Model,
		InputTokens: l.InputTokens, OutputTokens: l.OutputTokens,
		CacheReadTokens: l.CacheReadTokens, CacheCreationTokens: l.CacheCreationTokens,
		CostUSDMicros: l.CostMicros, Speed: l.Speed, Effort: l.Effort, Join: costledger.JoinLog,
	}
}

func spanOnlyRow(s SpanHalf) costledger.Row {
	return costledger.Row{
		RequestID: s.RequestID, TS: s.EndTS, SessionID: s.SessionID, AgentID: s.AgentID, ParentAgentID: s.ParentAgentID,
		QuerySource: s.QuerySource, Model: s.Model,
		InputTokens: s.InputTokens, OutputTokens: s.OutputTokens,
		CacheReadTokens: s.CacheReadTokens, CacheCreationTokens: s.CacheCreationTokens,
		Speed: s.Speed, Effort: s.Effort, Join: costledger.JoinSpan,
	}
}
