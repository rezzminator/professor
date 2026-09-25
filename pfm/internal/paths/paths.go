// Package paths centralizes filesystem defaults and their test-jail overrides.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// SID-dir scratch purposes: pfm's own raw output lives in these
// subdirectories of Values.SIDDir, never in a cwd-relative tmp/.
const (
	// SIDScratchDoctor holds `pfm doctor --verbose` raw probe output.
	SIDScratchDoctor = "pfm-doctor"
	// SIDScratchChatLoads holds the transcripts `pfm chat read` extracts.
	SIDScratchChatLoads = "chat-loads"
	// SIDEffortPrefix names the statusline's per-session effort record,
	// SIDEffortPrefix+sessionID, so the doctor's crumb audit accepts it.
	SIDEffortPrefix = "statusline-effort-"
)

// SIDScratchDirs lists every SID-dir scratch purpose, so a check that
// audits the SID dir can tell pfm's own directories from rot.
func SIDScratchDirs() []string { return []string{SIDScratchDoctor, SIDScratchChatLoads} }

const (
	EnvDB          = "PFM_DB"
	EnvFleetDB     = "PFM_FLEET_DB"
	EnvSIDDir      = "PFM_SID_DIR"
	EnvClaudeRoots = "PFM_CLAUDE_ROOTS"
	EnvCodexHome   = "PFM_CODEX_ROOT"
	// EnvOpenCodeRoot jails OpenCode's data home (~/.local/share/opencode),
	// the directory holding its SQLite session store opencode.db.
	EnvOpenCodeRoot = "PFM_OPENCODE_ROOT"
	EnvTmuxDir      = "PFM_TMUX_DIR"
	EnvHome         = "PFM_HOME"
	// EnvRealHome lets the rare test that MUST see the operator's own
	// machine — building against the real module cache, probing a live
	// config — opt back in by name. Everything else running under `go
	// test` is refused the real home rather than handed it silently.
	EnvRealHome = "PFM_TEST_REAL_HOME"
	// EnvTestJailHome names the package-wide jailed home internal/testjail
	// built. A test that moves PFM_HOME to a directory of its own still
	// inherits the jail's XDG_CONFIG_HOME, which is safe: it is this home's.
	EnvTestJailHome    = "PFM_TEST_JAIL_HOME"
	EnvProcRoot        = "PFM_PROC_ROOT"
	EnvCgroupRoot      = "PFM_CGROUP_ROOT"
	EnvDevRepoGitDir   = "PFM_DEV_REPO_GIT_DIR"
	EnvDevRepoWorkTree = "PFM_DEV_REPO_WORK_TREE"
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
	defaultTmpDir    = "/tmp"
)

// TmuxConfigArguments returns the `-f <config>` a chat server is created with,
// or nothing at all so tmux loads the user's own config.
func TmuxConfigArguments() []string {
	if config := os.Getenv(EnvTmuxConf); config != "" {
		return []string{"-f", config}
	}
	return nil
}

// EnsureTmuxDir creates the private socket directory a fresh machine does not
// have yet. Tmux's explicit -S form creates the socket, but not its parent.
func EnsureTmuxDir(directory string) error {
	if strings.TrimSpace(directory) == "" {
		return fmt.Errorf("tmux socket directory is empty")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create tmux socket directory %s: %w", directory, err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect tmux socket directory %s: %w", directory, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("tmux socket directory %s is not a real directory", directory)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("secure tmux socket directory %s: %w", directory, err)
		}
	}
	return nil
}

// Values contains the filesystem locations used by pfm.
//
// DB is this binary's own derived cache (transcripts, rollouts, names) and
// nothing else reads it. FleetDB is the authoritative operator state: kills,
// teammates, and the primary account.
type Values struct {
	DB      string
	FleetDB string
	SIDDir  string
	Roots   map[pfmengine.ID][]string
	TmuxDir string
	Home    string
	// ArchiveDir is ~/.claude-archive: where archived transcripts and rollouts
	// go, with the manifest that puts them back. It is defined relative to Home.
	ArchiveDir string
	ProcRoot   string
	CgroupRoot string
	// LogFile is the home's activity log, the JSON-lines file internal/obs
	// writes and `pfm log` reads. It hangs off the same pfm state directory
	// as the fleet cache, so a jail, a fence and the live host each keep
	// their own and can never mix.
	LogFile string
}

// EnvOr returns a non-empty environment override, or fallback otherwise.
func EnvOr(name, fallback string) string {
	return EnvOrFrom(OSEnv{}, name, fallback)
}

// EnvOrFrom is EnvOr expressed over an injected Env — the seam a host
// fixture, or a future paths caller, composes against instead of the live
// process environment. EnvOr itself is exactly this over OSEnv, so the two
// can never drift.
func EnvOrFrom(env Env, name, fallback string) string {
	if value := env.Get(name); value != "" {
		return value
	}
	return fallback
}

// DevRepoGitDir returns the fence-mounted git directory when root is the
// corresponding mounted worktree.
func DevRepoGitDir(root string) (string, bool) {
	workTree := strings.TrimSpace(os.Getenv(EnvDevRepoWorkTree))
	gitDir := strings.TrimSpace(os.Getenv(EnvDevRepoGitDir))
	if gitDir == "" {
		return "", false
	}
	cleanWorkTree := filepath.Clean(workTree)
	cleanRoot := filepath.Clean(root)
	physicalWorkTree, workTreeErr := filepath.EvalSymlinks(cleanWorkTree)
	physicalRoot, rootErr := filepath.EvalSymlinks(cleanRoot)
	if workTreeErr == nil && rootErr == nil {
		return gitDir, filepath.Clean(physicalWorkTree) == filepath.Clean(physicalRoot)
	}
	if workTreeErr != nil {
		fmt.Fprintf(
			os.Stderr,
			"pfm: resolve fence worktree %s: %v; falling back to cleaned path comparison\n",
			cleanWorkTree,
			workTreeErr,
		)
	}
	if rootErr != nil {
		fmt.Fprintf(
			os.Stderr,
			"pfm: resolve blueprint root %s: %v; falling back to cleaned path comparison\n",
			cleanRoot,
			rootErr,
		)
	}
	return gitDir, cleanWorkTree == cleanRoot
}

// Home resolves the operator home every pfm path hangs from: the PFM_HOME
// jail override first, then the OS home — except inside a test that never
// set up its jail, which is refused.
//
// A test that never set up its jail would otherwise resolve to the
// OPERATOR'S OWN home: the fleet.db their live chats are indexed in,
// the ~/.claude/projects their transcripts live in. That is not a
// hypothetical — one `go test ./...` run outside the fence has written
// fixture transcripts into a real account and held write transactions
// on a real fleet.db until the TUI could no longer open it.
//
// Resolving is silent by design: it computes pathnames and touches
// nothing, so a jailed run and an escaped one are byte-identical here
// and stay indistinguishable until something WRITES. This is the last
// place the difference is visible, so a missing jail is an error here
// rather than a surprise several layers down.
func Home() (string, error) {
	return HomeFrom(OSEnv{})
}

// HomeFrom is Home expressed over an injected Env — used by hostfixture and
// a future paths caller. The testing.Testing() refusal to resolve a real
// operator home from inside a test applies unconditionally: it is what
// makes a MapEnv-backed fixture and the live process behave identically
// under `go test`, never one safe and the other silently reading the
// machine.
func HomeFrom(env Env) (string, error) {
	if home := env.Get(EnvHome); home != "" {
		return home, nil
	}
	if testing.Testing() && env.Get(EnvRealHome) == "" {
		return "", fmt.Errorf(
			"refusing to resolve the operator's real home directory inside a test: "+
				"point %s at a temporary directory (see internal/testjail), or set %s=1 "+
				"if this test genuinely must read the host",
			EnvHome, EnvRealHome,
		)
	}
	home, err := env.Home()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return home, nil
}

// ConfigHomeFrom resolves the XDG config-home root pfm's own on-disk state
// hangs from: an absolute XDG_CONFIG_HOME wins, else home's own .config
// subdirectory. internal/config's ResolvePath composes pfm's config.json
// path under this same root — the single place it is computed, so a config
// resolver and a jail's own pin (internal/testjail) can never drift about
// which .config a caller meant (L3-F9).
func ConfigHomeFrom(env Env, home string) string {
	if root := env.Get("XDG_CONFIG_HOME"); filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return filepath.Join(home, ".config")
}

// ConfigHome is ConfigHomeFrom over the real process environment.
func ConfigHome(home string) string { return ConfigHomeFrom(OSEnv{}, home) }

// Resolve returns the standard host paths with all K4 test-jail overrides
// applied. It only computes pathnames; it does not access the filesystem.
func Resolve() (Values, error) {
	home, err := Home()
	if err != nil {
		return Values{}, err
	}

	roots := make(map[pfmengine.ID][]string, len(pfmengine.All()))
	for _, id := range pfmengine.All() {
		descriptor := pfmengine.MustLookup(id)
		if value := os.Getenv(descriptor.RootEnv); value != "" {
			roots[id] = filepath.SplitList(value)
		} else {
			roots[id] = descriptor.DefaultRoots(home)
		}
	}

	tmuxBase := EnvOr("TMUX_TMPDIR", defaultTmpDir)

	return Values{
		DB: EnvOr(EnvDB, filepath.Join(home, ".local", "state", "pfm", "fleet.db")),
		// The fleet database defaults to $HOME/.cc/fleet.db. PFM_DB already
		// overrides the private cache, so the fleet handle gets a distinct name.
		FleetDB:    EnvOr(EnvFleetDB, filepath.Join(home, ".cc", "fleet.db")),
		SIDDir:     EnvOr(EnvSIDDir, filepath.Join(defaultTmpDir, "cc-sid")),
		Roots:      roots,
		TmuxDir:    EnvOr(EnvTmuxDir, filepath.Join(tmuxBase, "tmux-"+strconv.Itoa(os.Getuid()))),
		Home:       home,
		ArchiveDir: filepath.Join(home, ".claude-archive"),
		LogFile:    filepath.Join(home, ".local", "state", "pfm", "log", "pfm.jsonl"),
		ProcRoot:   EnvOr(EnvProcRoot, "/proc"),
		CgroupRoot: EnvOr(EnvCgroupRoot, "/sys/fs/cgroup"),
	}, nil
}

// GeneratedCodexAgentsDir is where pfm compiles the machine-global Codex
// agent .toml twins (codexgen.RunGlobalAgents) — pfm-owned state, never
// inside the source clone a template's .md lives in, so a stale compiler on
// any host can never rewrite a tracked file. A `~/.codex/agents/<name>.toml`
// link points here; uninstall removes this whole directory along with the
// links it owns.
func LegacyGeneratedCodexAgentsDir(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "generated", "codex-agents")
}

// GeneratedClaudeAgentsDir is where pfm renders the machine-global Claude
// agent VARIANTS templates/global/agents/variants.json declares
// (codexgen.RunGlobalAgents) — one source agent re-emitted under another name
// with overridden frontmatter. An original agent is linked straight from the
// clone and never lands here; only a variant's `{config}/agents/<name>.md`
// link points into this directory, and uninstall removes it with those links.
func GeneratedClaudeAgentsDir(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "generated", "claude-agents")
}

// HarnessPromptsDir is where `pfm install` stages the fleet's system-prompt
// layer: one composed prompt per engine, beside the parts it was composed
// from and the Claude drift baselines.
func HarnessPromptsDir(home string) string {
	return filepath.Join(home, ".local", "share", "pfm", "install", "harness-prompts")
}

// HarnessPromptPath is the staged prompt ONE engine reads — claude.md,
// codex.md, opencode.md — composed at stage time from the shared head, that
// engine's middle and the shared tail. Claude takes it as
// --system-prompt-file, Codex as its SessionStart appendix, OpenCode through
// its config's `instructions` array. Three readers in three packages; one
// spelling of where the file is.
func HarnessPromptPath(home string, id pfmengine.ID) string {
	return filepath.Join(HarnessPromptsDir(home), pfmengine.MustLookup(id).LongName+".md")
}

// HarnessBaselineDir is the one staged location the harness-prompt drift
// doctor reads its pins, bodies and model provenance from. The baselines are
// captures of Claude Code's own built-in prompt, so they live under the
// Claude engine's directory rather than beside the shared parts.
func HarnessBaselineDir(home string) string {
	return filepath.Join(HarnessPromptsDir(home), pfmengine.MustLookup(pfmengine.Claude).LongName, "baselines")
}

// SocketPath resolves a chat's tmux socket to an absolute path: an absolute
// socket is returned unchanged, a bare name resolves under the private tmux
// directory. It lives here because both cmd/pfm and internal/headless need it
// and TmuxDir's resolution — including its jail override — is this package's
// to change. A second copy would keep working right up until that resolution
// moves, then fail in whichever copy nobody remembered.
func SocketPath(socket string) (string, error) {
	if filepath.IsAbs(socket) {
		return socket, nil
	}
	resolved, err := Resolve()
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved.TmuxDir, socket), nil
}

// SocketUnder is the path of the tmux socket named socket under values'
// TmuxDir. socket must be one bare name — an absolute path, a nested path or a
// ".." that would dial a server outside the tmux directory is refused.
func (values Values) SocketUnder(socket string) (string, error) {
	if values.TmuxDir == "" {
		return "", fmt.Errorf("tmux directory is empty")
	}
	if socket == "" || socket == "." || socket == ".." || filepath.IsAbs(socket) || filepath.Base(socket) != socket {
		return "", fmt.Errorf("socket %q must be one relative tmux socket name", socket)
	}
	return filepath.Join(values.TmuxDir, socket), nil
}

// FirstRoot is the engine's first configured root, or "" with none — the root
// a single-root consumer such as recovery or heal reads.
func (values Values) FirstRoot(id pfmengine.ID) string {
	if roots := values.Roots[id]; len(roots) != 0 {
		return roots[0]
	}
	return ""
}
