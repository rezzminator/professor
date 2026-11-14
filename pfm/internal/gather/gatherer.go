package gather

import (
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// CodexNameResolver resolves the indexed display name for a rollout path.
type CodexNameResolver func(rolloutPath string) naming.CodexName

// CodexIDNameResolver resolves the indexed display name for a Codex thread id.
// It is the only naming rung a session that writes no rollout file has, and
// the index behind it owns the precedence: the thread's own name, then the
// session_index name, then its first prompt.
type CodexIDNameResolver func(threadID string) naming.CodexName

// Dependencies supplies probe interfaces and process-local policy.
type Dependencies struct {
	// Paths are the caller's resolved paths (the runtime's): the process table, tmux directory, SID
	// directory and home every probe reads. Nil resolves them from the environment.
	Paths       *paths.Values
	ProcFS      ProcFS
	Tmux        TmuxClient
	TmuxBinary  string
	TmuxTmpDir  string
	Now         func() time.Time
	CodexName   CodexNameResolver
	CodexIDName CodexIDNameResolver
	CodexThread CodexThreadResolver
	// CodexHomes is the config-owned roster. Nil preserves the historical
	// paths.CodexHome singleton for direct callers; an empty non-nil slice
	// means no configured Codex engine.
	CodexHomes   []string
	ClaudeBinary string
	CodexBinary  string
	// OpenCodeBinary is the configured OpenCode launch command, used to
	// recognise an OpenCode process whose executable is not the default name.
	OpenCodeBinary string
	// OpenCodeSessions is the indexed OpenCode session set DetectOpenCode
	// names a live pane from. gather never opens opencode.db itself — the
	// rows arrive already read (fleet.Gather).
	OpenCodeSessions []OpenCodeSession
	LabelEmojis      []string
	ReadOnly         bool
}

// Gatherer creates immutable live-state snapshots.
type Gatherer struct {
	paths            paths.Values
	proc             ProcFS
	tmux             TmuxClient
	now              func() time.Time
	codexName        CodexNameResolver
	codexIDName      CodexIDNameResolver
	codexThread      CodexThreadResolver
	codexHomes       []string
	claudeBinary     string
	codexBinary      string
	openCodeBinary   string
	openCodeSessions []OpenCodeSession
	labelEmojis      []string
	readOnly         bool
}

// New takes the caller's paths (else resolves them) and fills real implementations for omitted
// interfaces.
func New(dependencies Dependencies) (*Gatherer, error) {
	var resolved paths.Values
	if dependencies.Paths != nil {
		resolved = *dependencies.Paths
	} else {
		var err error
		if resolved, err = paths.Resolve(); err != nil {
			return nil, fmt.Errorf("resolve gather paths: %w", err)
		}
	}
	proc := dependencies.ProcFS
	if proc == nil {
		proc = NewProcFS(resolved.ProcRoot)
	}
	now := dependencies.Now
	if now == nil {
		now = clock.Real.Now
	}
	tmux := dependencies.Tmux
	if tmux == nil {
		probe := TmuxProbe{
			Binary:     dependencies.TmuxBinary,
			TmuxDir:    resolved.TmuxDir,
			TmuxTmpDir: dependencies.TmuxTmpDir,
		}
		if dependencies.TmuxTmpDir != "" {
			probe.TmuxDir = ""
		}
		tmux = probe
	}
	codexHomes := dependencies.CodexHomes
	if codexHomes == nil {
		codexHomes = append([]string(nil), resolved.Roots[pfmengine.Codex]...)
	} else {
		codexHomes = append([]string{}, codexHomes...)
	}
	return &Gatherer{
		paths:          resolved,
		proc:           proc,
		tmux:           tmux,
		now:            now,
		codexName:      dependencies.CodexName,
		codexIDName:    dependencies.CodexIDName,
		codexThread:    dependencies.CodexThread,
		codexHomes:     codexHomes,
		claudeBinary:   dependencies.ClaudeBinary,
		codexBinary:    dependencies.CodexBinary,
		openCodeBinary: dependencies.OpenCodeBinary,
		openCodeSessions: append(
			[]OpenCodeSession(nil), dependencies.OpenCodeSessions...),
		labelEmojis: append([]string(nil), dependencies.LabelEmojis...),
		readOnly:    dependencies.ReadOnly,
	}, nil
}
