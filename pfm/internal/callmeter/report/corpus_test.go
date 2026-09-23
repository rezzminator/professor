package report

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	"github.com/rezzminator/professor/pfm/internal/callmeter/backfill"
	"github.com/rezzminator/professor/pfm/internal/callmeter/cmdparse"
)

// corpusDir holds two real sessions of this repository (a main chat with six
// sub-agents, a main chat with one), sanitized by internal/callmeter/corpussan
// (docs/design/hooks/callmeter.md § "Test corpora", item 2).
const corpusDir = "testdata/corpus"

// corpusStore copies the corpus into a temp Claude config dir, moving its
// /tmp/demo-proj and /tmp/demo-home roots under a fresh temp root (nothing of
// either exists there, so every attribution is the parse's alone), and
// backfills it into a fresh store. It returns the store, the temp root and
// the backfill summary; a fixture that fails to load fails the test by name.
func corpusStore(t *testing.T) (*callmeter.Store, string, backfill.Summary) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	config := filepath.Join(root, "config")
	remap := strings.NewReplacer("/tmp/demo-proj", root+"/demo-proj", "/tmp/demo-home", root+"/demo-home")
	var copied int
	err = filepath.WalkDir(corpusDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(corpusDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(config, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		copied++
		return os.WriteFile(dst, []byte(remap.Replace(string(raw))), 0o600)
	})
	if err != nil {
		t.Fatalf("load corpus fixture %s: %v", corpusDir, err)
	}
	if copied != 16 {
		t.Fatalf(
			"corpus fixture %s holds %d files, want 16 (2 sessions, 7 sub-agents with their meta)",
			corpusDir,
			copied,
		)
	}
	// The small file set that exists at parse time, relative to the temp root:
	// a cd-resolved grep target, the file a stat, a grep and a python3 name,
	// and beside it a second agent log and a meta file for a loop's glob.
	for _, rel := range corpusExisting {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte("line\n"), 0o600); err != nil {
			t.Fatalf("create %s: %v", rel, err)
		}
	}
	store := openStore(t)
	sum, err := backfill.FromTranscripts(context.Background(), store, []string{config}, time.Time{}, t.Logf)
	if err != nil {
		t.Fatalf("FromTranscripts over %s: %v", corpusDir, err)
	}
	if len(sum.Unreadable) != 0 || len(sum.Malformed) != 0 {
		t.Fatalf("corpus backfill reported problems:\n%s", sum)
	}
	return store, root, sum
}

const (
	corpusPipeline = "demo-proj/pfm/internal/picker/pipeline.go"
	corpusAgentLog = "demo-home/.claude/projects/-tmp-demo-home-work-xxxxxxx/xxxxxxxx-2039-xxxx-8387-xxxxxxxxxxxx/subagents/agent-xxxxxxxxxxxxxxxxx.jsonl"

	corpusSubagents = "demo-home/.claude/projects/-tmp-demo-home-work-xxxxxxx/xxxxxxxx-2039-xxxx-8387-xxxxxxxxxxxx/subagents/"
)

// corpusExisting: the agent logs a `subagents/*.jsonl` loop matches, and a
// meta file beside them it must not.
var corpusExisting = []string{
	corpusPipeline, corpusAgentLog,
	corpusSubagents + "agent-aaaaaaaaaaaaaaaaa.jsonl", corpusSubagents + "agent-aaaaaaaaaaaaaaaaa.meta.json",
}

// corpusCount runs one COUNT query.
func corpusCount(t *testing.T, store *callmeter.Store, query string) int64 {
	t.Helper()
	var n int64
	if err := store.DB().QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// corpusRows runs a query of text columns and joins each row with spaces.
func corpusRows(t *testing.T, store *callmeter.Store, query string) []string {
	t.Helper()
	rows, err := store.DB().QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("%s close rows: %v", query, err)
		}
	}()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("%s columns: %v", query, err)
	}
	var out []string
	for rows.Next() {
		values := make([]string, len(cols))
		targets := make([]any, len(cols))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatalf("%s scan: %v", query, err)
		}
		out = append(out, strings.Join(values, " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s rows: %v", query, err)
	}
	return out
}

func TestCorpusBackfillCountsCallsRequestsAgentsAndFailures(t *testing.T) {
	store, _, sum := corpusStore(t)
	if sum.TranscriptsRead != 9 || sum.CallsInserted != 266 || sum.CallsFilled != 0 || sum.Requests != 195 ||
		sum.Agents != 7 {
		t.Errorf("summary = %s\nwant 9 transcripts, 266 calls inserted, 0 filled, 195 requests, 7 agents", sum)
	}
	for query, want := range map[string]int64{
		"SELECT COUNT(*) FROM calls":                                       266,
		"SELECT COUNT(*) FROM requests":                                    195,
		"SELECT COUNT(*) FROM agents":                                      7,
		"SELECT COUNT(DISTINCT session_id) FROM calls":                     2,
		"SELECT COUNT(DISTINCT agent_id) FROM calls WHERE agent_id != ''":  7,
		"SELECT COUNT(*) FROM calls WHERE bytes_delivered IS NULL":         0,
		"SELECT COUNT(*) FROM agents WHERE parent_tool_use_id IS NOT NULL": 7,
		"SELECT COUNT(*) FROM calls WHERE failed = 1":                      5,
		"SELECT COUNT(*) FROM calls WHERE error LIKE 'malformed input: %'": 1,
	} {
		if got := corpusCount(t, store, query); got != want {
			t.Errorf("%s = %d, want %d", query, got, want)
		}
	}
	gotTools := corpusRows(t, store, "SELECT tool, COUNT(*) FROM calls GROUP BY tool ORDER BY tool")
	if want := []string{"Agent 7", "Bash 221", "Read 36", "Write 2"}; fmt.Sprint(gotTools) != fmt.Sprint(want) {
		t.Errorf("calls per tool = %v, want %v", gotTools, want)
	}
	// The one Read the harness rejected (an offset given as an array) is a
	// failed call, not a dropped line.
	// The input's byte count in the message follows the temp root's length.
	gotBad := corpusRows(t, store, "SELECT tool_use_id, tool, error FROM calls WHERE error LIKE 'malformed input: %'")
	wantBad := regexp.MustCompile(
		`^toolu_01XnofzHmu8GRve2nSJuKoE1 Read malformed input: decode Read input \(\d+ bytes\): ` +
			`json: cannot unmarshal array into Go struct field \.offset of type float64$`,
	)
	if len(gotBad) != 1 || !wantBad.MatchString(gotBad[0]) {
		t.Errorf("malformed calls = %q, want one matching %s", gotBad, wantBad)
	}
	gotFailed := corpusRows(t, store, "SELECT tool_use_id, tool FROM calls WHERE failed = 1 ORDER BY tool_use_id")
	if want := []string{
		"toolu_017rdKVrdXMi4UVUBLS1NdBb Bash",
		"toolu_019c1MaRZCU3DXrbh17pgrTK Bash",
		"toolu_01ArAhqfBHyvjnXzVqo7JHqX Bash",
		"toolu_01KdRTfpaFQ2PaT9wHv7y4qX Bash",
		"toolu_01XnofzHmu8GRve2nSJuKoE1 Read",
	}; fmt.Sprint(gotFailed) != fmt.Sprint(want) {
		t.Errorf("failed calls = %q, want %q", gotFailed, want)
	}
}

// corpusAttributions lists the files of one call's parts as
// "program action range exists path", the temp root cut to {R}.
func corpusAttributions(t *testing.T, parts []storedPart, root string) []string {
	t.Helper()
	var out []string
	for _, p := range parts {
		for _, f := range p.files {
			exists := "0"
			if f.Exists {
				exists = "1"
			}
			out = append(
				out,
				strings.Join(
					[]string{p.program, f.Action, f.Range, exists, strings.Replace(f.Path, root, "{R}", 1)},
					" ",
				),
			)
		}
	}
	return out
}

func TestCorpusParseStatusAttributionsAndByteShares(t *testing.T) {
	ctx := context.Background()
	store, root, _ := corpusStore(t)
	parsed, err := EnsureParsed(ctx, store, "", cmdparse.Python3{})
	if err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	if parsed != (ParseSummary{Parsed: 221}) {
		t.Errorf("EnsureParsed = %+v, want all 221 Bash calls parsed", parsed)
	}
	gotStatus := corpusRows(
		t,
		store,
		"SELECT lang, parse_status, COUNT(*) FROM command_parts GROUP BY lang, parse_status ORDER BY lang, parse_status",
	)
	if want := []string{"python error 1", "python ok 9", "sh ok 1902"}; fmt.Sprint(gotStatus) != fmt.Sprint(want) {
		t.Errorf("parts per lang and status = %q, want %q", gotStatus, want)
	}
	gotFaults := corpusRows(t, store, "SELECT tool_use_id FROM faults WHERE stage = 'parse'")
	if want := []string{"toolu_019yMBoTNuoVPms7Rp1dvAeN"}; fmt.Sprint(gotFaults) != fmt.Sprint(want) {
		t.Errorf("parse faults = %q, want %q", gotFaults, want)
	}

	all, err := loadParts(ctx, store, Filter{})
	if err != nil {
		t.Fatalf("loadParts: %v", err)
	}
	named := map[string]struct {
		why      string
		programs string
		files    []string
	}{
		"toolu_01U1nCg86aTEdGZLo9iVp4ZU": {
			why:      "a time wrapper and a redirect write of a missing file",
			programs: "cd,git,echo,tail",
			files: []string{
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/mirror-clone.log",
				"echo write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/mirror-clone.log",
				"tail read-range -5 0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/mirror-clone.log",
			},
		},
		"toolu_01DZRF89Th7yuTvY5dxUf3Zm": {
			why:      "a cd chain, a timeout wrapper and a head range",
			programs: "cd,git,tail,echo,git,git,[,git,cut,echo,echo,echo,git,head,read,git,git,head,echo,echo,ls,head",
			files: []string{
				"head read-range 1,5 0 {R}/demo-home/work/backup/xxxxxxxx-local-before-xxxxxx-2026-09-01-xxxxxxx.xxxxx",
			},
		},
		"toolu_01YUeMT1wPoVe4AsCnW1fDin": {
			why:      "a relative grep resolved against a cd",
			programs: "cd,grep",
			files: []string{
				"grep search  1 {R}/demo-proj/pfm/internal/picker/pipeline.go",
			},
		},
		"toolu_01MaAMWMKnMTqyzjj5922hGw": {
			why:      "a cd list, a redirect write of a missing file and searches",
			programs: "cd,sqlite3,sort,mkdir,sqlite3,echo,echo,grep,head,echo,grep,head",
			files: []string{
				"sort write  0 {R}/demo-home/tmp/claude-501/-tmp-demo-proj/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx/xxxxxxxxxx/h.txt",
				"grep search  0 {R}/demo-proj/pfm/internal/store/queries.go",
				"grep search  0 {R}/demo-proj/pfm/internal/store",
				"grep search  0 {R}/demo-proj/pfm/internal/compose",
				"grep search  0 {R}/demo-proj/pfm/internal/index",
				"grep search  0 {R}/demo-proj/pfm/internal/fleet",
			},
		},
		"toolu_01EZuNooRu2PPHxGd5PYn4rS": {
			why:      "a stat, a grep and a python3 argument naming one existing file",
			programs: "stat,grep,head,echo,python3",
			files: []string{
				"stat stat  1 {R}/demo-home/.claude/projects/-tmp-demo-home-work-xxxxxxx/xxxxxxxx-2039-xxxx-8387-xxxxxxxxxxxx/subagents/agent-xxxxxxxxxxxxxxxxx.jsonl",
				"grep search  1 {R}/demo-home/.claude/projects/-tmp-demo-home-work-xxxxxxx/xxxxxxxx-2039-xxxx-8387-xxxxxxxxxxxx/subagents/agent-xxxxxxxxxxxxxxxxx.jsonl",
				"python3 unknown  1 {R}/demo-home/.claude/projects/-tmp-demo-home-work-xxxxxxx/xxxxxxxx-2039-xxxx-8387-xxxxxxxxxxxx/subagents/agent-xxxxxxxxxxxxxxxxx.jsonl",
			},
		},
		"toolu_01QXLsMEZJGNuPcEqtXe7ET9": {
			why:      "a Python heredoc opening literal paths",
			programs: "sort,sort,diff,echo,echo,cd,git,sort,uniq,git,awk,sort,sort,xxxx,wc,echo,grep,python3",
			files: []string{
				"git read-whole  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/xxxxx-blob-xxx.txt",
				"sort write  0 {R}/demo-home/private/tmp/claude-501/-tmp-demo-proj/xxxxxxxx-xxxx-4151-xxxx-xxxxxxxxxxxx/xxxxxxxxxx/head-xxxxx.txt",
				"grep search  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/analysis/blob-xxxx-and-paths.txt",
				"python3 read-whole  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/analysis/blob-xxxx-and-paths.txt",
				"python3 read-whole  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/xxxxx-blob-xxx.txt",
			},
		},
		"toolu_01FfKtutigR8aA1hrNEvn4Qd": {
			why:      "a heredoc written through an assigned path, then a Python script run",
			programs: "cat,cd,python3,echo,awk,sort,head,git,wc,git,head,git",
			files: []string{
				"cat write  0 {R}/demo-home/private/tmp/claude-501/-tmp-demo-proj/xxxxxxxx-xxxx-4151-xxxx-xxxxxxxxxxxx/xxxxxxxxxx/xxxxxxxx.py",
				"python3 read-whole  0 {R}/demo-home/private/tmp/claude-501/-tmp-demo-proj/xxxxxxxx-xxxx-4151-xxxx-xxxxxxxxxxxx/xxxxxxxxxx/xxxxxxx-xxxxx.txt",
				"python3 write  0 {R}/demo-home/private/tmp/claude-501/-tmp-demo-proj/xxxxxxxx-xxxx-4151-xxxx-xxxxxxxxxxxx/xxxxxxxxxx/xxxxxxx-class.txt",
			},
		},
		"toolu_011mtxBnU6AjFC81eW7wcCjM": {
			why:      "a docker exec sh -c string, never parsed",
			programs: "echo,docker,docker,cut,docker,head",
		},
		"toolu_019yMBoTNuoVPms7Rp1dvAeN": {
			why:      "a Python heredoc its parser rejects",
			programs: "cd,git,python3,git,awk,head,echo,git,awk,wc,echo,git,awk,wc,echo",
		},
		"toolu_01DH6P3TMfnnpQW2gD4KXcz4": {
			why:      "find piped to an xargs-wrapped grep, its files on stdin",
			programs: "find,grep",
		},
		"toolu_015heLL81qsQ95br7c1fhpHz": {
			why:      "a loop over a glob: every matching agent log, never the meta file beside them",
			programs: "grep,echo",
			files: []string{
				"grep search  1 {R}/demo-home/.claude/projects/-tmp-demo-home-work-xxxxxxx/xxxxxxxx-2039-xxxx-8387-xxxxxxxxxxxx/subagents/agent-aaaaaaaaaaaaaaaaa.jsonl",
				"grep search  1 {R}/demo-home/.claude/projects/-tmp-demo-home-work-xxxxxxx/xxxxxxxx-2039-xxxx-8387-xxxxxxxxxxxx/subagents/agent-xxxxxxxxxxxxxxxxx.jsonl",
				"echo unknown  1 {R}/demo-home/.claude/projects/-tmp-demo-home-work-xxxxxxx/xxxxxxxx-2039-xxxx-8387-xxxxxxxxxxxx/subagents/agent-aaaaaaaaaaaaaaaaa.jsonl",
				"echo unknown  1 {R}/demo-home/.claude/projects/-tmp-demo-home-work-xxxxxxx/xxxxxxxx-2039-xxxx-8387-xxxxxxxxxxxx/subagents/agent-xxxxxxxxxxxxxxxxx.jsonl",
			},
		},
		"toolu_01KxCNBrHpLXwFeuvZRMCkfc": {
			why:      "a loop over a glob that matched nothing: `head -40 $f` is never a missing file, so the call credits no read",
			programs: "mkdir,cd,git,git,git,git,git,git,du,git,git,git,git,git,git,basename,echo,head,wc",
			files: []string{
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/rev-list-count-all.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/for-xxxx-xxx.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/stash-list.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/status-xxxxxxxxx.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/ls-files-s.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/config-local.txt",
				"du write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/du-git-before.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/log-xxxxxxxx-before.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/log-full-before.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/head-before.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/ls-tree-head-before.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/worktree-list.txt",
				"git write  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/count-objects-before.txt",
			},
		},
		"toolu_01U26ets9f9iu65Z8UhQfBas": {
			why:      "a loop over an unmatched `\"$out\"/*.txt`: its wc and head attribute no file named *.txt",
			programs: "export,rm,mkdir,grep,awk,git,git,head,xxxx,continue,[,git,head,cut,echo,continue,git,echo,du,basename,cut,wc,tr,head,cut,printf,head",
			files: []string{
				"grep search  0 {R}/demo-home/work/xxxxx/.xxxxx-xxxxxxx-xxxxxxxx/xxxxxxxxxxx-objects-preflight.txt",
			},
		},
	}
	for id, want := range named {
		parts := all[id]
		var programs []string
		for _, p := range parts {
			programs = append(programs, p.program)
		}
		if got := strings.Join(programs, ","); got != want.programs {
			t.Errorf("%s (%s): programs = %s, want %s", id, want.why, got, want.programs)
		}
		if got := corpusAttributions(t, parts, root); fmt.Sprint(got) != fmt.Sprint(want.files) {
			t.Errorf("%s (%s): attributions =\n%q\nwant\n%q", id, want.why, got, want.files)
		}
	}

	// Every Bash call's shares over its credited files sum to its bytes.
	rows := corpusRows(t, store, "SELECT tool_use_id, bytes_delivered FROM calls WHERE tool = 'Bash'")
	bytes := map[string]int64{}
	for _, row := range rows {
		var id string
		var n int64
		if _, err := fmt.Sscan(row, &id, &n); err != nil {
			t.Fatalf("bytes row %q: %v", row, err)
		}
		bytes[id] = n
	}
	var crediting int
	var credited int64
	for id, n := range bytes {
		seen := map[string]bool{}
		for _, p := range all[id] {
			for _, f := range p.files {
				if readActions[f.Action] {
					seen[f.Path] = true
				}
			}
		}
		if len(seen) == 0 {
			continue
		}
		crediting++
		credited += n
		var sum int64
		for _, share := range bashShares(n, len(seen)) {
			sum += share
		}
		if sum != n {
			t.Errorf("%s: shares over %d files sum to %d, want its %d bytes", id, len(seen), sum, n)
		}
	}
	if crediting != 97 {
		t.Errorf("%d Bash calls credit a read file, want %d", crediting, 97)
	}
	table, err := Files(ctx, store, Filter{Limit: 1 << 20}, nil)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	per, total := bashBytesOf(t, table)
	if total != credited {
		t.Errorf(
			"files report BASH BYTES total = %d, want %d, the bytes of the calls that credit a file",
			total,
			credited,
		)
	}
	for rel, want := range map[string]string{corpusPipeline: "5691", "demo-proj/pfm/internal/store/queries.go": "267"} {
		if got := per[filepath.Join(root, rel)]; got != want {
			t.Errorf("files report BASH BYTES of %s = %s, want %s", rel, got, want)
		}
	}
}
