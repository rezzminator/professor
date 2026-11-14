package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// The tab probe's title inputs, the names no hook announces: a tabbed Claude
// chat's rename or AI title its transcript gains past the fleet scan's read
// of it (transcriptRenamed), and a tabbed OpenCode chat's title in its store
// (writeOpenCodeTitles).

// tabTitleSeed is how far before the scan's read a transcript's first title
// read starts: Claude restates its titles every few turns, so the values the
// scan saw sit there, and their restatement past it is no rename.
const tabTitleSeed = 512 << 10

// tabTranscriptRead is a tabbed Claude transcript's title read: how far it
// went (offset), where the fleet scan's read of it stopped (scanned, its
// row's size), the last rename and AI title met, and where the last line
// that changed one of them starts (moved, -1: none).
type tabTranscriptRead struct {
	offset, scanned, moved int64
	custom, ai             string
}

// scannedTranscript records where the scan's read of target's transcript
// stopped (scanned), or its size now when the row names no size the file
// still holds; a first read starts tabTitleSeed before it.
func (probe *tabProbe) scannedTranscript(target *headless.Chat, scanned, size int64) {
	if target.Engine != pfmengine.Claude {
		return // Codex and OpenCode titles live outside the transcript
	}
	if scanned <= 0 || scanned > size {
		scanned = size
	}
	read, held := probe.transcriptReads[target.Path]
	if !held {
		read = tabTranscriptRead{offset: max(0, scanned-tabTitleSeed), moved: -1}
	}
	read.scanned = scanned
	if probe.transcriptReads == nil {
		probe.transcriptReads = map[string]tabTranscriptRead{}
	}
	probe.transcriptReads[target.Path] = read
}

// transcriptRenamed reads path's complete lines past its last read, up to
// size, and says "renamed" when a line past the scan's read changed its
// rename or AI title: a name the scanned rows lack. A transcript that shrank
// was rewritten, so it is renamed until the next scan reads it, its title read
// starting over; a read that fails keeps its offset, so the next probe reads
// again.
func (probe *tabProbe) transcriptRenamed(path string, size int64) string {
	read, held := probe.transcriptReads[path]
	if !held {
		return ""
	}
	if size < read.offset { // rewritten: a new file, whose name the scan must read
		read = tabTranscriptRead{offset: max(0, size-tabTitleSeed), scanned: -1, moved: -1}
	}
	if size > read.offset {
		at := read.offset
		offset, _, err := transcript.ReadLines(path, read.offset, func(line []byte, complete bool) {
			start := at
			at += int64(len(line)) + 1
			if complete {
				read.note(line, start)
			}
		})
		if err == nil {
			read.offset = offset
		}
	}
	probe.transcriptReads[path] = read
	if read.moved >= read.scanned {
		return "renamed"
	}
	return ""
}

// note keeps the title the line starting at start names: a rename's
// (transcript.LineTitle) or Claude's AI title.
func (read *tabTranscriptRead) note(line []byte, start int64) {
	value := &read.custom
	title, ok := transcript.LineTitle(line)
	if !ok {
		value = &read.ai
		title, ok = tabTranscriptAITitle(line)
	}
	if ok && *value != title {
		*value, read.moved = title, start
	}
}

// tabTranscriptAITitle is the AI title one Claude transcript line names.
func tabTranscriptAITitle(line []byte) (string, bool) {
	if !bytes.Contains(line, []byte(`"ai-title"`)) {
		return "", false
	}
	record, ok := transcript.DecodeClaudeRecord(line)
	if kind, _ := record.Kind(); !ok || kind != "ai-title" {
		return "", false
	}
	var title string
	if json.Unmarshal(record.AITitle, &title) != nil || title == "" {
		return "", false
	}
	return title, true
}

// forgetTranscriptReads drops the title read of every transcript the
// enumeration no longer tabs.
func (probe *tabProbe) forgetTranscriptReads() {
	tabbed := map[string]bool{}
	for i := range probe.tabbed {
		tabbed[probe.tabbed[i].Path] = true
	}
	maps.DeleteFunc(probe.transcriptReads, func(path string, _ tabTranscriptRead) bool { return !tabbed[path] })
}

// openCodeIDs are the tabbed OpenCode chats, sorted: the chats whose titles
// a fingerprint carries.
func (probe *tabProbe) openCodeIDs() []string {
	var ids []string
	for i := range probe.tabbed {
		if target := &probe.tabbed[i]; target.Engine == pfmengine.OpenCode && target.Path == "" {
			ids = append(ids, target.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

// writeOpenCodeTitles renders the titles of the OpenCode chats ids names as
// their store holds them: OpenCode writes no transcript, and the title its
// tab paints lands only there. Only tabbed chats' rows are read, so another
// chat's turn is no change. A store that cannot be read carries the error in
// its line, so a lasting failure reads as no change.
func (probe *tabProbe) writeOpenCodeTitles(ctx context.Context, out *strings.Builder, ids []string) {
	if len(ids) == 0 {
		return
	}
	for _, root := range probe.openCodeRoots {
		titles, err := index.ReadOpenCodeTitles(ctx, root, ids)
		if errors.Is(err, index.ErrNoOpenCodeStore) {
			continue
		}
		if err != nil {
			writeTabInput(out, "title", strconv.Quote(root), "", fmt.Sprintf("error %q", err))
			continue
		}
		for _, id := range ids {
			if title, found := titles[id]; found {
				writeTabInput(out, "title", strconv.Quote(root)+" "+strconv.Quote(id), "", strconv.Quote(title))
			}
		}
	}
}
