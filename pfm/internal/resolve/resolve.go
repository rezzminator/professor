package resolve

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// Kind is one public resolution namespace.
type Kind string

const (
	Label    Kind = "label"
	Session  Kind = "session"
	CxWindow Kind = "cxwin"
	tmuxName      = "tmux"
)

// Outcome is the exact stdout/stderr/return-code contract consumed by chat.sh.
type Outcome struct {
	Code   int
	Stdout string
	Stderr string
}

// Pane is one tmux pane row used by all three resolvers.
type ResolvedPane struct {
	SocketPath     string
	SessionName    string
	PaneID         string
	CurrentCommand string
	WindowName     string
}

// TmuxClient supplies the two read-only tmux operations resolution needs.
type TmuxClient interface {
	ListPanes(ctx context.Context, socketPath string) ([]ResolvedPane, error)
	CapturePane(ctx context.Context, socketPath, paneID string) (string, error)
}

// Binaries are the configured engine executables. Tmux reports the foreground
// process basename, so callers pass the configured command paths here and the
// resolver compares their basenames without making the config package part of
// the resolution contract.
type Binaries struct {
	Values        map[pfmengine.ID]string
	AccountEmojis []string
}

// Resolver scans a caller-selected tmux and crumb jail.
type Resolver struct {
	tmux          TmuxClient
	tmuxDir       string
	sidDir        string
	binaries      map[pfmengine.ID]string
	accountEmojis []string
}

// New resolves the standard paths and uses command-backed tmux when omitted.
func New(client TmuxClient, configured ...Binaries) (*Resolver, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve resolver paths: %w", err)
	}
	if client == nil {
		client = TmuxResolver{}
	}
	binaries := Binaries{}
	if len(configured) != 0 {
		binaries = configured[0]
	}
	return &Resolver{
		tmux:          client,
		tmuxDir:       resolved.TmuxDir,
		sidDir:        resolved.SIDDir,
		binaries:      cloneBinaries(binaries.Values),
		accountEmojis: append([]string(nil), binaries.AccountEmojis...),
	}, nil
}

func cloneBinaries(values map[pfmengine.ID]string) map[pfmengine.ID]string {
	cloned := make(map[pfmengine.ID]string, len(values))
	for id, binary := range values {
		cloned[id] = binary
	}
	return cloned
}

// Resolve applies one exact chat.sh namespace contract.
func (resolver *Resolver) Resolve(
	ctx context.Context,
	kind Kind,
	name string,
) (Outcome, error) {
	panes, err := resolver.allPanes(ctx)
	if err != nil {
		return Outcome{}, err
	}
	switch kind {
	case Label:
		return resolver.resolveLabel(ctx, name, panes)
	case Session:
		return resolver.resolveSession(name, panes), nil
	case CxWindow:
		return resolver.resolveCxWindow(name, panes), nil
	default:
		return Outcome{}, fmt.Errorf("unknown resolve kind %q", kind)
	}
}

func (resolver *Resolver) allPanes(ctx context.Context) ([]ResolvedPane, error) {
	entries, err := os.ReadDir(resolver.tmuxDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tmux socket directory: %w", err)
	}
	socketPaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		socketPaths = append(socketPaths, filepath.Join(resolver.tmuxDir, entry.Name()))
	}
	sort.Strings(socketPaths)

	var mutex sync.Mutex
	rowsBySocket := make(map[string][]ResolvedPane, len(socketPaths))
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(32)
	for _, socketPath := range socketPaths {
		socketPath := socketPath
		group.Go(func() error {
			rows, err := resolver.tmux.ListPanes(groupContext, socketPath)
			if err != nil {
				// One unreadable server is one ended chat; a tmux that never
				// started read no server at all, and a miss built on it would
				// refuse a live chat as "matched no live chat".
				if pfmtmux.CouldNotRun(err) {
					return fmt.Errorf("tmux could not run to list panes on %s: %w", socketPath, err)
				}
				return nil
			}
			for index := range rows {
				rows[index].SocketPath = socketPath
			}
			mutex.Lock()
			rowsBySocket[socketPath] = rows
			mutex.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	panes := make([]ResolvedPane, 0)
	for _, socketPath := range socketPaths {
		panes = append(panes, rowsBySocket[socketPath]...)
	}
	return panes, nil
}

type match struct {
	socketPath string
	paneID     string
	session    string
}

type captureResult struct {
	match match
	name  string
	order int
}

func (resolver *Resolver) resolveLabel(
	ctx context.Context,
	want string,
	panes []ResolvedPane,
) (Outcome, error) {
	results := make([]captureResult, 0)
	var mutex sync.Mutex
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(32)
	for order, pane := range panes {
		order := order
		pane := pane
		if pane.PaneID == "" || pane.CurrentCommand == tmuxName {
			continue
		}
		group.Go(func() error {
			capture, err := resolver.tmux.CapturePane(
				groupContext,
				pane.SocketPath,
				pane.PaneID,
			)
			if err != nil {
				return nil
			}
			label := naming.BookmarkLabelFor(capture, resolver.accountEmojis)
			if label == "" || !strings.EqualFold(label, want) {
				return nil
			}
			mutex.Lock()
			results = append(results, captureResult{
				match: match{
					socketPath: pane.SocketPath,
					paneID:     pane.PaneID,
					session:    pane.SessionName,
				},
				name:  label,
				order: order,
			})
			mutex.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return Outcome{}, err
	}
	sort.Slice(results, func(left, right int) bool {
		return results[left].order < results[right].order
	})
	matches := dedupeMatches(results)
	if len(matches) > 1 {
		if winner, uuid, ok := resolver.sameChatWinner(matches); ok {
			return Outcome{
				Code:   0,
				Stdout: targetLine(winner.socketPath, winner.paneID),
				Stderr: fmt.Sprintf(
					"note: 🔖 '%s' is hosted by %d servers for ONE chat (%s) — resolving to the newest (pfm shows it ⚠%dsrv; open it there to reap the elders)\n",
					want,
					len(matches),
					uuid,
					len(matches),
				),
			}, nil
		}
	}
	switch len(matches) {
	case 0:
		return Outcome{Code: 1}, nil
	case 1:
		return Outcome{
			Code:   0,
			Stdout: targetLine(matches[0].socketPath, matches[0].paneID),
		}, nil
	default:
		var stderr strings.Builder
		fmt.Fprintf(&stderr, "ambiguous 🔖 label '%s' — matches panes:\n", want)
		for _, candidate := range matches {
			fmt.Fprintf(
				&stderr,
				"  pane %s  (socket %s)\n",
				candidate.paneID,
				candidate.socketPath,
			)
		}
		return Outcome{Code: 2, Stderr: stderr.String()}, nil
	}
}

func dedupeMatches(results []captureResult) []match {
	matches := make([]match, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		key := result.match.socketPath + "\x00" + result.match.paneID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		matches = append(matches, result.match)
	}
	return matches
}

func (resolver *Resolver) sameChatWinner(
	matches []match,
) (match, string, bool) {
	uuid := ""
	winner := match{}
	bestEpoch := int64(-1)
	for _, candidate := range matches {
		socket := filepath.Base(candidate.socketPath)
		transcriptPath := readFirst(
			filepath.Join(resolver.sidDir, socket+"."+candidate.paneID),
			filepath.Join(resolver.sidDir, socket),
		)
		candidateUUID := strings.TrimSuffix(
			filepath.Base(transcriptPath),
			filepath.Ext(transcriptPath),
		)
		if candidateUUID == "" || candidateUUID == "." {
			return match{}, "", false
		}
		if uuid == "" {
			uuid = candidateUUID
		} else if candidateUUID != uuid {
			return match{}, "", false
		}
		epoch := socketStartedAt(socket)
		if epoch > bestEpoch {
			bestEpoch = epoch
			winner = candidate
		}
	}
	return winner, uuid, uuid != ""
}

func (resolver *Resolver) resolveSession(want string, panes []ResolvedPane) Outcome {
	matches := make([]match, 0)
	panesBySession := make(map[string][]ResolvedPane)
	seen := make(map[string]struct{})
	for _, pane := range panes {
		if pane.SessionName != want {
			continue
		}
		key := pane.SocketPath + "\x00" + pane.SessionName
		panesBySession[key] = append(panesBySession[key], pane)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		matches = append(matches, match{
			socketPath: pane.SocketPath,
			session:    pane.SessionName,
		})
	}
	switch len(matches) {
	case 0:
		return Outcome{Code: 1}
	case 1:
		key := matches[0].socketPath + "\x00" + matches[0].session
		sessionPanes := panesBySession[key]
		if len(sessionPanes) > 1 {
			claudePanes := make([]ResolvedPane, 0)
			for _, pane := range sessionPanes {
				if isClaudePaneCommand(pane.CurrentCommand, resolver.binaries[pfmengine.Claude]) {
					claudePanes = append(claudePanes, pane)
				}
			}
			if len(claudePanes) == 1 {
				return Outcome{
					Code:   0,
					Stdout: targetLine(matches[0].socketPath, claudePanes[0].PaneID),
				}
			}
			return Outcome{
				Code: 2,
				Stderr: fmt.Sprintf(
					"ambiguous session '%s' — it holds %d panes, %d running claude; a bare session name would target whichever pane is active. Address by 🔖 label or %%pane-id instead (run chat_ls).\n",
					want,
					len(sessionPanes),
					len(claudePanes),
				),
			}
		}
		return Outcome{
			Code:   0,
			Stdout: targetLine(matches[0].socketPath, sessionPanes[0].PaneID),
		}
	default:
		var stderr strings.Builder
		fmt.Fprintf(&stderr, "ambiguous session '%s' — matches:\n", want)
		for _, candidate := range matches {
			fmt.Fprintf(
				&stderr,
				"  %s  (socket %s)\n",
				candidate.session,
				candidate.socketPath,
			)
		}
		return Outcome{Code: 2, Stderr: stderr.String()}
	}
}

func (resolver *Resolver) resolveCxWindow(want string, panes []ResolvedPane) Outcome {
	queryRunes := runeCount(want)
	clippedWant := naming.ClipRunes(want, 24)
	matches := make([]match, 0)
	seen := make(map[string]struct{})
	for _, pane := range panes {
		exact := strings.EqualFold(pane.WindowName, want)
		clipped := queryRunes > 24 &&
			strings.EqualFold(pane.WindowName, clippedWant)
		id, known := pfmengine.FromSocket(filepath.Base(pane.SocketPath))
		codexSocket := known && id == pfmengine.Codex
		if (!codexSocket && !isCodexPaneCommand(pane.CurrentCommand, resolver.binaries[pfmengine.Codex])) ||
			pane.PaneID == "" ||
			pane.WindowName == "" ||
			(!exact && !clipped) {
			continue
		}
		key := pane.SocketPath + "\x00" + pane.PaneID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		matches = append(matches, match{
			socketPath: pane.SocketPath,
			paneID:     pane.PaneID,
		})
	}
	switch len(matches) {
	case 0:
		return Outcome{Code: 1}
	case 1:
		return Outcome{
			Code:   0,
			Stdout: targetLine(matches[0].socketPath, matches[0].paneID),
		}
	default:
		var stderr strings.Builder
		fmt.Fprintf(
			&stderr,
			"ambiguous codex thread name '%s' — matches panes:\n",
			want,
		)
		for _, candidate := range matches {
			fmt.Fprintf(
				&stderr,
				"  pane %s  (socket %s)\n",
				candidate.paneID,
				candidate.socketPath,
			)
		}
		return Outcome{Code: 2, Stderr: stderr.String()}
	}
}

func isClaudePaneCommand(command string, binaries ...string) bool {
	return pfmengine.MatchCommand(pfmengine.Claude, []string{command}, true, binaries...)
}

func isCodexPaneCommand(command string, binaries ...string) bool {
	return pfmengine.MatchCommand(pfmengine.Codex, []string{command}, false, binaries...)
}

func targetLine(socketPath, target string) string {
	return socketPath + "\t" + target + "\n"
}

func readFirst(candidates ...string) string {
	for _, candidate := range candidates {
		content, err := os.ReadFile(candidate)
		if err == nil {
			return strings.TrimRight(string(content), "\r\n")
		}
	}
	return ""
}

func socketStartedAt(socket string) int64 {
	parts := strings.Split(socket, "-")
	if len(parts) < 2 {
		return 0
	}
	epoch, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || epoch < 0 {
		return 0
	}
	return epoch
}

func runeCount(value string) int {
	count := 0
	for range value {
		count++
	}
	return count
}
