package tokens

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// `pfm tokens --tree ID` measures one Claude Code spawn tree: a session id
// roots it at the main session and takes every sub-agent of the session; an
// agent id roots it at that sub-agent and takes its descendants, each found
// through its agent-{id}.meta.json parentAgentId. Every agent is one row:
// its tokens and dollars from the same replay and price table the report
// uses, the bytes it wrote (Write content, Edit new_string, MultiEdit edits'
// new_string, NotebookEdit new_source, counted only when the target path is
// under the write scope — the --worktree, else the session's recorded cwd —
// and never under /tmp or /private/tmp) and the bytes it read (the text of
// every tool_result it received). The tree adds landed code lines
// (landedRules) against --base when --worktree names the checkout.
//
// The JSON document (--json on stdout, --out FILE) has four keys in this
// order, each object's keys in the order listed:
//
//	excluded  the landed-line rules version, landedRules
//	tree      target, root ("session" or "agent"), session, writeScope,
//	          agents, notRead, inputTokens (uncached + cache read + cache
//	          write), uncachedInputTokens, cacheReadTokens, cacheWriteTokens,
//	          usd (null when no agent is priced), unpricedAgents,
//	          writtenBytes, readBytes, silentShare (the input-token share of
//	          agents that wrote 0 bytes; null with no input), worktree, base,
//	          landedLines, landedPerMillionInput, landedExcluded {testLines,
//	          docLines, generatedLines, lockLines, binaryFiles}, and
//	          landedNotMeasured (the reason; null when measured, and then the
//	          landed fields are null)
//	types     one object per agent type, the most input first: type, agents,
//	          inputTokens, writtenBytes, readBytes, writtenReadRatio (null
//	          when nothing was read)
//	agents    one object per agent, the most input first: id, type ("main"
//	          for the session), parent, depth (null when its meta is
//	          unreadable), model, inputTokens, uncachedInputTokens,
//	          cacheReadTokens, cacheWriteTokens, usd, writtenBytes, readBytes,
//	          writtenReadRatio, unreadable (null, or why the row was not read)
//
// An unreadable agent stays a row and counts in notRead, never in a total;
// the run then exits 1.

// landedNotMeasured is what the landed fields say without --worktree/--base.
const landedNotMeasured = "NOT MEASURED — pass --worktree DIR --base SHA"

const (
	treeRootSession = "session"
	treeRootAgent   = "agent"
	agentPrefix     = "agent-"
)

// treeAgent is one transcript of the tree, measured.
type treeAgent struct {
	id, agentType, parent, file, model string
	depth                              int // -1 when the meta that places it is unreadable
	in, cacheRead, cacheWrite          float64
	usd                                *float64
	written, read                      int64
	unreadable                         string
}

func (n *treeAgent) input() float64 { return n.in + n.cacheRead + n.cacheWrite }

// spawnTree is the tree the target roots.
type spawnTree struct {
	target, root, session, scope string
	agents                       []*treeAgent
}

// treeRoots are the transcript roots --tree searches: every --root, else the
// ambient CLAUDE_CONFIG_DIR's projects directory then pfm's resolved Claude
// roots, each once.
func (a *auditor) treeRoots() []string {
	var roots []string
	if len(a.opts.roots) != 0 {
		roots = a.opts.roots
	} else {
		if a.env != nil {
			if dir := config.AmbientClaudeConfigDirFrom(a.env); dir != "" {
				roots = append(roots, filepath.Join(dir, "projects"))
			}
		}
		roots = append(roots, a.claudeRoots...)
	}
	var unique []string
	seen := map[string]bool{}
	for _, root := range roots {
		if clean := filepath.Clean(root); !seen[clean] {
			seen[clean] = true
			unique = append(unique, clean)
		}
	}
	return unique
}

// locate finds the one transcript id names: a session {id}.jsonl or a
// sub-agent agent-{id}.jsonl under the roots.
func (a *auditor) locate(id string) (path string, isAgent bool, err error) {
	roots := a.treeRoots()
	if len(roots) == 0 {
		return "", false, failure("--tree: no Claude transcript root is configured; pass --root DIR")
	}
	sessions, sessionErrs := transcript.FindSessions(roots, id)
	agents, agentErrs := transcript.FindSubagents(roots, id)
	var found []string
	for _, file := range sessions {
		if transcript.TranscriptStem(file) == id {
			found = append(found, file)
		}
	}
	matchedSessions := len(found)
	for _, file := range agents {
		if agentID, _ := transcript.SubagentID(filepath.Base(file)); agentID == id {
			found = append(found, file)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], matchedSessions == 0, nil
	case len(found) > 1:
		return "", false, failure("--tree %s names %d transcripts: %s", id, len(found), strings.Join(found, ", "))
	}
	var lookErrs []string
	for _, err := range append(sessionErrs, agentErrs...) {
		lookErrs = append(lookErrs, err.Error())
	}
	if len(lookErrs) != 0 {
		return "", false, failure("--tree %s: no session or agent transcript of that id under %s, and %d places "+
			"could not be read: %s", id, strings.Join(roots, ", "), len(lookErrs), strings.Join(lookErrs, "; "))
	}
	return "", false, failure("--tree %s: no session or agent transcript of that id under %s",
		id, strings.Join(roots, ", "))
}

// buildTree reads the session's sub-agent metas and keeps the agents the
// target roots, each placed by its parent chain.
func (a *auditor) buildTree(id string) (*spawnTree, error) {
	id = strings.TrimPrefix(id, agentPrefix)
	if id == "" {
		return nil, usageError("--tree wants a session id or an agent id")
	}
	path, isAgent, err := a.locate(id)
	if err != nil {
		return nil, err
	}
	sessionFile := path
	if isAgent {
		dir, _ := transcript.SessionDir(path)
		sessionFile = dir + ".jsonl"
	}
	tree := &spawnTree{target: id, root: treeRootSession, session: transcript.TranscriptStem(sessionFile)}
	main := &treeAgent{id: tree.session, agentType: kindMain, file: sessionFile}
	nodes := map[string]*treeAgent{}
	var order []*treeAgent
	subDir := transcript.SubagentsDir(sessionFile)
	if _, statErr := os.Stat(subDir); statErr == nil {
		files, listErr := transcript.SubagentTranscripts(subDir)
		if listErr != nil {
			return nil, failure("--tree %s: %v", id, listErr)
		}
		for _, file := range files {
			agentID, _ := transcript.SubagentID(filepath.Base(file))
			node := &treeAgent{id: agentID, agentType: "?", file: file}
			meta, metaErr := transcript.ReadSubagentMeta(transcript.SubagentMetaPath(file))
			if metaErr != nil {
				// its type and parent are unknown, so its place in the tree is too
				node.unreadable = "meta: " + metaErr.Error()
			} else {
				node.agentType, node.parent = meta.AgentType, firstText(meta.ParentAgentID, tree.session)
			}
			nodes[agentID] = node
			order = append(order, node)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, failure("--tree %s: stat %s: %v", id, subDir, statErr)
	}
	rootID := tree.session
	if isAgent {
		tree.root, rootID = treeRootAgent, id
	} else {
		tree.agents = append(tree.agents, main)
	}
	for _, node := range order {
		if node.unreadable != "" {
			node.depth = -1
			if node.id == rootID {
				node.depth = 0
			}
			tree.agents = append(tree.agents, node)
			continue
		}
		if depth, inside := depthUnder(node, rootID, tree.session, nodes); inside {
			node.depth = depth
			tree.agents = append(tree.agents, node)
		}
	}
	tree.scope = a.opts.worktree
	if tree.scope == "" {
		tree.scope = firstCWD(sessionFile)
	}
	if tree.scope == "" {
		tree.scope = firstCWD(path)
	}
	if tree.scope == "" {
		return nil, failure("--tree %s: %s records no cwd to count written bytes under; pass --worktree DIR --base SHA",
			id, sessionFile)
	}
	if absolute, absErr := filepath.Abs(tree.scope); absErr == nil {
		tree.scope = absolute
	}
	return tree, nil
}

// depthUnder is node's distance below rootID along its parent chain, the
// session's main agent standing above every agent no parent names; inside is
// false when the chain never reaches rootID (or loops).
func depthUnder(node *treeAgent, rootID, session string, nodes map[string]*treeAgent) (int, bool) {
	depth := 0
	seen := map[string]bool{}
	for at := node; ; {
		if at.id == rootID {
			return depth, true
		}
		if at.parent == "" || at.parent == session {
			return depth + 1, rootID == session
		}
		if seen[at.id] {
			return 0, false
		}
		seen[at.id] = true
		next, known := nodes[at.parent]
		if !known {
			return 0, false
		}
		at = next
		depth++
	}
}

// firstCWD is the first cwd a transcript records, "" when it records none or
// cannot be read.
func firstCWD(file string) string {
	cwd := ""
	_ = eachLine(file, func(raw []byte) {
		if cwd != "" {
			return
		}
		if line, ok := transcript.DecodeClaudeRecord(raw); ok {
			cwd = jsString(line.CWD)
		}
	})
	return cwd
}

// measure reads one agent's transcript: its replayed tokens, dollars and
// model, then its written and read bytes.
func (a *auditor) measure(node *treeAgent, scope string) {
	if node.unreadable != "" {
		return
	}
	c, err := a.collect(node.file, nil)
	if err != nil {
		node.unreadable = describe(err)
		return
	}
	node.model = noModel
	zero := 0.0
	node.usd = &zero
	if r := a.replay(node.file, c); r != nil {
		node.in, node.cacheRead, node.cacheWrite = r.tok.in, r.tok.cr, r.tok.cw5+r.tok.cw1
		node.model = firstText(r.model, noModel)
		if r.unpriced || r.advisor.unpriced != 0 {
			node.usd = nil
		} else {
			usd := r.usd + r.advisor.usd
			node.usd = &usd
		}
	}
	if node.written, node.read, err = toolBytes(node.file, scope); err != nil {
		node.unreadable = describe(err)
	}
}

// toolBytes sums the bytes an agent wrote under scope through its own tool
// calls and the text bytes of every tool result it received, each tool_use
// and tool_result counted once however many lines repeat it.
func toolBytes(file, scope string) (written, read int64, err error) {
	uses, results := map[string]bool{}, map[string]bool{}
	err = eachLine(file, func(raw []byte) {
		line, ok := transcript.DecodeClaudeRecord(raw)
		if !ok {
			return
		}
		kind, _ := line.Kind()
		message := line.DecodeMessage()
		if message == nil {
			return
		}
		list, _ := transcript.DecodeContent(message.Content)
		for k := range list {
			block := &list[k]
			switch {
			case kind == transcript.RoleAssistant && block.Kind() == "tool_use":
				if id := jsString(block.ID); id == "" || !uses[id] {
					uses[id] = id != ""
					written += writtenBytes(jsString(block.Name), block.Input, scope)
				}
			case kind == "user" && block.Kind() == "tool_result":
				if id := jsString(block.ToolUseID); id == "" || !results[id] {
					results[id] = id != ""
					read += resultBytes(block.Content)
				}
			}
		}
	})
	return written, read, err
}

// writeInput is the members of a file-writing tool's input the count reads.
type writeInput struct {
	FilePath     json.RawMessage `json:"file_path"`
	NotebookPath json.RawMessage `json:"notebook_path"`
	Content      json.RawMessage `json:"content"`
	NewString    json.RawMessage `json:"new_string"`
	NewSource    json.RawMessage `json:"new_source"`
	Edits        json.RawMessage `json:"edits"`
}

// writtenBytes is the bytes one tool call wrote under scope: 0 for any
// other tool, or a target outside scope or under /tmp.
func writtenBytes(name string, raw json.RawMessage, scope string) int64 {
	var input writeInput
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return 0
	}
	target, size := jsString(input.FilePath), int64(0)
	switch name {
	case "Write":
		size = int64(len(jsString(input.Content)))
	case "Edit":
		size = int64(len(jsString(input.NewString)))
	case "MultiEdit":
		var edits []struct {
			NewString json.RawMessage `json:"new_string"`
		}
		if json.Unmarshal(input.Edits, &edits) == nil {
			for _, edit := range edits {
				size += int64(len(jsString(edit.NewString)))
			}
		}
	case "NotebookEdit":
		target = firstText(jsString(input.NotebookPath), target)
		size = int64(len(jsString(input.NewSource)))
	default:
		return 0
	}
	if !underScope(target, scope) {
		return 0
	}
	return size
}

// underScope reports path inside scope and outside /tmp and /private/tmp; a
// relative path reads against scope.
func underScope(path, scope string) bool {
	if path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(scope, path)
	}
	path = filepath.Clean(path)
	for _, tmp := range []string{"/tmp", "/private/tmp"} {
		if path == tmp || strings.HasPrefix(path, tmp+"/") {
			return false
		}
	}
	rel, err := filepath.Rel(scope, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// resultBytes is the byte length of a tool result's text: a string as
// itself, a block list by its text blocks; an image carries none.
func resultBytes(content json.RawMessage) int64 {
	if isString(content) {
		return int64(len(jsString(content)))
	}
	list, _ := transcript.DecodeContent(content)
	total := int64(0)
	for k := range list {
		if list[k].Kind() == blockText {
			total += int64(len(jsString(list[k].Text)))
		}
	}
	return total
}

// runTree is --tree: the tree built and measured, its landed lines counted
// when --worktree names the checkout, the report printed and written.
func (a *auditor) runTree(ctx context.Context) (int, error) {
	a.since = 0 // the whole tree, whatever the window
	tree, err := a.buildTree(a.opts.tree)
	if err != nil {
		return 1, err
	}
	for _, node := range tree.agents {
		a.measure(node, tree.scope)
	}
	var landed *landedLines
	if a.opts.worktree != "" {
		if landed, err = measureLanded(ctx, obs.Runner(nil), a.opts.worktree, a.opts.base); err != nil {
			return 1, failure("--tree %s: landed lines: %v", tree.target, err)
		}
	}
	document := a.treeDocument(tree, landed)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return 1, failure("--tree %s: encode the report: %v", tree.target, err)
	}
	encoded = append(encoded, '\n')
	if a.opts.json {
		if _, err := a.stdout.Write(encoded); err != nil {
			return 1, failure("--tree %s: write the report: %v", tree.target, err)
		}
	} else {
		a.printTree(document)
	}
	if a.opts.out != "" {
		size, err := writeOutput(a.opts.out, string(encoded))
		if err != nil {
			return 1, err
		}
		if !a.opts.json {
			a.print(fmt.Sprintf("\nfull data → %s (%d bytes)", a.opts.out, size))
		}
	}
	if document.Tree.NotRead != 0 {
		return 1, nil
	}
	return 0, nil
}

// treeDocument is the measured tree as its JSON document.
type treeDocument struct {
	Excluded string     `json:"excluded"`
	Tree     treeTotals `json:"tree"`
	Types    []treeType `json:"types"`
	Agents   []treeRow  `json:"agents"`
}

type treeTotals struct {
	Target                string          `json:"target"`
	Root                  string          `json:"root"`
	Session               string          `json:"session"`
	WriteScope            string          `json:"writeScope"`
	Agents                int             `json:"agents"`
	NotRead               int             `json:"notRead"`
	InputTokens           int64           `json:"inputTokens"`
	UncachedInputTokens   int64           `json:"uncachedInputTokens"`
	CacheReadTokens       int64           `json:"cacheReadTokens"`
	CacheWriteTokens      int64           `json:"cacheWriteTokens"`
	USD                   *float64        `json:"usd"`
	UnpricedAgents        int             `json:"unpricedAgents"`
	WrittenBytes          int64           `json:"writtenBytes"`
	ReadBytes             int64           `json:"readBytes"`
	SilentShare           *float64        `json:"silentShare"`
	Worktree              *string         `json:"worktree"`
	Base                  *string         `json:"base"`
	LandedLines           *int64          `json:"landedLines"`
	LandedPerMillionInput *float64        `json:"landedPerMillionInput"`
	LandedExcluded        *landedExcluded `json:"landedExcluded"`
	LandedNotMeasured     *string         `json:"landedNotMeasured"`
}

type landedExcluded struct {
	TestLines      int64 `json:"testLines"`
	DocLines       int64 `json:"docLines"`
	GeneratedLines int64 `json:"generatedLines"`
	LockLines      int64 `json:"lockLines"`
	BinaryFiles    int64 `json:"binaryFiles"`
}

type treeType struct {
	Type             string   `json:"type"`
	Agents           int      `json:"agents"`
	InputTokens      int64    `json:"inputTokens"`
	WrittenBytes     int64    `json:"writtenBytes"`
	ReadBytes        int64    `json:"readBytes"`
	WrittenReadRatio *float64 `json:"writtenReadRatio"`
}

type treeRow struct {
	ID                  string   `json:"id"`
	Type                string   `json:"type"`
	Parent              *string  `json:"parent"`
	Depth               *int     `json:"depth"`
	Model               string   `json:"model"`
	InputTokens         int64    `json:"inputTokens"`
	UncachedInputTokens int64    `json:"uncachedInputTokens"`
	CacheReadTokens     int64    `json:"cacheReadTokens"`
	CacheWriteTokens    int64    `json:"cacheWriteTokens"`
	USD                 *float64 `json:"usd"`
	WrittenBytes        int64    `json:"writtenBytes"`
	ReadBytes           int64    `json:"readBytes"`
	WrittenReadRatio    *float64 `json:"writtenReadRatio"`
	Unreadable          *string  `json:"unreadable"`
}

// ratio is written/read, nil when nothing was read.
func ratio(written, read int64) *float64 {
	if read == 0 {
		return nil
	}
	value := float64(written) / float64(read)
	return &value
}

func (a *auditor) treeDocument(tree *spawnTree, landed *landedLines) treeDocument {
	agents := append([]*treeAgent(nil), tree.agents...)
	sort.SliceStable(agents, func(i, j int) bool {
		left, right := agents[i], agents[j]
		if (left.unreadable == "") != (right.unreadable == "") {
			return left.unreadable == ""
		}
		if left.input() != right.input() {
			return left.input() > right.input()
		}
		if left.depth != right.depth {
			return left.depth < right.depth
		}
		return left.id < right.id
	})
	totals := treeTotals{
		Target: tree.target, Root: tree.root, Session: tree.session, WriteScope: tree.scope, Agents: len(agents),
	}
	types := map[string]*treeType{}
	var typeOrder []string
	var usd, silent float64
	priced := 0
	rows := make([]treeRow, 0, len(agents))
	for _, node := range agents {
		row := treeRow{ID: node.id, Type: node.agentType, Model: node.model, USD: node.usd}
		if node.parent != "" {
			parent := node.parent
			row.Parent = &parent
		}
		if node.depth >= 0 {
			depth := node.depth
			row.Depth = &depth
		}
		if node.unreadable != "" {
			why := node.unreadable
			row.Unreadable, row.USD = &why, nil
			totals.NotRead++
			rows = append(rows, row)
			continue
		}
		row.InputTokens, row.UncachedInputTokens = int64(node.input()), int64(node.in)
		row.CacheReadTokens, row.CacheWriteTokens = int64(node.cacheRead), int64(node.cacheWrite)
		row.WrittenBytes, row.ReadBytes = node.written, node.read
		row.WrittenReadRatio = ratio(node.written, node.read)
		rows = append(rows, row)
		totals.InputTokens += row.InputTokens
		totals.UncachedInputTokens += row.UncachedInputTokens
		totals.CacheReadTokens += row.CacheReadTokens
		totals.CacheWriteTokens += row.CacheWriteTokens
		totals.WrittenBytes += node.written
		totals.ReadBytes += node.read
		if node.usd == nil {
			totals.UnpricedAgents++
		} else {
			usd += *node.usd
			priced++
		}
		if node.written == 0 {
			silent += node.input()
		}
		group, known := types[node.agentType]
		if !known {
			group = &treeType{Type: node.agentType}
			types[node.agentType] = group
			typeOrder = append(typeOrder, node.agentType)
		}
		group.Agents++
		group.InputTokens += row.InputTokens
		group.WrittenBytes += node.written
		group.ReadBytes += node.read
	}
	if priced != 0 || totals.UnpricedAgents == 0 {
		totals.USD = &usd
	}
	if totals.InputTokens != 0 {
		share := silent / float64(totals.InputTokens)
		totals.SilentShare = &share
	}
	if landed == nil {
		reason := landedNotMeasured
		totals.LandedNotMeasured = &reason
	} else {
		worktree, base, lines := a.opts.worktree, a.opts.base, landed.code
		totals.Worktree, totals.Base, totals.LandedLines = &worktree, &base, &lines
		totals.LandedExcluded = &landedExcluded{
			TestLines: landed.tests, DocLines: landed.docs, GeneratedLines: landed.generated, LockLines: landed.locks,
			BinaryFiles: landed.binaryFiles,
		}
		if totals.InputTokens != 0 {
			perMillion := float64(lines) / (float64(totals.InputTokens) / 1e6)
			totals.LandedPerMillionInput = &perMillion
		}
	}
	typeRows := make([]treeType, 0, len(typeOrder))
	for _, name := range typeOrder {
		group := types[name]
		group.WrittenReadRatio = ratio(group.WrittenBytes, group.ReadBytes)
		typeRows = append(typeRows, *group)
	}
	sort.SliceStable(typeRows, func(i, j int) bool { return typeRows[i].InputTokens > typeRows[j].InputTokens })
	return treeDocument{Excluded: landedRules, Tree: totals, Types: typeRows, Agents: rows}
}

// printTree is the text report: the tree's numbers first, then written/read
// per agent type, then one row per agent, the most input first.
func (a *auditor) printTree(doc treeDocument) {
	t := doc.Tree
	head := "TOKEN TREE · session " + t.Session
	if t.Root == treeRootAgent {
		head = "TOKEN TREE · agent " + t.Target + " of session " + t.Session
	}
	a.print(fmt.Sprintf("%s · %d agents · writes counted under %s", head, t.Agents, t.WriteScope))
	if t.LandedLines == nil {
		a.print(treeLine("landed code lines", landedNotMeasured))
		a.print(treeLine("landed lines per M input", landedNotMeasured))
	} else {
		x := t.LandedExcluded
		a.print(treeLine("landed code lines", fmt.Sprintf(
			"%d (%s against %s; %s excluded: tests %d · docs %d · generated %d · locks %d lines · binary %d files)",
			*t.LandedLines, *t.Worktree, *t.Base, doc.Excluded, x.TestLines, x.DocLines, x.GeneratedLines,
			x.LockLines, x.BinaryFiles)))
		a.print(treeLine("landed lines per M input", optional(t.LandedPerMillionInput, "%.2f")))
	}
	silentAgents := 0
	for i := range doc.Agents {
		row := &doc.Agents[i]
		if row.Unreadable == nil && row.WrittenBytes == 0 {
			silentAgents++
		}
	}
	share := notPriced
	if t.SilentShare != nil {
		share = fmt.Sprintf("%.1f%%", *t.SilentShare*100)
	}
	a.print(
		treeLine("silent share", fmt.Sprintf("%s of input tokens (%d of %d read agents wrote nothing under the scope)",
			share, silentAgents, t.Agents-t.NotRead)),
	)
	usd := usdOrNA(t.USD)
	if t.UnpricedAgents != 0 {
		usd += fmt.Sprintf(" (%d agents unpriced, not in the sum)", t.UnpricedAgents)
	}
	a.print(treeLine("input tokens", fmt.Sprintf("%d (uncached %d · cache read %d · cache write %d) · %s",
		t.InputTokens, t.UncachedInputTokens, t.CacheReadTokens, t.CacheWriteTokens, usd)))
	a.print(treeLine("written / read bytes", fmt.Sprintf("%d / %d", t.WrittenBytes, t.ReadBytes)))
	if t.NotRead != 0 {
		var why []string
		for i := range doc.Agents {
			row := &doc.Agents[i]
			if row.Unreadable != nil {
				why = append(why, row.ID+": "+*row.Unreadable)
			}
		}
		a.print(treeLine("NOT READ", fmt.Sprintf("%d agents — %s", t.NotRead, strings.Join(why, "; "))))
	}
	a.print("\n== WRITTEN / READ BY AGENT TYPE")
	a.print(fmt.Sprintf("  %-28s %6s %12s %10s %12s %8s", "TYPE", "AGENTS", "INPUT TOK", "WRITTEN", "READ", "W/R"))
	for _, row := range doc.Types {
		a.print(fmt.Sprintf("  %-28s %6d %12d %10d %12d %8s", row.Type, row.Agents, row.InputTokens,
			row.WrittenBytes, row.ReadBytes, optional(row.WrittenReadRatio, "%.3f")))
	}
	a.print("\n== AGENTS (the most input tokens first)")
	a.print(fmt.Sprintf("  %5s  %-36s %-24s %-22s %12s %12s %12s %10s %12s %8s %10s", "DEPTH", "AGENT", "TYPE",
		"MODEL", "INPUT TOK", "CACHE READ", "CACHE WRITE", "WRITTEN", "READ", "W/R", "USD"))
	for i := range doc.Agents {
		row := &doc.Agents[i]
		depth := "?"
		if row.Depth != nil {
			depth = fmt.Sprint(*row.Depth)
		}
		lead := fmt.Sprintf("  %5s  %-36s %-24s", depth, row.ID, row.Type)
		if row.Unreadable != nil {
			a.print(lead + " unreadable: " + *row.Unreadable)
			continue
		}
		a.print(fmt.Sprintf("%s %-22s %12d %12d %12d %10d %12d %8s %10s", lead, row.Model, row.InputTokens,
			row.CacheReadTokens, row.CacheWriteTokens, row.WrittenBytes, row.ReadBytes,
			optional(row.WrittenReadRatio, "%.3f"), usdOrNA(row.USD)))
	}
	a.print("\n" + a.gapsLine())
}

func treeLine(label, value string) string { return fmt.Sprintf("%-26s %s", label, value) }

// optional formats a nullable number, n/a when absent.
func optional(value *float64, format string) string {
	if value == nil {
		return notPriced
	}
	return fmt.Sprintf(format, *value)
}

// usdOrNA is a nullable dollar amount, n/a when unpriced, never $0.
func usdOrNA(value *float64) string {
	if value == nil {
		return notPriced
	}
	return fmt.Sprintf("$%.4f", *value)
}
