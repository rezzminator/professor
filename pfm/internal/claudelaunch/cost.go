package claudelaunch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The cost ledger rides on Claude Code's OpenTelemetry exporters, pointed at a sink on 127.0.0.1
// that the wrapper `pfm internal cost-launch` binds just before it execs Claude. The registry owns
// every name and value: nothing here sets OTEL_LOG_USER_PROMPTS, OTEL_LOG_TOOL_DETAILS or
// OTEL_LOG_RAW_API_BODIES, so no prompt, tool detail or API body is ever exported.
const (
	knobCostLedger = "costLedger"
	// EnvCostLedger is the launch's own record of what the ledger decided; the status line reads it.
	EnvCostLedger = "PFM_COST_LEDGER"
	// EnvCostLedgerPID names the process the armed sink serves: a launch that reaches the wrapper
	// again in that same process (exec keeps the pid) is already armed.
	EnvCostLedgerPID = "PFM_COST_LEDGER_PID"
	// CostLedgerOn: a sink was armed for this launch. Off: claude.costLedger is false. Preempted:
	// the user's own telemetry settings were kept and no ledger runs. Failed: the sink could not be
	// armed (or pfm itself is not installed where the wrapper must run).
	CostLedgerOn        = "on"
	CostLedgerOff       = "off"
	CostLedgerPreempted = "preempted"
	CostLedgerFailed    = "failed"

	envEnableTelemetry   = "CLAUDE_CODE_ENABLE_TELEMETRY"
	envEnhancedTelemetry = "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"
	envOtelEndpoint      = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otelPrefix           = "OTEL_"
	costPortToken        = "{port}"
	costLoopback         = "http://127.0.0.1:"
)

type costVar struct{ name, value string }

// costVars are the exporter variables of a ledger launch, in the order they are set. The endpoint's
// port is the one value known only at exec time.
var costVars = []costVar{
	{envEnableTelemetry, "1"},
	{"OTEL_LOGS_EXPORTER", "otlp"},
	{"OTEL_TRACES_EXPORTER", "otlp"},
	{envEnhancedTelemetry, "1"},
	{"OTEL_METRICS_EXPORTER", "none"},
	{"OTEL_EXPORTER_OTLP_PROTOCOL", "http/json"},
	{envOtelEndpoint, costLoopback + costPortToken},
	{"OTEL_LOGS_EXPORT_INTERVAL", "1000"},
	{"OTEL_BSP_SCHEDULE_DELAY", "1000"},
}

var ownEndpoint = regexp.MustCompile(`^` + regexp.QuoteMeta(costLoopback) + `[0-9]+$`)

// CostEnv is the environment of a launch whose sink listens on 127.0.0.1:port and serves process
// pid: every exporter variable, the marker (on) and the pid.
func CostEnv(port, pid int) []string {
	env := make([]string, 0, len(costVars)+2)
	for _, v := range costVars {
		env = append(env, v.name+"="+strings.ReplaceAll(v.value, costPortToken, strconv.Itoa(port)))
	}
	return append(env, EnvCostLedger+"="+CostLedgerOn, EnvCostLedgerPID+"="+strconv.Itoa(pid))
}

// CostEnvNames names every variable CostEnv sets.
func CostEnvNames() []string {
	names := make([]string, 0, len(costVars)+2)
	for _, v := range costVars {
		names = append(names, v.name)
	}
	return append(names, EnvCostLedger, EnvCostLedgerPID)
}

// inheritedCost splits environ's telemetry variables into pfm's own, vouched for by the marker
// PFM_COST_LEDGER=on and a value CostEnv sets, and the user's; marker is the marker's value.
func inheritedCost(environ []string) (own, user []string, marker string) {
	values := map[string]string{}
	for _, entry := range environ {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}
	marker = values[EnvCostLedger]
	want := map[string]string{}
	for _, v := range costVars {
		want[v.name] = v.value
	}
	for _, entry := range environ {
		name, value, _ := strings.Cut(entry, "=")
		expected, ours := want[name]
		if !ours && !namesTelemetry(name) {
			continue
		}
		known := ours && (value == expected || (name == envOtelEndpoint && ownEndpoint.MatchString(value)))
		switch {
		case marker == CostLedgerOn && known:
			own = append(own, name)
		case namesTelemetry(name):
			user = append(user, name)
		}
	}
	return own, user, marker
}

// InheritedCostNames names what a launch inherited from a pfm chat's own cost ledger: the exports
// vouched for by the marker on, the marker and the pid. A launch that arms no sink of its own
// drops them, or its Claude would export to the parent's sink.
func InheritedCostNames(environ []string) []string {
	own, _, marker := inheritedCost(environ)
	if marker != CostLedgerOn {
		return nil
	}
	return append(own, EnvCostLedger, EnvCostLedgerPID)
}

// CostArmedFor reports whether environ is a ledger launch already armed for process pid: the
// marker on, the pid pid and every exporter variable at pfm's own value.
func CostArmedFor(environ []string, pid int) bool {
	if !slices.Contains(environ, EnvCostLedger+"="+CostLedgerOn) ||
		!slices.Contains(environ, EnvCostLedgerPID+"="+strconv.Itoa(pid)) {
		return false
	}
	values := map[string]string{}
	for _, entry := range environ {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}
	for _, v := range costVars {
		value, set := values[v.name]
		if !set || (value != v.value && (v.name != envOtelEndpoint || !ownEndpoint.MatchString(value))) {
			return false
		}
	}
	return true
}

// CostWrapper is the argv words that run in front of Claude: pfm's own binary at its installed
// path, the same one the hooks and the status line run.
func CostWrapper(home string) []string {
	return []string{filepath.Join(home, ".local", "bin", "pfm"), "internal", "cost-launch", "--"}
}

func namesTelemetry(name string) bool {
	return name == envEnableTelemetry || strings.HasPrefix(name, otelPrefix)
}

// CostPreemptedReason says why a launch must keep the user's own telemetry and run without a
// ledger, or "" when it may arm one. The process environment (environ, NAME=value entries) or the
// account's settings.json env (settingsEnv, its key names) naming CLAUDE_CODE_ENABLE_TELEMETRY or
// any OTEL_ variable pre-empts it. One exception: the inherited marker PFM_COST_LEDGER=on together
// with exports that are exactly pfm's own (a chat started from inside a pfm chat) vouches that
// they are not the user's.
func CostPreemptedReason(environ, settingsEnv []string) string {
	_, user, _ := inheritedCost(environ)
	if len(user) > 0 {
		return "the launch environment already sets " + strings.Join(user, ", ")
	}
	var fromSettings []string
	for _, name := range settingsEnv {
		if namesTelemetry(name) {
			fromSettings = append(fromSettings, name)
		}
	}
	if len(fromSettings) > 0 {
		return "the account's settings.json env already sets " + strings.Join(fromSettings, ", ")
	}
	return ""
}

// SettingsEnvNames lists the keys of the "env" block of settingsDir/settings.json: none for an
// absent, empty or whitespace-only file or an absent block, an error for a file that cannot be
// read or parsed (its real state is unknown, which a caller must not read as "names nothing").
func SettingsEnvNames(settingsDir string) ([]string, error) {
	path := filepath.Join(settingsDir, "settings.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, nil
	}
	var settings struct {
		Env map[string]json.RawMessage `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	names := make([]string, 0, len(settings.Env))
	for name := range settings.Env {
		names = append(names, name)
	}
	return names, nil
}

// costLedgerPlan decides how a rendered launch carries the ledger: wrapped when the cost launcher
// is installed, marked off when claude.costLedger is false, marked failed when pfm's own binary is
// missing (the status line then says so instead of staying silent). A query carries nothing.
func costLedgerPlan(request Request, enabled bool, result *Launch) {
	if request.Purpose == PurposeQuery {
		return
	}
	if !enabled {
		result.Unset = append(result.Unset, InheritedCostNames(os.Environ())...)
		result.Env = append(result.Env, EnvCostLedger+"="+CostLedgerOff)
		return
	}
	wrapper := CostWrapper(request.Home)
	info, err := os.Stat(wrapper[0])
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		obs.Logger(context.Background()).Warn(
			"claudelaunch: the cost launcher is not installed; launching without a cost ledger",
			"path", wrapper[0], obs.FieldErr, fmt.Sprint(err),
		)
		result.Unset = append(result.Unset, InheritedCostNames(os.Environ())...)
		result.Env = append(result.Env, EnvCostLedger+"="+CostLedgerFailed)
		return
	}
	result.Wrapper = wrapper
}
