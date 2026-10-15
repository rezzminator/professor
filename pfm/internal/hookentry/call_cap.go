package hookentry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// callCapUsage is the one form `pfm internal call-cap` accepts.
const callCapUsage = "usage: pfm internal call-cap"

// callCapNear is the context from four fifths of the cap on: the count the
// agent's own memory loses at every compaction.
const callCapNear = "call-cap: %d of your %d model requests used, counted from your own transcript (%s), " +
	"compactions included; %d left. This count is the true one: trust it over your own."

// callCapStop is the context at or past the cap; its last verb is the return
// callCapReturns names for the agent's CallCapRole.
const callCapStop = "call-cap: %d model requests used, at or past your cap of %d, counted from your own " +
	"transcript (%s). Stop: start no new work, and %s"

// callCapReturns is each role's cap return as its own manual words it: the
// foreman's, the lander's, and the executor's for every other capped agent.
var callCapReturns = map[string]string{
	"foreman": "return `FAILED {flight}: cap` (a child writes its `{unit}`) with the handoff: the decision, " +
		"the files changed, what landed and was proven, what is left, the next step. A cap reached is never DONE.",
	"lander": "return `FAIL {flight}: cap` with the ledger as it stands.",
	"":       "return `FAILED {id}: cap` with what is done, what is left and the next step.",
}

// callCapTallyAge is how old a tally grows before a new tally's birth prunes
// it: past any one agent's run.
const callCapTallyAge = 7 * 24 * time.Hour

// callCapNoType is the context when the payload names no agent type: the
// count stands, the cap is unknown, never the default read in silence.
const callCapNoType = "call-cap: %d model requests used, counted from your own transcript (%s); your cap is " +
	"unknown: the hook payload names no agent_type."

// callCapUnknown is the context when the count could not be read: a failure
// to count never reads as a count under the cap.
const callCapUnknown = "call-cap: your model-request count is unknown (%v); your cap is %s, so keep your own count."

// callCapAgentID is the form of a Claude Code agent id; any other value never
// becomes a path.
var callCapAgentID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type callCapHookInput struct {
	TranscriptPath string `json:"transcript_path"`
	AgentID        string `json:"agent_id"`
	AgentType      string `json:"agent_type"`
}

type callCapResponse struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// CallCapHook is `pfm internal call-cap`, a PreToolUse hook on any tool of
// an agent whose manual caps its model requests. It counts the requests in
// the agent's own transcript (transcript.RequestTally, every compaction
// included, resumed from the tally the last run saved) against the cap of the payload's agent_type (transcript.CallCap,
// the cap the flight report judges by) and, from four fifths of the cap on,
// injects the count as additionalContext; at or past the cap the context
// orders the stop and the cap return of the agent's role (callCapReturns). It never denies a
// tool: the handoff needs them. A count or cap it cannot read is injected as
// unknown, never as silence.
func CallCapHook(args []string, input io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintf(stderr, "pfm internal call-cap: takes no argument, got %q\n%s\n", args, callCapUsage)
		return 1
	}
	hook, path, count, err := callCapCount(input, stderr)
	var context string
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "pfm internal call-cap: %v\n", err)
		limit := "unknown"
		if hook.AgentType != "" {
			limit = fmt.Sprintf("%d model requests", transcript.CallCap(hook.AgentType))
		}
		context = fmt.Sprintf(callCapUnknown, err, limit)
	case hook.AgentType == "":
		fmt.Fprintf(stderr, "pfm internal call-cap: hook payload names no agent_type\n")
		context = fmt.Sprintf(callCapNoType, count, path)
	default:
		limit := transcript.CallCap(hook.AgentType)
		switch {
		case count >= limit:
			context = fmt.Sprintf(
				callCapStop,
				count,
				limit,
				path,
				callCapReturns[transcript.CallCapRole(hook.AgentType)],
			)
		case count >= limit-limit/5:
			context = fmt.Sprintf(callCapNear, count, limit, path, limit-count)
		default:
			return 0
		}
	}
	var response callCapResponse
	response.HookSpecificOutput.HookEventName = "PreToolUse"
	response.HookSpecificOutput.AdditionalContext = context
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		fmt.Fprintf(stderr, "pfm internal call-cap: write hook response: %v\n", err)
		return 1
	}
	return 0
}

// callCapCount decodes the payload, resolves the calling agent's transcript
// and counts its requests. A sub-agent's payload names the session's
// transcript and its agent_id, its own file being agent-{id}.jsonl in the
// session's subagents directory; a main-thread agent's payload names its own
// file. hook holds whatever decoded, even beside an error.
func callCapCount(input io.Reader, stderr io.Writer) (hook callCapHookInput, path string, count int, err error) {
	raw, err := io.ReadAll(input)
	if err != nil {
		return hook, "", 0, fmt.Errorf("read hook payload: %w", err)
	}
	if err := json.Unmarshal(raw, &hook); err != nil {
		// A field of the wrong type still leaves its siblings decoded; a payload that is
		// no JSON decodes nothing.
		return hook, "", 0, fmt.Errorf("decode hook payload: %w", err)
	}
	if hook.TranscriptPath == "" {
		return hook, "", 0, errors.New("hook payload names no transcript_path")
	}
	path = hook.TranscriptPath
	if hook.AgentID != "" {
		if !callCapAgentID.MatchString(hook.AgentID) {
			return hook, "", 0, fmt.Errorf("hook payload agent_id %q is no agent id", hook.AgentID)
		}
		if id, named := transcript.SubagentID(filepath.Base(path)); !named || id != hook.AgentID {
			path = transcript.SubagentTranscript(transcript.SubagentsDir(path), hook.AgentID)
		}
	}
	count, err = callCapResume(path, stderr)
	if err != nil {
		return hook, path, 0, err
	}
	return hook, path, count, nil
}

// callCapResume counts path's requests from its saved tally
// (paths.CallCapTallyDir), reading only what the agent appended since, and
// saves the tally again. A tally that cannot be read or saved costs a full
// read or the next run's, logged to stderr; it never changes the count. A run
// that read no new complete line leaves the tally file untouched.
func callCapResume(path string, stderr io.Writer) (int, error) {
	var tally transcript.RequestTally
	dir := ""
	home, err := paths.Home()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal call-cap: no tally, counting from the start: %v\n", err)
	} else {
		dir = paths.CallCapTallyDir(home)
	}
	sum := sha256.Sum256([]byte(path))
	tallyPath := filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
	if dir != "" {
		raw, err := os.ReadFile(tallyPath)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			fmt.Fprintf(stderr, "pfm internal call-cap: read tally %s, counting from the start: %v\n", tallyPath, err)
		default:
			if err := json.Unmarshal(raw, &tally); err != nil {
				fmt.Fprintf(
					stderr,
					"pfm internal call-cap: decode tally %s, counting from the start: %v\n",
					tallyPath,
					err,
				)
				tally = transcript.RequestTally{}
			}
		}
	}
	born := tally.Offset == 0
	loaded := tally
	count, _, err := tally.Resume(path)
	if err != nil || dir == "" {
		return count, err
	}
	if tally.Offset == loaded.Offset && tally.Lines == loaded.Lines && tally.Head == loaded.Head {
		return count, nil
	}
	if born {
		callCapPrune(dir, stderr)
	}
	raw, err := json.Marshal(tally)
	if err == nil {
		err = atomicfile.Write(tallyPath, raw, 0o600)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal call-cap: save tally %s: %v\n", tallyPath, err)
	}
	return count, nil
}

// callCapPrune removes the files in dir older than callCapTallyAge (tallies,
// and temporary ones a killed run left), each failure logged to stderr.
func callCapPrune(dir string, stderr io.Writer) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stderr, "pfm internal call-cap: list tallies in %s: %v\n", dir, err)
		}
		return
	}
	cutoff := clock.Real.Now().Add(-callCapTallyAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(stderr, "pfm internal call-cap: stat tally %s: %v\n", entry.Name(), err)
			}
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stderr, "pfm internal call-cap: prune tally %s: %v\n", entry.Name(), err)
		}
	}
}
