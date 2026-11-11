package chat

import (
	"context"
	"fmt"
	"io"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// NewListShield is the one herdr-shield reader a listing's scan and its row
// inspections share (ListRequest.Shield, InspectRows).
func NewListShield(runtime *pfmconfig.Runtime) (*herdr.Shield, error) {
	effective, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		return nil, fmt.Errorf("herdr-shield reader: %w", err)
	}
	return gather.HerdrShield(gather.NewProcFS(effective.Paths.ProcRoot)), nil
}

// RowInspect inspects one chat, proving its native pane through proofs.
type RowInspect func(context.Context, headless.Chat, time.Time, pfmcall.HerdrProofs) (headless.Status, error)

// InspectRows inspects every row of one listing through shield, held for the
// whole pass: herdr-shield lists each Herdr socket once however many native
// rows there are. Statuses and errors run parallel to rows.
func InspectRows(
	ctx context.Context,
	rows []compose.Row,
	now time.Time,
	shield *herdr.Shield,
	inspect RowInspect,
) ([]headless.Status, []error) {
	defer shield.Hold()()
	statuses, errs := make([]headless.Status, len(rows)), make([]error, len(rows))
	for index := range rows {
		statuses[index], errs[index] = inspect(ctx, FromRow(rows[index]), now, pfmcall.HerdrProofs{Shield: shield})
	}
	return statuses, errs
}

// ListInspected is List followed by every listed row's inspection
// (InspectSeatWith), the scan, the Herdr presence probe and the inspections
// sharing one herdr-shield reader held for the whole pass, so a scan longer
// than a listing's freshness never makes the inspections list again. The
// statuses and errors run parallel to the result's Rows.
func ListInspected(
	ctx context.Context,
	runtime *pfmconfig.Runtime,
	request ListRequest,
	now time.Time,
	warn io.Writer,
) (ListResult, []headless.Status, []error, error) {
	shield, err := NewListShield(runtime)
	if err != nil {
		return ListResult{}, nil, nil, err
	}
	defer shield.Hold()()
	request.Shield = shield
	listed, err := List(ctx, runtime, request, warn)
	if err != nil {
		return ListResult{}, nil, nil, err
	}
	statuses, errs := InspectRows(
		ctx,
		listed.Rows,
		now,
		shield,
		func(ctx context.Context, chat headless.Chat, now time.Time, proofs pfmcall.HerdrProofs) (headless.Status, error) {
			return InspectSeatWith(ctx, runtime, chat, now, nil, proofs)
		},
	)
	return listed, statuses, errs, nil
}

// realHerdrProbe is the probe on the host's own environment, tmux and Herdr
// sockets and process table (under values' proc root, the Claude binary being
// what a chat's pane runs); every failed lookup is also written to warn. It
// reads herdr-shield through shield, the listing's own (nil: one of its own).
func realHerdrProbe(warn io.Writer, values paths.Values, claudeBinary string, shield *herdr.Shield) herdr.Probe {
	proc := gather.NewProcFS(values.ProcRoot)
	if shield == nil {
		shield = gather.HerdrShield(proc)
	}
	return herdr.Probe{
		Getenv:   paths.OSEnv{}.Get,
		LookPath: deps.RealRunner{}.LookPath,
		Tmux:     runTmux,
		Call:     herdr.Call,
		Environ:  proc.Environ,
		Shield:   shield,
		Hookless: hooklessLaunch(warn, proc, claudeBinary),
		Warn:     warn,
	}
}
