package kill

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"hostops/pfm/internal/clock"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	fleetindex "hostops/pfm/internal/index"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
)

// New constructs a manager from the already-open fleet store.
func New(database *store.Store, dependencies Dependencies) (*Manager, error) {
	if database == nil {
		return nil, errors.New("kill store is nil")
	}
	resolved := dependencies.Paths
	if resolved.Home == "" {
		var err error
		resolved, err = paths.Resolve()
		if err != nil {
			return nil, fmt.Errorf("resolve kill paths: %w", err)
		}
	}
	proc := dependencies.ProcFS
	if proc == nil {
		proc = gather.NewProcFS(resolved.ProcRoot)
	}
	tmux := dependencies.Tmux
	if tmux == nil {
		tmux = TmuxKiller{}
	}
	now := dependencies.Now
	if now == nil {
		now = clock.Real.Now
	}
	spawner := dependencies.Spawner
	if spawner == nil {
		spawner = CommandSpawner{
			Executable: dependencies.Executable,
			ConfigPath: dependencies.ConfigPath,
		}
	}
	codexHomes := dependencies.CodexHomes
	if codexHomes == nil {
		codexHomes = append([]string(nil), resolved.Roots[pfmengine.Codex]...)
	} else {
		codexHomes = append([]string{}, codexHomes...)
	}
	return &Manager{
		database: database,
		proc:     proc,
		tmux:     tmux,
		spawner:  spawner,
		now:      now,
		paths: resolvedPaths{
			home:       resolved.Home,
			sidDir:     resolved.SIDDir,
			codexHomes: codexHomes,
			tmuxDir:    resolved.TmuxDir,
		},
	}, nil
}

// Environment reads the three caller values used by --self, through env
// (pfm/TESTPLAN.md § Seams, paths.Env); nil reads the real process
// environment.
func Environment(env paths.Env) SelfEnvironment {
	if env == nil {
		env = paths.OSEnv{}
	}
	return SelfEnvironment{
		TMUX:            env.Get("TMUX"),
		TMUXPane:        env.Get("TMUX_PANE"),
		ClaudeSessionID: env.Get("CLAUDE_CODE_SESSION_ID"),
	}
}

// Kill records a permanent kill and starts the detached exit finisher
// whenever the target resolves to a live tmux pane — hiding a live chat
// always ends it, because a hidden row left running is a chat nobody can see
// but that still holds its pane, its socket, and its viewport tab. Exit is
// the explicit form of the same choreography for a target this call cannot
// see is live on its own (a resolved socket/pane the caller already vouches
// for): it still errors when no live address resolves at all. The kill lifts
// only on an explicit unkill, so no prompt baseline is recorded.
func (manager *Manager) Kill(
	ctx context.Context,
	request Request,
) (Target, error) {
	var target Target
	var err error
	switch {
	case request.Self:
		target, err = manager.IdentifySelf(ctx, request.Environment)
	case request.ID != "":
		target, err = manager.lookupTarget(ctx, request.ID, request.Engine, request.RolloutPath)
	default:
		err = errors.New("kill requires --self or an id")
	}
	if err != nil {
		return Target{}, err
	}
	if request.SocketName != "" || request.PaneID != "" {
		if request.SocketName == "" || request.PaneID == "" {
			return Target{}, errors.New("resolved live address requires socket and pane")
		}
		if filepath.Base(request.SocketName) != request.SocketName {
			return Target{}, errors.New("resolved socket name must not contain a path")
		}
		target.SocketName = request.SocketName
		target.SocketPath = filepath.Join(manager.paths.tmuxDir, request.SocketName)
		target.PaneID = request.PaneID
	}
	if request.Exit && (target.SocketPath == "" || target.PaneID == "") {
		return Target{}, errors.New("--exit requires a live --self tmux pane")
	}
	live := target.SocketPath != "" && target.PaneID != ""

	if err := manager.database.Kill(ctx, store.Killed{
		ID:       target.ID,
		Engine:   target.Engine,
		KilledAt: manager.now().Unix(),
	}); err != nil {
		return Target{}, err
	}

	if request.Exit || live {
		if err := manager.spawner.Spawn(ctx, ExitArgs(target)); err != nil {
			return Target{}, err
		}
	}
	return target, nil
}

// KillCleared records a prompt-baseline kill only when id is already an
// indexed Claude fleet chat. The SessionEnd hook supplies the id directly;
// no tmux or cwd guess may turn an ordinary bare Claude session into a fleet
// kill.
func (manager *Manager) KillCleared(
	ctx context.Context,
	id string,
) (Target, bool, error) {
	if id == "" {
		return Target{}, false, nil
	}
	transcript, found, err := manager.database.Transcript(ctx, id)
	if err != nil || !found {
		return Target{}, false, err
	}
	baseline := transcript.PromptCount
	if err := manager.database.Kill(ctx, store.Killed{
		ID: id, Engine: pfmengine.Claude, KilledAt: manager.now().Unix(),
		BaselinePrompts: &baseline,
	}); err != nil {
		return Target{}, false, err
	}
	return Target{
		Engine: pfmengine.Claude, ID: id, DataPath: transcript.Path,
	}, true, nil
}

// CodexPaneBinding returns the last Codex thread observed in one immutable
// tmux pane. Codex starts a new thread in the same pane for /clear, so this is
// the only unambiguous way to identify the pane's CURRENT thread without
// guessing from a shared cwd or a process birth time that never moves —
// AdvanceCodexPane is the sole writer, kept current by
// fleet.ReconcileCodexPanes on every gather pass.
func (manager *Manager) CodexPaneBinding(
	ctx context.Context,
	socket, pane string,
) (string, bool, error) {
	key, ok := codexPaneBindingKey(socket, pane)
	if !ok {
		return "", false, nil
	}
	return manager.database.Meta(ctx, key)
}

// CodexPaneBound adapts CodexPaneBinding to store.CodexPaneBound's shape over
// ctx, for wiring a rollout-less live-process resolver's bound-pane priority
// (store.NewCodexThreadResolverRoots). It swallows a genuine store error into
// "no binding" rather than failing the live-scan identity resolve outright —
// the same "an unresolved id is not a failure" tradeoff resolve.CodexThreadID's
// own fallback chain already makes for every other rung; a store outage still
// surfaces loudly through every OTHER read the caller depends on in the same
// pass.
func (manager *Manager) CodexPaneBound(ctx context.Context) store.CodexPaneBound {
	return func(socket, pane string) (string, bool) {
		id, found, err := manager.CodexPaneBinding(ctx, socket, pane)
		if err != nil {
			return "", false
		}
		return id, found
	}
}

// AdvanceCodexPane moves one live Codex pane's binding to threadID and
// reports what it was bound to before this write. Unlike the write-once
// SeedCodexPane this replaces, the binding MOVES on every pass: the pane's
// status line is the only thing that knows a /clear happened (#1), and a
// changed binding — previous non-empty, new different — IS that signal
// (fleet.ReconcileCodexPanes, #3). changed is false only when the pane's
// observed thread already matches the stored binding.
func (manager *Manager) AdvanceCodexPane(
	ctx context.Context,
	socket, pane, threadID string,
) (previous string, changed bool, err error) {
	key, ok := codexPaneBindingKey(socket, pane)
	if !ok || threadID == "" {
		return "", false, nil
	}
	current, found, err := manager.database.Meta(ctx, key)
	if err != nil {
		return "", false, fmt.Errorf("read Codex pane binding: %w", err)
	}
	if found && current == threadID {
		return current, false, nil
	}
	if err := manager.database.SetMeta(ctx, key, threadID); err != nil {
		return "", false, fmt.Errorf("advance Codex pane binding: %w", err)
	}
	return current, true, nil
}

// KillClearedCodex records a prompt-baseline kill on the visible lineage root
// for an already indexed Codex thread. It never guesses an id: the caller
// (fleet.ReconcileCodexPanes) supplies the previous pane binding. Retirement
// must succeed before that binding advances, so failures remain retryable.
func (manager *Manager) KillClearedCodex(
	ctx context.Context,
	id string,
) (Target, bool, error) {
	if id == "" {
		return Target{}, false, nil
	}
	if err := fleetindex.RefreshCodexLineage(ctx, manager.database, id); err != nil {
		return Target{}, false, err
	}
	lineage, found, err := manager.database.CodexLineage(ctx, id)
	if err != nil || !found {
		return Target{}, false, err
	}
	baseline := lineage.PromptCount
	if err := manager.database.Kill(ctx, store.Killed{
		ID: lineage.RootID, Engine: pfmengine.Codex, KilledAt: manager.now().Unix(),
		BaselinePrompts: &baseline,
	}); err != nil {
		return Target{}, false, err
	}
	return Target{
		Engine: pfmengine.Codex, ID: lineage.RootID, DataPath: lineage.Newest.Path,
	}, true, nil
}

// ForgetCodexPane erases one pane's binding.
//
// It exists for the one binding that cannot possibly be true: a pane bound to
// a thread a /clear already retired. That state never heals on its own,
// because from then on the pane shows a NAME and a name is not allowed to move
// a binding. Erasing it returns the pane to "unbound", which is honest — pfm
// genuinely does not know what that pane runs — and lets the next observation
// of its own screen re-seat it.
func (manager *Manager) ForgetCodexPane(ctx context.Context, socket, pane string) error {
	key, ok := codexPaneBindingKey(socket, pane)
	if !ok {
		return nil
	}
	if err := manager.database.DeleteMeta(ctx, key); err != nil {
		return fmt.Errorf("forget Codex pane binding: %w", err)
	}
	return nil
}

// CodexPaneBinding is one pane binding decoded back to the address it names.
type CodexPaneBinding struct {
	Socket   string
	PaneID   string
	ThreadID string
}

// CodexPaneBindings returns every Codex pane binding the store holds.
//
// One binding read alone can never look wrong — it is just a string. The
// defects this table exists to expose are RELATIONS between bindings: two
// panes pointing at one thread, or a pane pointing at a thread a clear already
// retired. Auditing that needs the whole family at once, which is why this
// exists beside the single-key CodexPaneBinding rather than inside it.
//
// A key that will not decode is returned with an empty Socket rather than
// dropped: a binding pfm cannot address is a defect to report, not a row to
// quietly lose.
func (manager *Manager) CodexPaneBindings(ctx context.Context) ([]CodexPaneBinding, error) {
	values, err := manager.database.MetaPrefix(ctx, codexPaneBindingPrefix)
	if err != nil {
		return nil, fmt.Errorf("read Codex pane bindings: %w", err)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	bindings := make([]CodexPaneBinding, 0, len(keys))
	for _, key := range keys {
		socket, pane := decodeCodexPaneBindingKey(key)
		bindings = append(bindings, CodexPaneBinding{
			Socket: socket, PaneID: pane, ThreadID: values[key],
		})
	}
	return bindings, nil
}

func decodeCodexPaneBindingKey(key string) (socket, pane string) {
	address, err := base64.RawURLEncoding.DecodeString(
		strings.TrimPrefix(key, codexPaneBindingPrefix),
	)
	if err != nil {
		return "", ""
	}
	socket, pane, found := strings.Cut(string(address), "\x00")
	if !found {
		return "", ""
	}
	return socket, pane
}

const codexPaneBindingPrefix = "codex_clear_pane_"

func codexPaneBindingKey(socket, pane string) (string, bool) {
	socket = filepath.Base(strings.TrimSpace(socket))
	pane = strings.TrimSpace(pane)
	if socket == "" || socket == "." || pane == "" {
		return "", false
	}
	address := base64.RawURLEncoding.EncodeToString([]byte(socket + "\x00" + pane))
	return codexPaneBindingPrefix + address, true
}

// Unkill removes one kill through the store's non-fatal busy policy.
//
// A Codex kill is not always keyed on the id this call receives: the live
// process can expose a resumed CHILD rollout id, while every id
// the picker shows the user is the lineage ROOT — compose keys every Codex
// row on it (composer.rolloutRow). Resolving id to the root and unhiding
// only that key would leave a child-keyed kill standing: the row would come
// right back killed (composer.killedMatch, store.codexLineageKilled both
// check every member). So every id in the lineage is unkilled, root and
// members alike; the shared store's Unkill is a safe no-op for an id that
// carries no kill.
func (manager *Manager) Unkill(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("unkill id is empty")
	}
	if _, found, err := manager.database.Transcript(ctx, id); err != nil {
		return err
	} else if !found {
		if lineage, found, err := manager.database.CodexLineage(
			ctx,
			id,
		); err != nil {
			return err
		} else if found {
			return manager.unkillLineage(ctx, lineage)
		}
	}
	return manager.database.Unkill(ctx, id)
}

// unkillLineage clears every id in a Codex resume lineage, root and members
// alike — see Unkill's own comment for why the root alone is not enough.
func (manager *Manager) unkillLineage(
	ctx context.Context,
	lineage store.CodexLineage,
) error {
	if err := manager.database.Unkill(ctx, lineage.RootID); err != nil {
		return err
	}
	for _, member := range lineage.MemberIDs {
		if member == lineage.RootID {
			continue
		}
		if err := manager.database.Unkill(ctx, member); err != nil {
			return err
		}
	}
	return nil
}

// Killed returns every current kill in stable ID order.
func (manager *Manager) Killed(ctx context.Context) ([]store.Killed, error) {
	return manager.database.KilledChats(ctx)
}

// lookupTarget resolves the chat a kill is aimed at. engine is the caller's
// own answer to "which engine?", supplied only by a caller holding the row;
// rolloutPath is that same caller's own answer to "which file?" for a Codex
// id, used only to resolve an UNINDEXED lineage member to its root before
// falling back to naming the member's own (wrong) id.
func (manager *Manager) lookupTarget(
	ctx context.Context,
	id string,
	engine pfmengine.ID,
	rolloutPath string,
) (Target, error) {
	transcript, found, err := manager.database.Transcript(ctx, id)
	if err != nil {
		return Target{}, err
	}
	if found {
		return Target{
			Engine:   pfmengine.Claude,
			ID:       id,
			DataPath: transcript.Path,
		}, nil
	}
	lineage, found, err := manager.database.CodexLineage(ctx, id)
	if err != nil {
		return Target{}, err
	}
	if found {
		return Target{
			Engine:   pfmengine.Codex,
			ID:       lineage.RootID,
			DataPath: lineage.Newest.Path,
		}, nil
	}
	if engine == pfmengine.Codex {
		if target, resolved := manager.resolveUnindexedCodexParent(ctx, rolloutPath); resolved {
			return target, nil
		}
	}
	if engine != "" {
		// A LIVE agent, almost always: its row is composed straight from the
		// running process, so the picker shows it long before its transcript
		// reaches the index — and the index is what the two lookups above ask.
		// Refusing here is what made ⌃X on an agent row a silent no-op. The
		// shared killed store is keyed by uuid alone and derives the engine
		// back out of the index, so the agent's own session uuid is the whole
		// key this kill needs.
		return Target{Engine: engine, ID: id}, nil
	}
	return Target{}, fmt.Errorf("chat %q is not indexed", id)
}

func (manager *Manager) codexTarget(
	ctx context.Context,
	id, fallbackPath string,
) (Target, error) {
	lineage, found, err := manager.database.CodexLineage(ctx, id)
	if err != nil {
		return Target{}, err
	}
	if !found {
		if target, resolved := manager.resolveUnindexedCodexParent(ctx, fallbackPath); resolved {
			return target, nil
		}
		return Target{
			Engine:   pfmengine.Codex,
			ID:       id,
			DataPath: fallbackPath,
		}, nil
	}
	path := lineage.Newest.Path
	if path == "" {
		path = fallbackPath
	}
	return Target{
		Engine:   pfmengine.Codex,
		ID:       lineage.RootID,
		DataPath: path,
	}, nil
}

// resolveUnindexedCodexParent reads rolloutPath's own session_meta header —
// the shared shape identifyCodexSelf and the CLI's own row lookup both hit —
// for the conversation it resumes from: the ONLY place that link exists
// before the indexer has parsed the file into the fleet database. It
// answers found=true only when THAT parent id itself resolves to a real,
// already-indexed lineage; a parent that is ALSO unindexed (a deeper resume
// chain the indexer has not reached at all) falls through to the caller's
// own fallback rather than guessing further.
func (manager *Manager) resolveUnindexedCodexParent(
	ctx context.Context,
	rolloutPath string,
) (Target, bool) {
	parent := readCodexLineageParent(rolloutPath)
	if parent == "" {
		return Target{}, false
	}
	lineage, found, err := manager.database.CodexLineage(ctx, parent)
	if err != nil || !found {
		return Target{}, false
	}
	return Target{
		Engine:   pfmengine.Codex,
		ID:       lineage.RootID,
		DataPath: lineage.Newest.Path,
	}, true
}

func parseTMUX(value string) (socketPath, socketName string) {
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		value = value[:comma]
	}
	if value == "" {
		return "", ""
	}
	return value, filepath.Base(value)
}
