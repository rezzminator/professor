package costsink

import (
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/costledger"
)

var epoch = time.Date(2026, 10, 9, 18, 27, 0, 0, time.UTC)

func cost(v int64) *int64 { return &v }

func logHalf(request string, c int64) LogHalf {
	return LogHalf{
		RequestID:           request,
		SessionID:           testSession,
		TS:                  testStart,
		QuerySource:         "agent:builtin:general-purpose",
		Model:               "claude-haiku-5-5",
		InputTokens:         2,
		OutputTokens:        186,
		CacheReadTokens:     56278,
		CacheCreationTokens: 11693,
		CostMicros:          cost(c),
		Speed:               "normal",
		Effort:              "medium",
	}
}

func spanHalf(request, agent, parent string) SpanHalf {
	return SpanHalf{
		RequestID:           request,
		SessionID:           testSession,
		AgentID:             agent,
		ParentAgentID:       parent,
		QuerySource:         "agent:builtin:general-purpose",
		Model:               "claude-haiku-5-5",
		InputTokens:         2,
		OutputTokens:        186,
		CacheReadTokens:     56278,
		CacheCreationTokens: 11693,
		Speed:               "normal",
		Effort:              "medium",
		EndTS:               "2026-10-09T18:27:32.847Z",
	}
}

type collector struct{ rows []costledger.Row }

func (c *collector) emit(row costledger.Row) { c.rows = append(c.rows, row) }

func TestJoinerPairsLogThenSpanIntoOneRow(t *testing.T) {
	var c collector
	j := NewJoiner(20*time.Second, c.emit)
	j.AddLog(logHalf("req_1", 2118), epoch)
	if len(c.rows) != 0 {
		t.Fatalf("a lone log half emitted %d rows before its wait", len(c.rows))
	}
	j.AddSpan(spanHalf("req_1", "a0000000000000001", "a0000000000000002"), epoch.Add(3*time.Second))
	if len(c.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(c.rows))
	}
	r := c.rows[0]
	if r.Join != costledger.JoinLogSpan || r.AgentID != "a0000000000000001" || r.ParentAgentID != "a0000000000000002" ||
		r.CostUSDMicros == nil || *r.CostUSDMicros != 2118 || r.TS != testStart || r.QuerySource != "agent:builtin:general-purpose" ||
		r.OutputTokens != 186 || r.SessionID != testSession {
		t.Fatalf("row = %+v", r)
	}
}

func TestJoinerPairsSpanThenLogAndIgnoresDuplicates(t *testing.T) {
	var c collector
	j := NewJoiner(20*time.Second, c.emit)
	j.AddSpan(spanHalf("req_1", "", ""), epoch)
	j.AddSpan(spanHalf("req_1", "", ""), epoch.Add(time.Second))
	j.AddLog(logHalf("req_1", 500), epoch.Add(2*time.Second))
	j.AddLog(logHalf("req_1", 500), epoch.Add(3*time.Second))
	j.AddSpan(spanHalf("req_1", "", ""), epoch.Add(4*time.Second))
	if len(c.rows) != 1 || c.rows[0].Join != costledger.JoinLogSpan || c.rows[0].AgentID != "" {
		t.Fatalf("rows = %+v, want exactly one main log+span row", c.rows)
	}
}

func TestJoinerWritesALogOnlyRowAfterTheWaitAndUpgradesItWhenTheSpanArrives(t *testing.T) {
	var c collector
	j := NewJoiner(20*time.Second, c.emit)
	j.AddLog(logHalf("req_1", 2118), epoch)
	j.Flush(epoch.Add(19 * time.Second))
	if len(c.rows) != 0 {
		t.Fatalf("flushed at 19s: %d rows", len(c.rows))
	}
	j.Flush(epoch.Add(21 * time.Second))
	if len(c.rows) != 1 || c.rows[0].Join != costledger.JoinLog || c.rows[0].AgentID != "?" ||
		c.rows[0].ParentAgentID != "" ||
		c.rows[0].CostUSDMicros == nil ||
		*c.rows[0].CostUSDMicros != 2118 {
		t.Fatalf("log-only row = %+v, want agent ? and the cost", c.rows)
	}
	j.AddSpan(spanHalf("req_1", "a0000000000000001", "a0000000000000002"), epoch.Add(40*time.Second))
	if len(c.rows) != 2 || c.rows[1].Join != costledger.JoinLogSpan || c.rows[1].AgentID != "a0000000000000001" {
		t.Fatalf("rows = %+v, want the upgraded log+span line appended", c.rows)
	}
	j.AddSpan(spanHalf("req_1", "a0000000000000001", "a0000000000000002"), epoch.Add(41*time.Second))
	if len(c.rows) != 2 {
		t.Fatalf("a repeated half re-emitted: %d rows", len(c.rows))
	}
	// Halves with no time of their own: the upgraded row keeps the request's first receipt, the
	// log-only row's ts, never the late span's arrival.
	unstampedLog, unstampedSpan := logHalf("req_2", 2118), spanHalf("req_2", "a0000000000000001", "")
	unstampedLog.TS, unstampedSpan.EndTS = "", ""
	j.AddLog(unstampedLog, epoch)
	j.Flush(epoch.Add(21 * time.Second))
	j.AddSpan(unstampedSpan, epoch.Add(40*time.Second))
	if len(c.rows) != 4 || c.rows[2].TS != "2026-10-09T18:27:00.000Z" || c.rows[3].TS != c.rows[2].TS {
		t.Fatalf("rows = %+v, want the log-only and upgraded rows both at the first receipt", c.rows[2:])
	}
}

func TestJoinerWritesASpanOnlyRowWithNullCostAndUpgradesItWhenTheLogArrives(t *testing.T) {
	var c collector
	j := NewJoiner(20*time.Second, c.emit)
	j.AddSpan(spanHalf("req_1", "a0000000000000001", "a0000000000000002"), epoch)
	j.Flush(epoch.Add(21 * time.Second))
	if len(c.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(c.rows))
	}
	r := c.rows[0]
	if r.Join != costledger.JoinSpan || r.CostUSDMicros != nil || r.TS != "2026-10-09T18:27:32.847Z" ||
		r.AgentID != "a0000000000000001" {
		t.Fatalf("span-only row = %+v, want null cost, the span end time and its agent", r)
	}
	j.AddLog(logHalf("req_1", 2118), epoch.Add(60*time.Second))
	if len(c.rows) != 2 || c.rows[1].Join != costledger.JoinLogSpan || *c.rows[1].CostUSDMicros != 2118 {
		t.Fatalf("rows = %+v, want the upgraded line", c.rows)
	}
}

func TestJoinerDrainFlushesEverythingPendingNow(t *testing.T) {
	var c collector
	j := NewJoiner(20*time.Second, c.emit)
	j.AddLog(logHalf("req_1", 10), epoch)
	j.AddSpan(spanHalf("req_2", "a0000000000000001", ""), epoch)
	// A lone half without its own time is stamped with its receipt, never left untimed.
	unstampedLog, unstampedSpan := logHalf("req_3", 10), spanHalf("req_4", "a0000000000000001", "")
	unstampedLog.TS, unstampedSpan.EndTS = "", ""
	j.AddLog(unstampedLog, epoch.Add(time.Second))
	j.AddSpan(unstampedSpan, epoch.Add(2*time.Second))
	j.Drain()
	if len(c.rows) != 4 {
		t.Fatalf("Drain emitted %d rows, want 4", len(c.rows))
	}
	joins, stamps := map[string]string{}, map[string]string{}
	for _, r := range c.rows {
		joins[r.RequestID], stamps[r.RequestID] = r.Join, r.TS
	}
	if joins["req_1"] != costledger.JoinLog || joins["req_2"] != costledger.JoinSpan {
		t.Fatalf("joins = %v", joins)
	}
	if stamps["req_3"] != "2026-10-09T18:27:01.000Z" || stamps["req_4"] != "2026-10-09T18:27:02.000Z" {
		t.Fatalf("stamps = %v, want each unstamped lone half at its receipt time", stamps)
	}
}
