package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// ClearHook shares one dispatch door for the two halves of Claude's /clear lifecycle.
func ClearHook(args []string, stdin io.Reader, stdout, stderr io.Writer, runtime config.Runtime) int {
	if args[0] == "clear-kill" {
		return ClearKill(args[1:], stdin, stderr, runtime)
	}
	if args[0] == "resume-unkill" {
		return ResumeUnkill(args[1:], stdin, stderr, runtime)
	}
	return ClearName(args[1:], stdin, stdout, stderr, runtime)
}

const clearNameStartEvent = "SessionStart"

type clearTitleOutput struct {
	Hook struct {
		Event string `json:"hookEventName"`
		Title string `json:"sessionTitle"`
	} `json:"hookSpecificOutput"`
}

type clearNamePayload struct {
	Event     string `json:"hook_event_name"`
	Source    string `json:"source"`
	SessionID string `json:"session_id"`
	Title     string `json:"session_title"`
	Path      string `json:"transcript_path"`
	CWD       string `json:"cwd"`
	AgentID   string `json:"agent_id"`
}

// ClearName names pfm and Herdr immediately, then gives Claude its title on
// the first prompt. SessionStart sessionTitle does not support source=clear.
func ClearName(args []string, stdin io.Reader, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet("internal clear-name", "usage: pfm internal clear-name < hook-payload.json", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, herdrMaxPayload))
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal clear-name: read hook payload (fail-open): %v\n", err)
		return 0
	}
	var payload clearNamePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal clear-name: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	if payload.AgentID != "" || (payload.Event != claudelaunch.HookEventUserPromptSubmit &&
		(payload.Event != clearNameStartEvent || payload.Source != "clear")) {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), promptHookDeadline)
	defer cancel()
	if err := clearNameEvent(ctx, payload, stdout, runtime); err != nil {
		if payload.Event == claudelaunch.HookEventUserPromptSubmit {
			fmt.Fprintf(stderr, "pfm internal clear-name: session %s pending title check failed (fail-open): %v\n",
				payload.SessionID, err)
		} else {
			fmt.Fprintf(stderr, "pfm internal clear-name: successor %s NOT named/titled (fail-open): %v\n",
				payload.SessionID, err)
		}
		return 0
	}
	if payload.Event == clearNameStartEvent {
		// Hooks run concurrently. Re-report AFTER our durable decision, so an
		// earlier herdr-start cannot leave the pane carrying the inherited name.
		return HerdrState([]string{"start"}, strings.NewReader(string(raw)), stderr)
	}
	return 0
}

func clearNameEvent(
	ctx context.Context,
	payload clearNamePayload,
	stdout io.Writer,
	runtime config.Runtime,
) (returnErr error) {
	if !engine.IsUUID(payload.SessionID) {
		return fmt.Errorf("session_id %q is not a session id", payload.SessionID)
	}
	if payload.Path != "" && !filepath.IsAbs(payload.Path) {
		return fmt.Errorf("successor transcript_path %q is not absolute", payload.Path)
	}
	state := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	if payload.Event == claudelaunch.HookEventUserPromptSubmit {
		pending, found, err := state.ClearName(ctx, payload.SessionID)
		if err != nil {
			return err
		}
		if !found || pending.Emitted || pending.Confirmed {
			return nil
		}
	}
	unlock, err := fleetdb.LockSeat(ctx, runtime.Paths, payload.SessionID)
	if err != nil {
		return fmt.Errorf("lock successor name: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	name, found, err := state.ClearName(ctx, payload.SessionID)
	if err != nil {
		return err
	}
	if payload.Event == claudelaunch.HookEventUserPromptSubmit {
		if !found || name.Emitted {
			return nil
		}
		current := payload.Title
		if payload.Path != "" {
			current, err = transcript.FileTitle(payload.Path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		name = name.ObserveTitle(current)
		// A user rename between /clear and the first prompt wins.
		if !name.Confirmed && current != name.Name && name.Title(current) == name.Name {
			output := clearTitleOutput{}
			output.Hook.Event = claudelaunch.HookEventUserPromptSubmit
			output.Hook.Title = name.Name
			if err := json.NewEncoder(stdout).Encode(output); err != nil {
				return fmt.Errorf("write Claude sessionTitle output: %w", err)
			}
		}
		name.Emitted = true
		return state.PutClearName(ctx, payload.SessionID, name, clock.Real.Now().Unix())
	}
	db, err := store.OpenContext(ctx)
	if err != nil {
		return fmt.Errorf("open successor cache: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	if !found {
		original := payload.Title
		if original == "" && payload.Path != "" {
			original, err = transcript.FileTitle(payload.Path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		name, err = db.ReserveClearName(ctx, payload.SessionID, original)
		if err != nil || name.Name == "" {
			return err
		}
	}
	// The successor can still be empty: seed only the derived cache, never
	// Claude's transcript. Subsequent index passes read the shared overlay.
	_, exists, err := db.Transcript(ctx, payload.SessionID)
	if err != nil {
		return err
	}
	if !exists {
		cached := store.Transcript{UUID: payload.SessionID, Path: payload.Path, CWD: payload.CWD}
		return db.SeedClearTranscript(ctx, cached)
	}
	return nil
}
