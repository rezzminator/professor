package installer

import (
	"context"
	"log/slog"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// comp is the activity-log component the installer's ledger doors record
// under (spec § Middleware, `installer`).
const comp = "installer"

// modeName spells a Mode for the record's kind field.
func modeName(mode Mode) string {
	switch mode {
	case ModeDryRun:
		return "dry-run"
	case ModeApply:
		return "apply"
	case ModeUninstall:
		return "uninstall"
	}
	return "unknown"
}

// record writes one installer.step record for a ledger row: the mode as
// kind, the row's result (change, ok, skip) as decision, its message as
// step — the handler caps the path — and err at ERROR when a change's
// action failed. say is presentation and never reaches here.
func (installer *engine) record(result, message string, err error) {
	ctx := obs.Component(context.Background(), comp)
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.String("kind", modeName(installer.options.Mode)),
		slog.String("decision", result),
		slog.String("step", message),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String(obs.FieldErr, err.Error()))
	}
	obs.Logger(ctx).LogAttrs(ctx, level, "installer.step", attrs...)
}

// runSpan is the installer.run span: it records a failure that happens
// before any ledger row exists (option normalization, an unknown mode, the
// running-service gate), with the mode as kind.
func runSpan(ctx context.Context, mode Mode) func(err error) {
	return obs.Span(obs.With(obs.Component(ctx, comp), "kind", modeName(mode)), "installer.run")
}
