package report

import (
	"context"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// stages are every fault stage, always listed so a zero is visible.
var stages = []string{
	callmeter.StagePayload, callmeter.StageStore, callmeter.StageTranscript,
	callmeter.StageParse, callmeter.StageBackfill,
}

// Faults counts faults by stage and command parts by parse status, then lists
// the latest Limit faults. Faults narrow by window and session only: a call
// that failed to record has no calls row to carry the other filters.
func Faults(ctx context.Context, store *callmeter.Store, f Filter, nameOf NameOf) (*Table, error) {
	n := newNames(nameOf)
	t := &Table{
		Title:  f.title("faults", n),
		Header: []string{"KIND", "STAGE/STATUS", "COUNT", "WHEN", "CHAT", "CALL", "ERROR"},
	}
	faultWhere, faultArgs := faultFilter(f, "faults")
	byStage := map[string]int64{}
	var total int64
	err := query(ctx, store, "faults by stage",
		"SELECT COALESCE(stage, ''), COUNT(*) FROM faults WHERE "+faultWhere+" GROUP BY stage", faultArgs,
		func(r rowSource) error {
			var stage string
			var count int64
			if err := r.Scan(&stage, &count); err != nil {
				return err
			}
			byStage[stage] = count
			total += count
			return nil
		})
	if err != nil {
		return nil, err
	}
	where, args := f.where()
	var calls int64
	if err := store.DB().
		QueryRowContext(ctx, "SELECT COUNT(*) FROM calls c WHERE "+where, args...).
		Scan(&calls); err != nil {
		return nil, fmt.Errorf("callmeter report: count calls in window: %w", err)
	}
	if total == 0 && calls == 0 {
		return t, nil
	}
	for _, stage := range stages {
		t.Rows = append(t.Rows, []string{"stage", stage, itoa(byStage[stage]), "", "", "", ""})
		delete(byStage, stage)
	}
	for stage, count := range byStage {
		t.Rows = append(t.Rows, []string{"stage", stage + " (unknown stage)", itoa(count), "", "", "", ""})
	}
	err = query(ctx, store, "parts by parse status",
		`SELECT COALESCE(p.parse_status, '(none)'), COUNT(*) FROM command_parts p
		JOIN calls c ON c.tool_use_id = p.tool_use_id WHERE `+where+` GROUP BY p.parse_status ORDER BY p.parse_status`, args,
		func(r rowSource) error {
			var status string
			var count int64
			if err := r.Scan(&status, &count); err != nil {
				return err
			}
			t.Rows = append(t.Rows, []string{"parse", status, itoa(count), "", "", "", ""})
			return nil
		})
	if err != nil {
		return nil, err
	}
	err = query(
		ctx,
		store,
		"latest faults",
		`SELECT COALESCE(ts, 0), COALESCE(stage, ''), COALESCE(session_id, ''), COALESCE(tool_use_id, ''), COALESCE(error, '')
		FROM faults WHERE `+faultWhere+` ORDER BY ts DESC LIMIT ?`,
		append(faultArgs, f.limit()),
		func(r rowSource) error {
			var ts int64
			var stage, session, call, message string
			if err := r.Scan(&ts, &stage, &session, &call, &message); err != nil {
				return err
			}
			chat := "-"
			if session != "" {
				chat = n.of(session)
			}
			t.Rows = append(t.Rows, []string{
				"fault", stage, "1", time.UnixMilli(ts).UTC().Format("2006-01-02 15:04:05"), chat, call, message,
			})
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	notes, err := gapNotes(ctx, store, f)
	if err != nil {
		return nil, err
	}
	t.Notes = notes
	t.Notes = append(t.Notes, n.notes()...)
	return t, nil
}
