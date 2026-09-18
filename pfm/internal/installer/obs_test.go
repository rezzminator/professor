package installer

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestLedgerDoorsRecordUnderTheInstallerComponent: engine.change/ok/skip are
// the installer's ledger doors — each writes one comp=installer record with
// the mode, the result and the step (a path, capped by the handler); say is
// presentation and writes nothing. A change whose action fails is ERROR.
func TestLedgerDoorsRecordUnderTheInstallerComponent(t *testing.T) {
	_, recorder := obs.Test(t)
	var stdout bytes.Buffer
	installer := &engine{options: Options{Mode: ModeApply, Stdout: &stdout}, apply: true}
	installer.say("pfm install: banner")
	if err := installer.change("create ~/.local/bin/pfm", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	installer.ok("shim current")
	installer.skip("launchd not on this platform")
	if err := installer.change(
		"link ~/.claude/agents",
		func() error { return errors.New("read-only file system") },
	); err == nil {
		t.Fatal("the failing action's error was swallowed")
	}
	records := recorder.Records()
	if len(records) != 4 {
		t.Fatalf("records = %d, want one per ledger row and none for say: %s", len(records), recorder.Raw())
	}
	for index, want := range []struct{ decision, step string }{
		{"change", "create ~/.local/bin/pfm"}, {"ok", "shim current"}, {"skip", "launchd not on this platform"}, {"change", "link ~/.claude/agents"},
	} {
		record := records[index]
		if record.Message != "installer.step" {
			t.Fatalf("record %d = %s, want installer.step", index, record.Message)
		}
		for key, value := range map[string]any{obs.FieldComp: "installer", "decision": want.decision, "step": want.step, "kind": "apply"} {
			if got, _ := record.Field(key); got != value {
				t.Fatalf("record %d %s = %v, want %v", index, key, got, value)
			}
		}
	}
	if records[3].Level != slog.LevelError.String() || records[0].Level != slog.LevelInfo.String() {
		t.Fatalf(
			"levels: failed change at %s (want ERROR), change at %s (want INFO)",
			records[3].Level,
			records[0].Level,
		)
	}
	if got, _ := records[3].Field(obs.FieldErr); got != "read-only file system" {
		t.Fatalf("err = %v", got)
	}
	if !strings.Contains(stdout.String(), "  change  create") {
		t.Fatalf("the ledger line stopped reaching stdout: %q", stdout.String())
	}
}

// TestRunSpanRecordsAFailureBeforeAnyLedgerRow: an option the installer
// refuses before it can write a ledger row still ends installer.run at ERROR.
func TestRunSpanRecordsAFailureBeforeAnyLedgerRow(t *testing.T) {
	ctx, recorder := obs.Test(t)
	if _, err := Run(ctx, Options{Mode: Mode(9), Home: t.TempDir()}); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	records := recorder.Records()
	var end *obs.Record
	for index := range records {
		if records[index].Message == "installer.run.end" {
			end = &records[index]
		}
	}
	if end == nil {
		t.Fatalf("no installer.run.end record: %s", recorder.Raw())
	}
	if end.Level != slog.LevelError.String() {
		t.Fatalf("a refused run ended at %s, want ERROR", end.Level)
	}
	if comp, _ := end.Field(obs.FieldComp); comp != "installer" {
		t.Fatalf("comp = %v", comp)
	}
	if _, found := end.Field(obs.FieldDur); !found {
		t.Fatalf("no dur_ms: %v", end.Fields)
	}
	_ = context.Background
}
