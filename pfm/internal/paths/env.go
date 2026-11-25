package paths

import (
	"os"
	"os/user"
	"strings"
)

// Env abstracts a host's environment and identity reads — os.Getenv/
// LookupEnv/UserHomeDir/user.Current/os.Hostname — the seam every PFM_*
// override and host probe crosses instead of calling the os package
// directly (the unit-test law's three seams, item 3). OSEnv is the identity implementation EnvOr and Home
// already ran against before this existed; MapEnv is its in-memory twin for
// tests and hostfixture.
type Env interface {
	// Get returns name's value, or "" when unset — os.Getenv's contract.
	Get(name string) string
	// Lookup returns name's value and whether it was set at all —
	// os.LookupEnv's contract, for a caller that must tell "unset" apart
	// from "set to the empty string".
	Lookup(name string) (string, bool)
	// Home resolves the operator's home directory — os.UserHomeDir's
	// contract.
	Home() (string, error)
	// Hostname resolves the host's name — os.Hostname's contract.
	Hostname() (string, error)
	// User resolves the current OS username — user.Current's contract,
	// narrowed to the one field pfm ever reads from it.
	User() (string, error)
}

// OSEnv is Env over the real process: os.Getenv, os.LookupEnv,
// os.UserHomeDir, os.Hostname and user.Current.
type OSEnv struct{}

func (OSEnv) Get(name string) string            { return os.Getenv(name) }
func (OSEnv) Lookup(name string) (string, bool) { return os.LookupEnv(name) }
func (OSEnv) Home() (string, error)             { return os.UserHomeDir() }
func (OSEnv) Hostname() (string, error)         { return os.Hostname() }

func (OSEnv) User() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", err
	}
	return current.Username, nil
}

// MapEnv is an in-memory Env for tests and hostfixture: Values seeds
// Get/Lookup; HomeDir/HomeErr, HostnameValue/HostnameErr and
// Username/UserErr seed the three identity probes, each answering its
// configured error when a fixture wants that probe to fail (hostfixture's
// NoHome leaves HomeDir "" with a HomeErr set).
type MapEnv struct {
	Values map[string]string

	HomeDir string
	HomeErr error

	HostnameValue string
	HostnameErr   error

	Username string
	UserErr  error
}

func (env *MapEnv) Get(name string) string {
	if env.Values == nil {
		return ""
	}
	return env.Values[name]
}

func (env *MapEnv) Lookup(name string) (string, bool) {
	if env.Values == nil {
		return "", false
	}
	value, ok := env.Values[name]
	return value, ok
}

func (env *MapEnv) Home() (string, error) {
	if env.HomeErr != nil {
		return "", env.HomeErr
	}
	return env.HomeDir, nil
}

func (env *MapEnv) Hostname() (string, error) {
	if env.HostnameErr != nil {
		return "", env.HostnameErr
	}
	return env.HostnameValue, nil
}

func (env *MapEnv) User() (string, error) {
	if env.UserErr != nil {
		return "", env.UserErr
	}
	return env.Username, nil
}

// The names of the variables pfm reads.
const (
	EnvConfig  = "PFM_CONFIG"
	EnvCacheDB = "PFM_CACHE_DB"
	EnvStateDB = "PFM_STATE_DB"
	EnvSIDDir  = "PFM_SID_DIR"
	// EnvMCPHandoverChild=1 marks the installed pfm a replaced stdio MCP
	// server handed its chat to (mcpserv.Handover): it serves and never hands
	// over itself, the server that started it moving it on the next install.
	EnvMCPHandoverChild = "PFM_MCP_HANDOVER_CHILD"
	// EnvSeatHostEnd carries an engine's end into the installed pfm a replaced
	// seat-host re-execs (hookentry.resumeSeatHostEnd): the engine's exit status,
	// ":faulted" appended when its run failed; that image closes the seat, or
	// starts the reload it awaits, never the ended engine again.
	EnvSeatHostEnd = "PFM_SEAT_HOST_END"
	EnvClaudeRoots = "PFM_CLAUDE_ROOTS"
	EnvCodexHome   = "PFM_CODEX_ROOT"
	// EnvOpenCodeRoot jails OpenCode's data home (~/.local/share/opencode),
	// the directory holding its SQLite session store opencode.db.
	EnvOpenCodeRoot = "PFM_OPENCODE_ROOT"
	EnvTmuxDir      = "PFM_TMUX_DIR"
	EnvHome         = "PFM_HOME"
	// EnvDevFence=1 marks a process the fence (infra/fence) started; with
	// /.dockerenv it is how testjail admits a test run.
	EnvDevFence = "PFM_DEV_FENCE"
	// EnvCostLedgerDir names the directory of the per-call cost ledger
	// ({session_id}.jsonl, one line per API call) the cost sink writes and the
	// status line, `pfm tokens` and callmeter read; unset, it hangs off the
	// pfm state directory (CostLedgerDir).
	EnvCostLedgerDir = "PFM_COST_LEDGER_DIR"
	// EnvWatchSnapshot names the fleet snapshot `pfm chat watch` processes
	// share (WatchSnapshot); unset, it sits beside the fleet cache.
	EnvWatchSnapshot = "PFM_WATCH_SNAPSHOT"
	// EnvRealHome lets the rare test that MUST see the operator's own
	// machine — building against the real module cache, probing a live
	// config — opt back in by name. Everything else running under `go
	// test` is refused the real home rather than handed it silently.
	EnvRealHome             = "PFM_TEST_REAL_HOME"
	EnvTestPFMBinary        = "PFM_TEST_PFM_BINARY"
	EnvTestMockEngineBinary = "PFM_TEST_MOCK_ENGINE_BINARY"
	EnvProcRoot             = "PFM_PROC_ROOT"
	EnvManagedSettingsDir   = "PFM_MANAGED_SETTINGS_DIR"
	EnvCgroupRoot           = "PFM_CGROUP_ROOT"
	EnvDevRepoGitDir        = "PFM_DEV_REPO_GIT_DIR"
	EnvDevRepoWorkTree      = "PFM_DEV_REPO_WORK_TREE"
	// EnvTestArtifactDir names the directory every Go test process writes its
	// profile under (internal/testjail); unset, profiling writes nothing and a
	// red run says so once on stderr. EnvTestProfile=0 turns profiling off;
	// EnvTestProfile=cpu adds a CPU profile, opt-in only: its SIGPROF interval
	// timer survives execve, so a test whose child execs another program (zsh, a
	// re-exec'd pfm) sees that child killed by "profiling timer expired".
	// EnvTestProfileParent is exported by the first profiled process: a test
	// binary inheriting it is a helper, recorded but never a failure bundle,
	// since helpers exit non-zero on purpose. EnvTestDeadlineEpoch is the step
	// deadline in decimal epoch seconds, which the profiler's watchdog fires
	// before.
	EnvTestArtifactDir   = "PFM_TEST_ARTIFACT_DIR"
	EnvTestProfile       = "PFM_TEST_PROFILE"
	EnvTestProfileParent = "PFM_TEST_PROFILE_PARENT"
	EnvTestDeadlineEpoch = "PFM_TEST_DEADLINE_EPOCH"
	// EnvTmuxConf pins the config a chat's tmux server is born with. Unset —
	// the way a real chat runs — the server loads ~/.tmux.conf like every other
	// terminal on the machine, because a chat IS a terminal the user lives in:
	// one that ignores their config wears tmux's default green status bar at
	// the bottom while every other window wears theirs on top. Jails set it to
	// /dev/null so a machine's real config can never steer a fixture.
	EnvTmuxConf = "PFM_TMUX_CONF"
	// EnvLogLevel overrides the activity log's level for one run,
	// EnvLogComponents (`mcp=debug,db=off`) its per-component levels, and
	// EnvLogMirror set to "stderr" mirrors every record onto stderr for a
	// foreground run (internal/obs).
	EnvLogLevel      = "PFM_LOG_LEVEL"
	EnvLogComponents = "PFM_LOG_COMPONENTS"
	EnvLogMirror     = "PFM_LOG"
	// EnvAutoEffortStateFile is the auto-effort plugin's own variable naming
	// its per-session state file (AutoEffortStateFile); pfm only reads it.
	EnvAutoEffortStateFile = "AUTO_EFFORT_STATE_FILE"
	// EnvFormerAutoEffortStateFile is the variable under the plugin's former
	// name, which the plugin reads in place of an unset EnvAutoEffortStateFile.
	EnvFormerAutoEffortStateFile = "AUTO_EFFORT_CONTROL_STATE_FILE"
)

// ApplyEnv is environ with every name in unset and every name an assignment
// sets dropped, then assignments: an exec'd process reads the first of two
// copies of a name, so an inherited copy never shadows an assignment.
func ApplyEnv(environ, unset, assignments []string) []string {
	dropped := make(map[string]bool, len(unset)+len(assignments))
	for _, name := range unset {
		dropped[name] = true
	}
	for _, assignment := range assignments {
		name, _, _ := strings.Cut(assignment, "=")
		dropped[name] = true
	}
	result := make([]string, 0, len(environ)+len(assignments))
	for _, entry := range environ {
		if name, _, _ := strings.Cut(entry, "="); !dropped[name] {
			result = append(result, entry)
		}
	}
	return append(result, assignments...)
}
