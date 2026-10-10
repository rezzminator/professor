package claudelaunch

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestCostEnvIsTheNineKnobsAndTheMarkerAndNeverTheContentFlags(t *testing.T) {
	want := []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_TRACES_EXPORTER=otlp",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"OTEL_METRICS_EXPORTER=none",
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/json",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:43181",
		"OTEL_LOGS_EXPORT_INTERVAL=1000",
		"OTEL_BSP_SCHEDULE_DELAY=1000",
		"PFM_COST_LEDGER=on",
		"PFM_COST_LEDGER_PID=7",
	}
	got := CostEnv(43181, 7)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CostEnv = %q, want %q", got, want)
	}
	for _, entry := range got {
		for _, banned := range []string{"OTEL_LOG_USER_PROMPTS", "OTEL_LOG_TOOL_DETAILS", "OTEL_LOG_RAW_API_BODIES"} {
			if strings.HasPrefix(entry, banned) {
				t.Errorf("CostEnv sets %s", banned)
			}
		}
		if strings.Contains(entry, "://") && !strings.HasPrefix(strings.SplitN(entry, "=", 2)[1], "http://127.0.0.1:") {
			t.Errorf("%s leaves 127.0.0.1", entry)
		}
	}
	for _, name := range CostEnvNames() {
		if !slices.Contains(LaunchEnvNames(), name) {
			t.Errorf("LaunchEnvNames lacks %s", name)
		}
	}
}

func TestKnobsNameTheCostLedgerRows(t *testing.T) {
	rows := map[string]Knob{}
	for _, knob := range Knobs {
		rows[knob.Name] = knob
	}
	row, ok := rows["costLedger"]
	if !ok || row.Wire != WireWrapper || row.Source != SourceConfig || row.Default != true {
		t.Fatalf("costLedger knob = %#v, want a config-sourced wrapper row defaulting to true", row)
	}
	for _, name := range CostEnvNames() {
		row, ok := rows[name]
		if !ok || row.Wire != WireWrapperEnv || row.Target != name {
			t.Errorf("knob %s = %#v, want a wrapper-env row", name, row)
		}
	}
}

func TestCostPreemptedReasonTable(t *testing.T) {
	ownEnv := append([]string{"HOME=/h", "PATH=/bin"}, CostEnv(40111, 99)...)
	for _, tc := range []struct {
		name     string
		environ  []string
		settings []string
		want     string // substring of the reason; "" = the launch may arm
	}{
		{"clean", []string{"HOME=/h"}, nil, ""},
		{"user enables telemetry in the process env", []string{"CLAUDE_CODE_ENABLE_TELEMETRY=1"}, nil, "CLAUDE_CODE_ENABLE_TELEMETRY"},
		{"user sets one OTEL_ variable", []string{"OTEL_METRICS_EXPORTER=prometheus"}, nil, "OTEL_METRICS_EXPORTER"},
		{"user names a variable with an empty value", []string{"OTEL_SERVICE_NAME="}, nil, "OTEL_SERVICE_NAME"},
		{"settings.json env enables telemetry", nil, []string{"CLAUDE_CODE_ENABLE_TELEMETRY"}, "settings.json"},
		{"settings.json env names an OTEL_ variable", nil, []string{"EDITOR", "OTEL_EXPORTER_OTLP_ENDPOINT"}, "OTEL_EXPORTER_OTLP_ENDPOINT"},
		{"settings.json env without telemetry names", nil, []string{"EDITOR"}, ""},
		// The nested-claude door: a chat started from inside a pfm chat inherits the parent's own
		// exports and marker. They are pfm's, not the user's, so the launch is not pre-empted.
		{"nested chat inheriting pfm's own exports", ownEnv, nil, ""},
		{"inherited marker but a user variable on top", append(slices.Clone(ownEnv), "OTEL_SERVICE_NAME=mine"), nil, "OTEL_SERVICE_NAME"},
		{"inherited marker but the user changed the endpoint", append(slices.Clone(ownEnv), "OTEL_EXPORTER_OTLP_ENDPOINT=https://collector.example.invalid"), nil, "OTEL_EXPORTER_OTLP_ENDPOINT"},
		{"inherited exports without the marker are the user's", CostEnv(40111, 99)[:9], nil, "CLAUDE_CODE_ENABLE_TELEMETRY"},
		{"inherited own exports but settings.json names OTEL_", ownEnv, []string{"OTEL_LOGS_EXPORTER"}, "settings.json"},
		{"marker off does not vouch for exports", []string{"PFM_COST_LEDGER=off", "OTEL_LOGS_EXPORTER=otlp"}, nil, "OTEL_LOGS_EXPORTER"},
	} {
		got := CostPreemptedReason(tc.environ, tc.settings)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: reason = %q, want it to contain %q", tc.name, got, tc.want)
		}
	}
}

func TestSettingsEnvNamesReadsTheEnvBlockKeys(t *testing.T) {
	dir := t.TempDir()
	if names, err := SettingsEnvNames(dir); err != nil || len(names) != 0 {
		t.Fatalf("missing settings.json = %v, %v; want no names, no error", names, err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"theme":"dark","env":{"OTEL_LOGS_EXPORTER":"otlp","EDITOR":"vi"}}`)
	names, err := SettingsEnvNames(dir)
	slices.Sort(names)
	if err != nil || !reflect.DeepEqual(names, []string{"EDITOR", "OTEL_LOGS_EXPORTER"}) {
		t.Fatalf("names = %v, %v", names, err)
	}
	write(`{"theme":"dark"}`)
	if names, err := SettingsEnvNames(dir); err != nil || len(names) != 0 {
		t.Fatalf("no env block = %v, %v", names, err)
	}
	write(`{broken`)
	if _, err := SettingsEnvNames(dir); err == nil {
		t.Fatal("a settings.json that cannot be parsed read as no names")
	}
}

func installFakePFM(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRenderWrapsInteractiveLaunchesWithTheCostLauncher(t *testing.T) {
	home, machine := renderMachine(t)
	pfm := filepath.Join(home, ".local", "bin", "pfm")
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher} {
		launch, err := Render(Request{Purpose: purpose, Home: home}, machine)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{pfm, "internal", "cost-launch", "--"}; !reflect.DeepEqual(launch.Wrapper, want) {
			t.Errorf("purpose %d Wrapper = %q, want %q", purpose, launch.Wrapper, want)
		}
		for _, entry := range launch.Env {
			if strings.HasPrefix(entry, "PFM_COST_LEDGER=") {
				t.Errorf("purpose %d sets %s before the wrapper decided", purpose, entry)
			}
		}
	}
	query, err := Render(Request{Purpose: PurposeQuery, Home: home}, machine)
	if err != nil {
		t.Fatal(err)
	}
	if len(query.Wrapper) != 0 ||
		slices.ContainsFunc(query.Env, func(e string) bool { return strings.HasPrefix(e, "PFM_COST_LEDGER=") }) {
		t.Errorf("a query launch carries the ledger: wrapper %q env %q", query.Wrapper, query.Env)
	}
}

func TestRenderMarksARefusedLedgerSoTheStatusLineCanSayWhy(t *testing.T) {
	// Started from inside a pfm chat: the parent's own exports point at the parent's sink and
	// must not reach a launch that carries no ledger of its own.
	for _, entry := range CostEnv(40111, 99) {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	t.Setenv("OTEL_SERVICE_NAME", "mine")
	home, machine := renderMachine(t)
	machine.Claude.CostLedger = false
	off, err := Render(Request{Purpose: PurposeInteractive, Home: home}, machine)
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Wrapper) != 0 || !slices.Contains(off.Env, "PFM_COST_LEDGER=off") {
		t.Errorf(
			"claude.costLedger=false: wrapper %q env %q, want no wrapper and PFM_COST_LEDGER=off",
			off.Wrapper,
			off.Env,
		)
	}
	bare, bareMachine := renderMachine(t)
	if err := os.Remove(filepath.Join(bare, ".local", "bin", "pfm")); err != nil {
		t.Fatal(err)
	}
	failed, err := Render(Request{Purpose: PurposeInteractive, Home: bare}, bareMachine)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed.Wrapper) != 0 || !slices.Contains(failed.Env, "PFM_COST_LEDGER=failed") {
		t.Errorf(
			"no installed pfm: wrapper %q env %q, want no wrapper and PFM_COST_LEDGER=failed",
			failed.Wrapper,
			failed.Env,
		)
	}
	for _, launch := range []Launch{off, failed} {
		for _, name := range CostEnvNames() {
			if name != EnvCostLedger && !slices.Contains(launch.Unset, name) {
				t.Errorf("env %q: the parent chat's %s is not unset", launch.Env, name)
			}
		}
		if slices.Contains(launch.Unset, "OTEL_SERVICE_NAME") {
			t.Errorf("the user's own OTEL_SERVICE_NAME is unset: %q", launch.Unset)
		}
	}
}
