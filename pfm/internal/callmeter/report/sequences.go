package report

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// Run lengths the sequences topic enumerates.
const (
	MinRunSteps = 2
	MaxRunSteps = 5
)

// MinRunAgents is how many distinct agents a run must recur in to be listed.
const MinRunAgents = 2

// UnparsedBashStep is the step shape of a Bash call with no cached parts.
const UnparsedBashStep = "Bash (unparsed)"

// runArrow joins a run's steps.
const runArrow = " → "

// bashTool is the tool whose calls are shaped by their parsed command.
const bashTool = "Bash"

// seqStep is one call of an agent, normalized.
type seqStep struct {
	shape     string // tool, or "Bash {program}"
	file      string // "" when the call names no file
	delivered int64
}

// seqOccurrence is where one run occurrence starts.
type seqOccurrence struct{ agent, start int }

type runStat struct {
	key    string
	steps  int
	occurs []seqOccurrence
	agents map[int]bool
	bytes  int64
}

// bashStepOf is a Bash call's step: its first non-trivial program's base name
// and its first attributed file. A call with only trivial parts takes the
// first program it has.
func bashStepOf(parts []storedPart) (shape, file string) {
	if len(parts) == 0 {
		return UnparsedBashStep, ""
	}
	program := ""
	for _, p := range parts {
		if p.program != "" && !trivialPart(p) {
			program = filepath.Base(p.program)
			break
		}
	}
	for _, p := range parts {
		if program == "" && p.program != "" {
			program = filepath.Base(p.program)
		}
		if file == "" && len(p.files) > 0 {
			file = p.files[0].Path
		}
	}
	if program == "" {
		return bashTool, file
	}
	return bashTool + " " + program, file
}

// runKeyOf names the run steps[start:start+length]: each step's shape plus
// its file's role within the run, "same" when an earlier step of the run
// touched the file, "new" otherwise; a step with no file shows its shape only.
func runKeyOf(steps []seqStep) string {
	seen := map[string]bool{}
	names := make([]string, len(steps))
	for i, s := range steps {
		switch {
		case s.file == "":
			names[i] = s.shape
		case seen[s.file]:
			names[i] = s.shape + " same"
		default:
			seen[s.file] = true
			names[i] = s.shape + " new"
		}
	}
	return strings.Join(names, runArrow)
}

// Sequences lists call runs of MinRunSteps to MaxRunSteps consecutive steps of
// one agent that recur in at least MinRunAgents agents, ranked by occurrences
// times the bytes their calls delivered.
func Sequences(ctx context.Context, store *callmeter.Store, f Filter, nameOf NameOf) (*Table, error) {
	n := newNames(nameOf)
	parts, err := loadParts(ctx, store, f)
	if err != nil {
		return nil, err
	}
	var agents [][]seqStep
	var unparsed int64
	lastKey := ""
	where, args := f.where()
	err = query(ctx, store, "calls in order",
		`SELECT c.tool_use_id, COALESCE(c.session_id, ''), COALESCE(c.agent_id, ''), COALESCE(c.tool, ''),
		COALESCE(c.file_path, ''), COALESCE(c.bytes_delivered, 0)
		FROM calls c WHERE `+where+` ORDER BY c.session_id, c.agent_id, c.ts, c.tool_use_id`, args,
		func(r rowSource) error {
			var id, session, agent, tool, path string
			var delivered int64
			if err := r.Scan(&id, &session, &agent, &tool, &path, &delivered); err != nil {
				return err
			}
			step := seqStep{shape: tool, file: path, delivered: delivered}
			if tool == bashTool {
				step.shape, step.file = bashStepOf(parts[id])
				if step.shape == UnparsedBashStep {
					unparsed++
				}
			}
			if key := agentKey(session, agent); key != lastKey || len(agents) == 0 {
				lastKey = key
				agents = append(agents, nil)
			}
			agents[len(agents)-1] = append(agents[len(agents)-1], step)
			return nil
		})
	if err != nil {
		return nil, err
	}
	listed := recurringRuns(agents)
	sort.Slice(listed, func(i, j int) bool {
		a, b := listed[i], listed[j]
		if sa, sb := int64(len(a.occurs))*a.bytes, int64(len(b.occurs))*b.bytes; sa != sb {
			return sa > sb
		}
		return a.key < b.key
	})
	t := &Table{
		Title:  f.title("sequences", n),
		Header: []string{"RUN", "OCCURRENCES", "AGENTS", "BYTES", "MEAN BYTES"},
	}
	for _, s := range listed[:min(len(listed), f.limit())] {
		occurs := int64(len(s.occurs))
		t.Rows = append(t.Rows, []string{
			s.key, itoa(occurs), itoa(int64(len(s.agents))), itoa(s.bytes), itoa(s.bytes / occurs),
		})
	}
	notes, err := gapNotes(ctx, store, f)
	if err != nil {
		return nil, err
	}
	t.Notes = notes
	if unparsed > 0 {
		t.Notes = append(
			t.Notes,
			fmt.Sprintf("%d Bash calls have no parsed parts: stepped as %q", unparsed, UnparsedBashStep),
		)
	}
	t.Notes = append(t.Notes, n.notes()...)
	return t, nil
}

// recurringRuns enumerates every run of MinRunSteps to MaxRunSteps steps in
// each agent's step list and keeps those seen in MinRunAgents agents or more.
func recurringRuns(agents [][]seqStep) []*runStat {
	stats := map[string]*runStat{}
	for ai, steps := range agents {
		for start := range steps {
			for length := MinRunSteps; length <= MaxRunSteps && start+length <= len(steps); length++ {
				window := steps[start : start+length]
				key := runKeyOf(window)
				s, ok := stats[key]
				if !ok {
					s = &runStat{key: key, steps: length, agents: map[int]bool{}}
					stats[key] = s
				}
				s.occurs = append(s.occurs, seqOccurrence{agent: ai, start: start})
				s.agents[ai] = true
				for _, step := range window {
					s.bytes += step.delivered
				}
			}
		}
	}
	// A shorter run that occurs exactly as often as a longer run containing it
	// adds nothing: every one of its occurrences sits inside an occurrence of
	// the longer run, so it is dropped. Each occurrence of the longer run holds
	// the shorter one at the same offset, so the shorter count is at least the
	// longer one's; equal counts mean no occurrence of it stands on its own.
	drop := map[string]bool{}
	for _, long := range stats {
		if len(long.agents) < MinRunAgents {
			continue
		}
		first := long.occurs[0]
		steps := agents[first.agent][first.start : first.start+long.steps]
		for length := MinRunSteps; length < long.steps; length++ {
			for offset := 0; offset+length <= long.steps; offset++ {
				short := stats[runKeyOf(steps[offset:offset+length])]
				if len(short.occurs) == len(long.occurs) {
					drop[short.key] = true
				}
			}
		}
	}
	var listed []*runStat
	for key, s := range stats {
		if len(s.agents) >= MinRunAgents && !drop[key] {
			listed = append(listed, s)
		}
	}
	return listed
}
