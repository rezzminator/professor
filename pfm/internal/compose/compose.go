package compose

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	claudeResumeCap   = 30
	codexResumeCap    = 15
	openCodeResumeCap = 10
)

type composer struct {
	input Input

	transcriptByID   map[string]store.Transcript
	transcriptByPath map[string]store.Transcript
	rolloutByID      map[string]store.Rollout
	rolloutByPath    map[string]store.Rollout
	codexLineages    []store.CodexLineage
	lineageByRoot    map[string]store.CodexLineage
	lineageRootByID  map[string]string
	killedByID       map[string]store.Killed
	panesBySocket    map[string][]gather.ProbePane
	paneByTarget     map[string]gather.ProbePane
	claudeSockets    map[string]struct{}
	cacheSockets     map[string]struct{}
	liveTranscripts  map[string]struct{}
	liveRollouts     map[string]struct{}
	liveOpenCode     map[string]struct{}
	claudeAccounts   accountMatcher
	codexAccounts    accountMatcher
	projectDirs      map[string]string
	projects         projectNames
}

// Compose performs the complete side-effect-free row composition pass.
func Compose(input Input) Output {
	current := &composer{input: input, projects: projectNames{}}
	current.buildIndexes()

	liveClaude, splits := current.liveClaudeRows()
	liveCodex := current.liveCodexRows()
	liveOpenCode := current.liveOpenCodeRows()
	liveRows := collapseLiveServers(
		append(append(liveClaude, liveCodex...), liveOpenCode...),
	)
	liveRows = append(liveRows, splits...)
	// Booting rows are already deduped by socket in gather (DetectCrumblessLive
	// skips any socket a crumb resolves for), so they bypass
	// collapseLiveServers — that function's multi-server winner selection
	// exists for LiveClaude/LiveCodex identity collapse, a different problem.
	liveRows = append(liveRows, current.bootingRows()...)
	agentRows := current.agentRows()

	output := Output{
		ProjectDirs:        cloneStringMap(current.projectDirs),
		includeNewClaude:   input.Options.View != KilledView && len(input.AccountRoots) != 0,
		includeNewCodex:    input.Options.View != KilledView && len(input.Options.CodexAccountIDs) != 0,
		includeNewOpenCode: input.Options.View != KilledView && len(input.Options.OpenCodeAccountIDs) != 0,
		primaryAccount:     input.Options.PrimaryAccount,
		primaryCodex:       input.Options.PrimaryCodexAccount,
		primaryOpenCode:    input.Options.PrimaryOpenCode,
		fallbackDir:        input.Options.CurrentDir,
		projects:           current.projects,
	}
	if !configuredAccount(input.AccountRoots, output.primaryAccount) {
		if len(input.AccountRoots) != 0 {
			output.primaryAccount = input.AccountRoots[0].Account
		}
	}
	if !configuredID(input.Options.CodexAccountIDs, output.primaryCodex) {
		if len(input.Options.CodexAccountIDs) != 0 {
			output.primaryCodex = input.Options.CodexAccountIDs[0]
		}
	}
	if !configuredID(input.Options.OpenCodeAccountIDs, output.primaryOpenCode) {
		if len(input.Options.OpenCodeAccountIDs) != 0 {
			output.primaryOpenCode = input.Options.OpenCodeAccountIDs[0]
		}
	}

	liveRows = append(liveRows, agentRows...)
	for index := range liveRows {
		row := liveRows[index]
		row = current.applyKill(row, EngineForKind(row.Kind))
		countOmitted(row, &output.KilledCount, &output.SuppressedCount)
		if visibleInView(row, input.Options.View) {
			output.Rows = append(output.Rows, current.finalize(row))
		}
	}

	claudeResume := make([]Row, 0)
	claudeEligible := 0
	for index := range input.Transcripts {
		transcript := input.Transcripts[index]
		if _, live := current.liveTranscripts[transcript.UUID]; live {
			continue
		}
		row := current.transcriptRow(transcript, ResumeClaude)
		row = current.applyKill(row, pfmengine.Claude)
		countOmitted(row, &output.KilledCount, &output.SuppressedCount)
		if input.Options.View == DefaultView {
			if defaultEligible(row) {
				claudeEligible++
				claudeResume = insertTopRow(
					claudeResume,
					row,
					claudeResumeCap,
				)
			}
		} else {
			claudeResume = append(claudeResume, row)
		}
	}
	if input.Options.View == DefaultView {
		if claudeEligible > claudeResumeCap {
			output.SuppressedCount += claudeEligible - claudeResumeCap
		}
		for index := range claudeResume {
			row := claudeResume[index]
			output.Rows = append(output.Rows, current.finalize(row))
		}
	} else {
		output.Rows = append(
			output.Rows,
			current.selectResumeRows(claudeResume, claudeResumeCap, &output.SuppressedCount)...)
	}
	codexResume := make([]Row, 0)
	codexEligible := 0
	for index := range current.codexLineages {
		lineage := current.codexLineages[index]
		if _, live := current.liveRollouts[lineage.RootID]; live {
			continue
		}
		row := current.rolloutRow(lineage.Newest, ResumeCodex)
		row = current.applyKill(row, pfmengine.Codex)
		countOmitted(row, &output.KilledCount, &output.SuppressedCount)
		if input.Options.View == DefaultView {
			if defaultEligible(row) {
				codexEligible++
				codexResume = insertTopRow(
					codexResume,
					row,
					codexResumeCap,
				)
			}
		} else {
			codexResume = append(codexResume, row)
		}
	}
	if input.Options.View == DefaultView {
		if codexEligible > codexResumeCap {
			output.SuppressedCount += codexEligible - codexResumeCap
		}
		for index := range codexResume {
			row := codexResume[index]
			output.Rows = append(output.Rows, current.finalize(row))
		}
	} else {
		output.Rows = append(
			output.Rows,
			current.selectResumeRows(codexResume, codexResumeCap, &output.SuppressedCount)...)
	}

	openCodeResume := make([]Row, 0)
	openCodeEligible := 0
	for index := range input.OpenCodeSessions {
		session := input.OpenCodeSessions[index]
		// Subagent children and archived sessions never earn rows: a child is
		// part of its parent's turn, an archived one the user filed away.
		if session.ParentID != "" || session.TimeArchivedMS != 0 {
			continue
		}
		// A session a live pane already claimed is that pane's row, not a
		// second resumable one — the same suppression liveTranscripts and
		// liveRollouts do for the other two engines.
		if _, live := current.liveOpenCode[session.ID]; live {
			continue
		}
		row := current.openCodeSessionRow(session)
		row = current.applyKill(row, EngineForKind(row.Kind))
		countOmitted(row, &output.KilledCount, &output.SuppressedCount)
		if input.Options.View == DefaultView {
			if defaultEligible(row) {
				openCodeEligible++
				openCodeResume = insertTopRow(openCodeResume, row, openCodeResumeCap)
			}
		} else {
			openCodeResume = append(openCodeResume, row)
		}
	}
	if input.Options.View == DefaultView {
		if openCodeEligible > openCodeResumeCap {
			output.SuppressedCount += openCodeEligible - openCodeResumeCap
		}
		for index := range openCodeResume {
			row := openCodeResume[index]
			output.Rows = append(output.Rows, current.finalize(row))
		}
	} else {
		output.Rows = append(
			output.Rows,
			current.selectResumeRows(
				openCodeResume,
				openCodeResumeCap,
				&output.SuppressedCount,
			)...,
		)
	}

	output.Rows, output.ProjectOrder = sortProjectRows(output.Rows)
	output = leadWithCurrentProject(output, input.Options.CurrentDir)
	output = withNewRows(output)
	return output
}

func (current *composer) buildIndexes() {
	current.codexLineages, current.lineageRootByID = store.ResolveCodexLineages(current.input.Rollouts)
	current.lineageByRoot = make(
		map[string]store.CodexLineage,
		len(current.codexLineages),
	)
	for index := range current.codexLineages {
		lineage := current.codexLineages[index]
		current.lineageByRoot[lineage.RootID] = lineage
	}

	wantedTranscriptPaths := make(map[string]struct{}, len(current.input.Snapshot.Crumbs))
	wantedTranscriptIDs := make(map[string]struct{}, len(current.input.Snapshot.Crumbs))
	for _, crumb := range current.input.Snapshot.Crumbs {
		wantedTranscriptPaths[cleanPath(crumb.TranscriptPath)] = struct{}{}
		wantedTranscriptIDs[transcriptIDFromPath(crumb.TranscriptPath)] = struct{}{}
	}
	wantedAgentIDs := make(map[string]struct{}, len(current.input.Snapshot.Agents))
	for _, agent := range current.input.Snapshot.Agents {
		wantedAgentIDs[agent.SessionID] = struct{}{}
	}
	wantedRolloutPaths := make(map[string]struct{}, len(current.input.Snapshot.Codex))
	wantedRolloutIDs := make(map[string]struct{}, len(current.input.Snapshot.Codex))
	for _, process := range current.input.Snapshot.Codex {
		wantedRolloutPaths[cleanPath(process.RolloutPath)] = struct{}{}
		wantedRolloutIDs[gather.LiveCodexThreadID(process)] = struct{}{}
	}
	current.transcriptByID = make(
		map[string]store.Transcript,
		len(wantedAgentIDs)+len(wantedTranscriptIDs),
	)
	current.transcriptByPath = make(
		map[string]store.Transcript,
		len(wantedTranscriptPaths),
	)
	directories := make(map[string]projectDir)
	if current.input.Options.CurrentDir != "" {
		project := current.projects.of(current.input.Options.CurrentDir)
		directories[project] = projectDir{
			path:   cleanPath(current.input.Options.CurrentDir),
			seeded: true,
		}
	}
	for index := range current.input.Transcripts {
		transcript := current.input.Transcripts[index]
		_, wantedAgent := wantedAgentIDs[transcript.UUID]
		_, wantedLive := wantedTranscriptIDs[transcript.UUID]
		if wantedAgent || wantedLive {
			current.transcriptByID[transcript.UUID] = transcript
		}
		normalizedPath := cleanPath(transcript.Path)
		if _, wanted := wantedTranscriptPaths[normalizedPath]; wanted {
			current.transcriptByPath[normalizedPath] = transcript
		}
		rememberProjectDir(current.projects, directories, transcript.CWD, transcript.EffectiveActivityNS())
	}
	current.rolloutByPath = make(map[string]store.Rollout, len(wantedRolloutPaths))
	current.rolloutByID = make(map[string]store.Rollout, len(wantedRolloutIDs))
	for index := range current.input.Rollouts {
		rollout := current.input.Rollouts[index]
		normalizedPath := cleanPath(rollout.Path)
		if _, wanted := wantedRolloutPaths[normalizedPath]; wanted {
			current.rolloutByPath[normalizedPath] = rollout
		}
		if _, wanted := wantedRolloutIDs[rollout.ID]; wanted {
			current.rolloutByID[rollout.ID] = rollout
		}
		if rollout.UserThread {
			rememberProjectDir(current.projects, directories, rollout.CWD, rollout.MTimeNS)
		}
	}
	current.projectDirs = make(map[string]string, len(directories))
	for project, directory := range directories {
		current.projectDirs[project] = directory.path
	}
	current.killedByID = make(map[string]store.Killed, len(current.input.Killed))
	for _, killed := range current.input.Killed {
		current.killedByID[killed.ID] = killed
	}
	current.panesBySocket = make(map[string][]gather.ProbePane)
	current.paneByTarget = make(map[string]gather.ProbePane, len(current.input.Snapshot.Panes))
	for index := range current.input.Snapshot.Panes {
		pane := current.input.Snapshot.Panes[index]
		current.panesBySocket[pane.Socket] = append(
			current.panesBySocket[pane.Socket],
			pane,
		)
		current.paneByTarget[targetKey(pane.Socket, pane.PaneID)] = pane
	}
	for socket := range current.panesBySocket {
		sort.Slice(current.panesBySocket[socket], func(left, right int) bool {
			return current.panesBySocket[socket][left].PaneID <
				current.panesBySocket[socket][right].PaneID
		})
	}
	current.claudeSockets = make(map[string]struct{})
	for _, process := range current.input.Snapshot.ClaudeProcesses {
		current.claudeSockets[process.Socket] = struct{}{}
	}
	// Agent rows are themselves live Claude processes. Counting them here also
	// keeps hand-built snapshots backward-compatible with pre-WP5 fixtures.
	for _, agent := range current.input.Snapshot.Agents {
		if agent.Socket != "" {
			current.claudeSockets[agent.Socket] = struct{}{}
		}
	}
	current.cacheSockets = make(map[string]struct{}, len(current.input.Snapshot.Cache1HSockets))
	for _, socket := range current.input.Snapshot.Cache1HSockets {
		current.cacheSockets[socket] = struct{}{}
	}
	current.liveTranscripts = make(map[string]struct{})
	current.liveRollouts = make(map[string]struct{})
	current.liveOpenCode = make(map[string]struct{})
	// Account roots are the stable side of the prefix match. Resolve each one
	// once, then match the ordinary row path lexically against both its
	// configured and canonical spellings. The previous implementation called
	// EvalSymlinks for every transcript on every picker refresh: a 50k-row
	// corpus repeated 1,000 times spent more than ten minutes in filesystem
	// probes. A path with a third alias still takes the canonical fallback, so
	// the symlink-safe attribution contract is preserved without putting the
	// common path on the filesystem.
	current.claudeAccounts = newAccountMatcher(current.input.AccountRoots)
	current.codexAccounts = newAccountMatcher(current.input.CodexHomes)
}

func canonicalPath(path string) string {
	if path == "" {
		return ""
	}
	cleaned := absoluteCleanPath(path)
	probe := cleaned
	suffix := make([]string, 0, 4)
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return cleanPath(resolved)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return cleaned
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
}

func absoluteCleanPath(path string) string {
	cleaned := cleanPath(path)
	if absolute, err := filepath.Abs(cleaned); err == nil {
		return cleanPath(absolute)
	}
	return cleaned
}

type accountPathRoot struct {
	account    int
	configured string
	canonical  string
	// configDir / configDirCanonical: the seat's own config dir, when the
	// root carries one; empty roots never match a process by config dir.
	configDir          string
	configDirCanonical string
}

type accountMatcher struct {
	roots []accountPathRoot
}

func newAccountMatcher(roots []AccountRoot) accountMatcher {
	matcher := accountMatcher{roots: make([]accountPathRoot, 0, len(roots))}
	for _, root := range roots {
		if root.Account < 1 || root.Path == "" {
			continue
		}
		configured := absoluteCleanPath(root.Path)
		entry := accountPathRoot{
			account:    root.Account,
			configured: configured,
			canonical:  canonicalPath(configured),
		}
		if root.ConfigDir != "" {
			entry.configDir = absoluteCleanPath(root.ConfigDir)
			entry.configDirCanonical = canonicalPath(entry.configDir)
		}
		matcher.roots = append(matcher.roots, entry)
	}
	return matcher
}

// accountForConfigDir names the seat whose config dir a live process runs
// under — an exact match, configured spelling or canonical. Zero when the
// process names no config dir or none of the roots carries one, so the
// caller falls back to the transcript path.
func (matcher accountMatcher) accountForConfigDir(dir string) int {
	if dir == "" {
		return 0
	}
	normalized := absoluteCleanPath(dir)
	canonical := canonicalPath(normalized)
	for _, root := range matcher.roots {
		if root.configDir == "" {
			continue
		}
		if normalized == root.configDir || normalized == root.configDirCanonical ||
			canonical == root.configDir || canonical == root.configDirCanonical {
			return root.account
		}
	}
	return 0
}

func (matcher accountMatcher) accountFor(path string) int {
	if path == "" {
		return 0
	}
	normalized := absoluteCleanPath(path)
	if account := matcher.match(normalized, true); account != 0 {
		return account
	}
	canonical := canonicalPath(normalized)
	if canonical == normalized {
		return 0
	}
	return matcher.match(canonical, false)
}

func (matcher accountMatcher) match(path string, includeConfigured bool) int {
	account := 0
	longest := -1
	for _, root := range matcher.roots {
		candidates := []string{root.canonical}
		if includeConfigured && root.configured != root.canonical {
			candidates = append(candidates, root.configured)
		}
		for _, candidate := range candidates {
			if !pathWithinRoot(path, candidate) || len(candidate) <= longest {
				continue
			}
			longest = len(candidate)
			account = root.account
		}
	}
	return account
}

func pathWithinRoot(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func (current *composer) accountFor(path string) int {
	return current.claudeAccounts.accountFor(path)
}

type crumbsForSocket struct {
	socket *gather.Crumb
	panes  map[string]gather.Crumb
}

func (current *composer) liveClaudeRows() ([]Row, []Row) {
	crumbs := make(map[string]*crumbsForSocket)
	for index := range current.input.Snapshot.Crumbs {
		crumb := current.input.Snapshot.Crumbs[index]
		if id, ok := pfmengine.FromSocket(crumb.Socket); !ok || id != pfmengine.Claude {
			continue
		}
		socketCrumbs := crumbs[crumb.Socket]
		if socketCrumbs == nil {
			socketCrumbs = &crumbsForSocket{panes: make(map[string]gather.Crumb)}
			crumbs[crumb.Socket] = socketCrumbs
		}
		if crumb.PaneID == "" {
			if socketCrumbs.socket == nil ||
				crumb.Filename < socketCrumbs.socket.Filename {
				crumbCopy := crumb
				socketCrumbs.socket = &crumbCopy
			}
			continue
		}
		if _, live := current.paneByTarget[targetKey(crumb.Socket, crumb.PaneID)]; !live {
			continue
		}
		incumbent, found := socketCrumbs.panes[crumb.PaneID]
		if !found || crumb.Filename < incumbent.Filename {
			socketCrumbs.panes[crumb.PaneID] = crumb
		}
	}

	sockets := make([]string, 0, len(crumbs))
	for socket := range crumbs {
		sockets = append(sockets, socket)
	}
	sort.Strings(sockets)

	rows := make([]Row, 0, len(sockets))
	splits := make([]Row, 0)
	for _, socket := range sockets {
		panes := current.panesBySocket[socket]
		if len(panes) == 0 {
			continue
		}
		socketCrumbs := crumbs[socket]
		paneIDs := make([]string, 0, len(socketCrumbs.panes))
		for paneID := range socketCrumbs.panes {
			paneIDs = append(paneIDs, paneID)
		}
		sort.Strings(paneIDs)
		if len(paneIDs) >= 2 {
			splits = append(splits, current.splitRow(socket, paneIDs, socketCrumbs.panes))
			continue
		}

		var crumb gather.Crumb
		var pane gather.ProbePane
		if len(paneIDs) == 1 {
			crumb = socketCrumbs.panes[paneIDs[0]]
			pane = current.paneByTarget[targetKey(socket, paneIDs[0])]
		} else {
			if socketCrumbs.socket == nil {
				continue
			}
			if _, running := current.claudeSockets[socket]; !running {
				continue
			}
			crumb = *socketCrumbs.socket
			pane = panes[0]
		}
		row, id := current.liveClaudeRow(socket, pane, crumb.TranscriptPath)
		if id != "" {
			current.liveTranscripts[id] = struct{}{}
		}
		rows = append(rows, row)
	}
	return rows, splits
}

func (current *composer) liveClaudeRow(
	socket string,
	pane gather.ProbePane,
	path string,
) (Row, string) {
	transcript, found := current.transcriptByPath[cleanPath(path)]
	if !found {
		transcript, found = current.transcriptByID[transcriptIDFromPath(path)]
	}
	if !found {
		transcript = store.Transcript{
			UUID: transcriptIDFromPath(path),
			Path: path,
		}
	}
	row := current.transcriptRow(transcript, LiveClaude)
	row.Socket = socket
	row.PaneID = pane.PaneID
	row.PanePIDs = []int{pane.PID}
	row.SessionName = pane.SessionName
	row.ServerCount = 1
	row.Attached = pane.Attached
	row.Here = socket == current.input.Options.CurrentSocket
	_, row.C1H = current.cacheSockets[socket]
	if row.CWD == "" && pane.CurrentPath != "" {
		row.CWD = pane.CurrentPath
		row.Project = current.projects.of(pane.CurrentPath)
	}
	indexed := naming.DisplayName(
		transcript.CustomTitle,
		transcript.AITitle,
		transcript.FirstPrompt,
	)
	row.Name = naming.LiveFallback(
		indexed,
		pane.PaneTitle,
		pane.SessionName,
		transcript.LastPrompt,
		true,
	)
	if row.ActivityNS == 0 {
		row.ActivityNS = socketEpochNS(socket)
	}
	return row, transcript.UUID
}

func (current *composer) splitRow(
	socket string,
	paneIDs []string,
	crumbs map[string]gather.Crumb,
) Row {
	row := Row{
		Kind:        LiveSplit,
		Socket:      socket,
		ServerCount: 1,
		SplitCount:  len(paneIDs),
		Here:        socket == current.input.Options.CurrentSocket,
	}
	_, row.C1H = current.cacheSockets[socket]
	names := make([]string, 0, len(paneIDs))
	accounts := make(map[int]struct{})
	for _, paneID := range paneIDs {
		pane := current.paneByTarget[targetKey(socket, paneID)]
		row.PanePIDs = append(row.PanePIDs, pane.PID)
		crumb := crumbs[paneID]
		transcript, found := current.transcriptByPath[cleanPath(crumb.TranscriptPath)]
		if !found {
			transcript, found = current.transcriptByID[transcriptIDFromPath(
				crumb.TranscriptPath,
			)]
		}
		if !found {
			transcript = store.Transcript{
				UUID: transcriptIDFromPath(crumb.TranscriptPath),
				Path: crumb.TranscriptPath,
			}
		}
		if transcript.UUID != "" {
			current.liveTranscripts[transcript.UUID] = struct{}{}
		}
		indexed := naming.DisplayName(
			transcript.CustomTitle,
			transcript.AITitle,
			transcript.FirstPrompt,
		)
		name := naming.LiveFallback(
			indexed,
			pane.PaneTitle,
			pane.SessionName,
			transcript.LastPrompt,
			true,
		)
		if name == naming.Unnamed {
			name = "?"
		}
		names = append(names, name)
		row.Size += transcript.Size
		row.PromptCount += transcript.PromptCount
		row.BG = row.BG || transcript.IsBG
		row.Attached = row.Attached || pane.Attached
		if transcript.EffectiveActivityNS() >= row.ActivityNS {
			row.ActivityNS = transcript.EffectiveActivityNS()
			row.CWD = transcript.CWD
			if row.CWD == "" {
				row.CWD = pane.CurrentPath
			}
			row.Path = transcript.Path
			row.LastPrompt = transcript.LastPrompt
		}
		if account := current.accountFor(transcript.Path); account != 0 {
			accounts[account] = struct{}{}
		}
	}
	row.Name = strings.Join(names, "+")
	if row.Name == "" {
		row.Name = "(split)"
	}
	if row.ActivityNS == 0 {
		row.ActivityNS = socketEpochNS(socket)
	}
	row.Project = current.projects.of(row.CWD)
	row.Accounts = sortedIntKeys(accounts)
	return row
}

func (current *composer) liveCodexRows() []Row {
	live := append([]gather.LiveCodex(nil), current.input.Snapshot.Codex...)
	sort.Slice(live, func(left, right int) bool {
		if live[left].Socket != live[right].Socket {
			return live[left].Socket < live[right].Socket
		}
		return live[left].PID < live[right].PID
	})
	rows := make([]Row, 0, len(live))
	for _, process := range live {
		pane, paneFound := current.paneByTarget[targetKey(
			process.Socket,
			process.PaneID,
		)]
		if !paneFound {
			// A responsive cx-* server is not a live Codex chat. The fd-walk
			// process must still resolve to a pane that exists in this same
			// gather snapshot; otherwise its indexed rollout remains resumable.
			continue
		}
		rollout, found := current.rolloutByPath[cleanPath(process.RolloutPath)]
		if !found {
			rollout, found = current.rolloutByID[gather.LiveCodexThreadID(process)]
		}
		if !found {
			rollout = store.Rollout{
				ID:   gather.LiveCodexThreadID(process),
				Path: process.RolloutPath,
			}
		}
		root := current.lineageRoot(rollout)
		if root != "" {
			current.liveRollouts[root] = struct{}{}
		}
		row := current.rolloutRow(rollout, LiveCodex)
		row.Socket = process.Socket
		row.PaneID = process.PaneID
		row.PanePIDs = []int{process.PanePID}
		row.ServerCount = 1
		row.Here = process.Socket == current.input.Options.CurrentSocket
		row.SessionName = pane.SessionName
		row.WindowName = pane.WindowName
		row.Attached = pane.Attached
		if row.CWD == "" && pane.CurrentPath != "" {
			row.CWD = pane.CurrentPath
			row.Project = current.projects.of(pane.CurrentPath)
		}
		if row.Name == "" {
			row.Name = "Codex chat"
		}
		if row.ActivityNS == 0 {
			row.ActivityNS = socketEpochNS(process.Socket)
		}
		rows = append(rows, row)
	}
	return rows
}

// bootingRows synthesizes one row per crumbless-live entry gather found: a
// chat with a live pane and process but no SID crumb yet, because its
// statusline has not rendered a first time. There is no transcript identity
// to key on — the socket IS the identity, exactly as a fresh cc-new-* socket
// has no other name either.
func (current *composer) bootingRows() []Row {
	entries := current.input.Snapshot.CrumblessLive
	if len(entries) == 0 {
		return nil
	}
	rows := make([]Row, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.WindowName)
		if name == "" && entry.SessionName != entry.Socket {
			name = strings.TrimSpace(entry.SessionName)
		}
		if name == "" {
			name = "booting…"
		}
		row := Row{
			Kind:        Booting,
			ID:          entry.Socket,
			Socket:      entry.Socket,
			PaneID:      entry.PaneID,
			PanePIDs:    []int{entry.PID},
			SessionName: entry.SessionName,
			WindowName:  entry.WindowName,
			Name:        name,
			CWD:         entry.CWD,
			Project:     current.projects.of(entry.CWD),
			ServerCount: 1,
			Here:        entry.Socket == current.input.Options.CurrentSocket,
			ActivityNS:  paneStartActivityNS(entry.PaneStartUnix),
		}
		_, row.C1H = current.cacheSockets[entry.Socket]
		if row.ActivityNS == 0 {
			row.ActivityNS = socketEpochNS(entry.Socket)
		}
		rows = append(rows, row)
	}
	return rows
}

func paneStartActivityNS(paneStartUnix int64) int64 {
	if paneStartUnix <= 0 {
		return 0
	}
	return paneStartUnix * 1_000_000_000
}

func (current *composer) agentRows() []Row {
	agents := make(map[string]gather.Agent)
	for _, agent := range current.input.Snapshot.Agents {
		if agent.SessionID == "" {
			continue
		}
		incumbent, found := agents[agent.SessionID]
		if !found || agentSortKey(agent) < agentSortKey(incumbent) {
			agents[agent.SessionID] = agent
		}
	}
	ids := make([]string, 0, len(agents))
	for id := range agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	rows := make([]Row, 0, len(ids))
	for _, id := range ids {
		if _, live := current.liveTranscripts[id]; live {
			continue
		}
		agent := agents[id]
		transcript, found := current.transcriptByID[id]
		if !found {
			transcript = store.Transcript{UUID: id}
		}
		current.liveTranscripts[id] = struct{}{}
		row := current.transcriptRow(transcript, Agent)
		row.Socket = agent.Socket
		row.PaneID = agent.PaneID
		row.ConfigDir = agent.ConfigDir
		row.ServerCount = boolInt(agent.Socket != "")
		row.Here = agent.Socket == current.input.Options.CurrentSocket
		if pane, found := current.paneByTarget[targetKey(agent.Socket, agent.PaneID)]; found {
			row.PanePIDs = []int{pane.PID}
			row.SessionName = pane.SessionName
			row.WindowName = pane.WindowName
			row.Attached = pane.Attached
		}
		if row.Name == "" {
			row.Name = "(no prompt)"
		}
		// The process's config dir is the seat, whatever store the transcript
		// sits in: with seats sharing one store, the path names every seat.
		if account := current.claudeAccounts.accountForConfigDir(agent.ConfigDir); account != 0 {
			row.Account = account
		} else if row.Account == 0 {
			row.Account = current.accountFor(agent.ConfigDir)
		}
		_, row.C1H = current.cacheSockets[agent.Socket]
		rows = append(rows, row)
	}
	return rows
}

func (current *composer) transcriptRow(
	transcript store.Transcript,
	kind Kind,
) Row {
	return Row{
		Kind: kind,
		ID:   transcript.UUID,
		Path: transcript.Path,
		Name: naming.DisplayName(
			transcript.CustomTitle,
			transcript.AITitle,
			transcript.FirstPrompt,
		),
		LastPrompt:  transcript.LastPrompt,
		Project:     current.projects.of(transcript.CWD),
		CWD:         transcript.CWD,
		Size:        transcript.Size,
		PromptCount: transcript.PromptCount,
		ActivityNS:  transcript.EffectiveActivityNS(),
		Account:     current.accountFor(transcript.Path),
		BG:          transcript.IsBG,
	}
}

func (current *composer) rolloutRow(rollout store.Rollout, kind Kind) Row {
	root := current.lineageRoot(rollout)
	newest := rollout
	if lineage, found := current.lineageByRoot[root]; found {
		newest = lineage.Newest
		newest.PromptCount = lineage.PromptCount
	}
	name := naming.CodexRowName(
		newest.ID,
		newest.SessionID,
		newest.ParentThread,
		root,
		newest.FirstPrompt,
		current.input.CxNames,
	)
	return Row{
		Kind:        kind,
		ID:          root,
		Path:        newest.Path,
		Name:        name,
		Project:     current.projects.of(newest.CWD),
		CWD:         newest.CWD,
		Size:        newest.Size,
		PromptCount: newest.PromptCount,
		ActivityNS:  newest.MTimeNS,
		Account:     current.codexAccounts.accountFor(newest.Path),
		BG:          newest.IsBG,
	}
}

func (current *composer) lineageRoot(rollout store.Rollout) string {
	if root := current.lineageRootByID[rollout.ID]; root != "" {
		return root
	}
	if rollout.LineageRoot != "" {
		return rollout.LineageRoot
	}
	if rollout.SessionID != "" {
		return rollout.SessionID
	}
	if rollout.ParentThread != "" {
		return rollout.ParentThread
	}
	return rollout.ID
}

func (current *composer) applyKill(row Row, engine pfmengine.ID) Row {
	// A "_KILL…" label is a kill the user writes by renaming the chat, so it
	// needs no id, no store row and no picker keystroke — which is why it is
	// tested BEFORE every id-shaped guard below and applies to a live chat the
	// index has never seen. A split row is the one exclusion: its Name is a
	// join of its panes' names, not a label anyone set on one chat.
	if row.Kind != LiveSplit && naming.LabelKilled(row.Name) {
		row.Killed = true
		row.NameKilled = true
		return row
	}
	// A Booting row's ID is the crumbless socket name, not a chat identity —
	// unlike LiveSplit's empty-ID case, it WOULD pass the id check below, so
	// it needs its own guard. The socket is reused by the picker's own
	// kill-eligibility test (ui/model.go's toggleKilled): neither side may let
	// a kill land on an identity that stops meaning anything the moment the
	// crumb appears and the row becomes an ordinary live one.
	// An unidentified live OpenCode row is keyed on its own SOCKET for exactly
	// the reason Booting is, and needs the same guard: the moment the seat's
	// session is finally pinned down, a tombstone written against the socket
	// names nothing at all.
	if row.Kind == LiveSplit || row.Kind == Booting || row.ID == "" ||
		pfmengine.SocketKeyedID(engine, row.ID, row.Socket) {
		return row
	}
	// Explicit kills carry no baseline and stay permanent. A /clear kill is a
	// prompt-count ratchet: once this row grows past its stored baseline it is
	// visible again, matching the store's persisted auto-unkill pass.
	row.Killed = current.killedMatch(row.ID, engine, row.PromptCount)
	return row
}

// killedMatch reports whether id — or, for a Codex row, ANY id in its resume
// lineage — carries a kill whose engine agrees.
//
// A live Codex process exposes the raw id of its current rollout, which can be
// the resumed CHILD's id
// on a multi-file lineage, not the ROOT this row is keyed on (rolloutRow,
// liveCodexRows) — a kill written that way lands on a key nothing else here
// reads unless every member id is checked too. A kill the `kill` manager
// itself writes is already normalized onto the root
// (internal/kill/manager.go), so this lineage walk only ever WIDENS what
// matches, never narrows it.
//
// See also codexLineageKilled (store/queries.go), the cached first frame's
// copy of this same question — the two must never disagree about what the
// user sees.
func (current *composer) killedMatch(id string, engine pfmengine.ID, promptCount int64) bool {
	if killMatchesID(current.killedByID, id, engine, promptCount) {
		return true
	}
	if engine != pfmengine.Codex {
		return false
	}
	lineage, found := current.lineageByRoot[id]
	if !found {
		return false
	}
	for _, member := range lineage.MemberIDs {
		if killMatchesID(current.killedByID, member, engine, promptCount) {
			return true
		}
	}
	return false
}

func killMatchesID(
	killedByID map[string]store.Killed,
	id string,
	engine pfmengine.ID,
	promptCount int64,
) bool {
	killed, found := killedByID[id]
	if !found {
		return false
	}
	if killed.Engine != "" && killed.Engine != engine {
		return false
	}
	return killed.BaselinePrompts == nil || promptCount <= *killed.BaselinePrompts
}

func (current *composer) selectResumeRows(
	rows []Row,
	capacity int,
	suppressedCount *int,
) []Row {
	switch current.input.Options.View {
	case AllView:
		selected := make([]Row, 0, len(rows))
		for index := range rows {
			row := rows[index]
			selected = append(selected, current.finalize(row))
		}
		defaultRows := defaultEligibleCount(rows)
		if defaultRows > capacity {
			*suppressedCount += defaultRows - capacity
		}
		return selected
	case KilledView:
		selected := make([]Row, 0)
		for index := range rows {
			row := rows[index]
			if row.Killed {
				selected = append(selected, current.finalize(row))
			}
		}
		defaultRows := defaultEligibleCount(rows)
		if defaultRows > capacity {
			*suppressedCount += defaultRows - capacity
		}
		return selected
	default:
		panic("default resume rows are selected while scanning")
	}
}

func countOmitted(row Row, killedCount, suppressedCount *int) {
	if row.Killed {
		*killedCount++
		return
	}
	if !defaultEligible(row) {
		*suppressedCount++
	}
}

func (current *composer) finalize(row Row) Row {
	if current.input.Options.NowNS > row.ActivityNS && row.ActivityNS > 0 {
		row.AgeNS = current.input.Options.NowNS - row.ActivityNS
	}
	return row
}

// defaultEligible answers whether a row belongs in the default listing.
//
// THE KILL IS TESTED FIRST, before any liveness short-circuit: killed is dead,
// live or not. A split row is the one row with no single id to kill — compose
// never marks it — so it short-circuits above the test rather than around it.
func defaultEligible(row Row) bool {
	if row.Killed {
		return false
	}
	if row.Kind == LiveSplit {
		return true
	}
	// A live agent is exempt from the emptiness tests below and from those
	// ALONE: it is rowed from its running process, so its transcript is often
	// still zero bytes with no prompt parsed out of it.
	if row.Kind == Agent {
		return true
	}
	// A booting row has no transcript at all — it exists BECAUSE the crumb
	// that would normally lead compose to one has not been written yet — so
	// the emptiness test below would suppress every one of them from the
	// default view and silently defeat this entire fix.
	if row.Kind == Booting {
		return true
	}
	// A live Codex row is exempt from the SIZE/PROMPT half of the test below
	// for the same reason: Codex >=0.146.1 keeps a paginated thread's content
	// in its own sqlite state store, so the rollout file gather and the index
	// parse from can carry prompt_count=0 and size=0 even while the chat is
	// genuinely running in tmux right now. The index enriches such a row from
	// the state store once it catches up (applyCodexThread); until then, a
	// row this self-evidently real must never be judged by a test built to
	// catch an abandoned zero-byte spawn.
	//
	// The BG half is NOT waived: a machine-spawned thread (codex exec, never
	// renamed) caught live in a pane is still background work, not a chat,
	// exactly like its resume-shape twin — the exemption is for genuinely
	// empty content, never for who started the conversation.
	if row.Kind == LiveCodex {
		return !row.BG
	}
	// A LIVE OpenCode row is exempt for the same reason and one stronger: a
	// seat no indexed session could be pinned to carries NO counters at all
	// (liveOpenCodeRows), so every emptiness test below reads a running TUI
	// the user is typing into as an abandoned spawn. The whole point of this
	// Kind is that such a chat stops being reported as absent.
	if row.Kind == LiveOpenCode {
		return !row.BG
	}
	// An OpenCode session has no file size at all — it lives entirely inside
	// its engine's SQLite store, so the size half of this test would suppress
	// every one of them forever. Its reality signal is prompts AND an answer:
	// a session with no admitted input was opened and never used, and one
	// with prompts but zero assistant messages was opened and never
	// answered — exactly as empty as a Claude transcript with no visible
	// turns. The displayed prompt count is never fudged to fake either case.
	if row.Kind == ResumeOpenCode {
		return !row.BG && row.PromptCount > 0 && row.AssistantCount > 0
	}
	// A resumable Claude transcript with a file but no parsed prompts is a
	// spawn that was never used, so the default view suppresses it on purpose.
	// The all view remains the way to reach that row.
	return !row.BG && row.Size > 0 && row.PromptCount > 0
}

func visibleInView(row Row, view View) bool {
	switch view {
	case AllView:
		return true
	case KilledView:
		return row.Killed
	default:
		return defaultEligible(row)
	}
}

func defaultEligibleCount(rows []Row) int {
	count := 0
	for index := range rows {
		row := rows[index]
		if defaultEligible(row) {
			count++
		}
	}
	return count
}

func collapseLiveServers(rows []Row) []Row {
	type winner struct {
		row   Row
		count int
	}
	winners := make(map[string]winner)
	standalone := make([]Row, 0)
	for index := range rows {
		row := rows[index]
		if row.ID == "" || row.Kind == LiveSplit {
			standalone = append(standalone, row)
			continue
		}
		key := string(EngineForKind(row.Kind)) + "\x00" + row.ID
		incumbent, found := winners[key]
		if !found {
			winners[key] = winner{row: row, count: 1}
			continue
		}
		incumbent.count++
		if newerSocket(row.Socket, incumbent.row.Socket) {
			incumbent.row = row
		}
		winners[key] = incumbent
	}
	keys := make([]string, 0, len(winners))
	for key := range winners {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := winners[key]
		entry.row.ServerCount = entry.count
		standalone = append(standalone, entry.row)
	}
	return standalone
}

func newerSocket(challenger, incumbent string) bool {
	challengerEpoch := pfmengine.SocketBirth(challenger)
	incumbentEpoch := pfmengine.SocketBirth(incumbent)
	if challengerEpoch != incumbentEpoch {
		return challengerEpoch > incumbentEpoch
	}
	return challenger > incumbent
}

func socketEpochNS(socket string) int64 {
	epoch := pfmengine.SocketBirth(socket)
	if epoch <= 0 || epoch > (1<<63-1)/1_000_000_000 {
		return 0
	}
	return epoch * 1_000_000_000
}

func targetKey(socket, paneID string) string {
	return socket + "\x00" + paneID
}

func transcriptIDFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func configuredAccount(roots []AccountRoot, account int) bool {
	for _, root := range roots {
		if root.Account == account {
			return true
		}
	}
	return false
}

func configuredID(ids []int, account int) bool {
	for _, id := range ids {
		if id == account {
			return true
		}
	}
	return false
}

// EngineForKind reports the engine ID a row's kind belongs to —
// exported so a caller resolving an id against a compose pass (cmd/pfm's
// CLI kill) can vouch for the same engine the picker itself would.
func EngineForKind(kind Kind) pfmengine.ID {
	id, err := EngineForKindChecked(kind)
	if err != nil {
		return pfmengine.ID(kind.String())
	}
	return id
}

// EngineForKindChecked is the input-boundary form of EngineForKind. An
// unknown Kind is a named programming/data error, never an empty engine that
// drifts into MustLookup several hops later.
func EngineForKindChecked(kind Kind) (pfmengine.ID, error) {
	switch kind {
	case LiveCodex, ResumeCodex, NewCodex:
		return pfmengine.Codex, nil
	case LiveOpenCode, ResumeOpenCode, NewOpenCode:
		return pfmengine.OpenCode, nil
	case LiveClaude, ResumeClaude, NewClaude, LiveSplit, Agent, Booting:
		return pfmengine.Claude, nil
	default:
		return "", fmt.Errorf("unknown compose kind %d", kind)
	}
}

func agentSortKey(agent gather.Agent) string {
	return agent.Socket + "\x00" + agent.PaneID + "\x00" + strconv.Itoa(agent.PID)
}

func sortRowsByActivity(rows []Row) {
	sort.Slice(rows, func(left, right int) bool {
		return rowComesBefore(rows[left], rows[right])
	})
}

func rowComesBefore(left, right Row) bool {
	if left.ActivityNS != right.ActivityNS {
		return left.ActivityNS > right.ActivityNS
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	if left.Socket != right.Socket {
		return left.Socket < right.Socket
	}
	return left.Kind < right.Kind
}

func insertTopRow(rows []Row, row Row, capacity int) []Row {
	insert := sort.Search(len(rows), func(position int) bool {
		return rowComesBefore(row, rows[position])
	})
	if insert >= capacity {
		return rows
	}
	rows = append(rows, Row{})
	copy(rows[insert+1:], rows[insert:])
	rows[insert] = row
	if len(rows) > capacity {
		rows = rows[:capacity]
	}
	return rows
}

func cleanPath(path string) string {
	if path == "" {
		return ""
	}
	if !strings.Contains(path, "//") &&
		!strings.Contains(path, "/./") &&
		!strings.Contains(path, "/../") &&
		!strings.HasSuffix(path, "/.") &&
		!strings.HasSuffix(path, "/..") {
		if path == string(filepath.Separator) {
			return path
		}
		return strings.TrimSuffix(path, string(filepath.Separator))
	}
	return filepath.Clean(path)
}

func sortedIntKeys(values map[int]struct{}) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
