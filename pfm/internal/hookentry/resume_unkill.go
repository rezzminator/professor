package hookentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
)

// ResumeUnkill is the fail-open hook for Claude's SessionStart event with
// source "resume". `claude --resume`, `--continue`, pfm's own resume and the
// in-app /resume all reopen a thread under its own session id and all fire
// this one event, so a killed thread that is live again leaves the killed set
// here, whatever route reopened it.
func ResumeUnkill(args []string, stdin io.Reader, stderr io.Writer, runtimes ...config.Runtime) (exitCode int) {
	flags := cli.NewFlagSet("internal resume-unkill", "usage: pfm internal resume-unkill < hook-payload.json", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	payload, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal resume-unkill: read hook payload (fail-open): %v\n", err)
		return 0
	}
	var hook struct {
		Event     string `json:"hook_event_name"`
		Source    string `json:"source"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(payload, &hook); err != nil {
		fmt.Fprintf(stderr, "pfm internal resume-unkill: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	if hook.Event != "SessionStart" || hook.Source != "resume" {
		return 0
	}
	if !pfmengine.IsUUID(hook.SessionID) {
		fmt.Fprintf(
			stderr,
			"pfm internal resume-unkill: session_id %q is not a session id (fail-open)\n",
			hook.SessionID,
		)
		return 0
	}
	database, manager, code := fleet.OpenKillManager(stderr, runtimes...)
	if code != 0 {
		fmt.Fprintln(stderr, "pfm internal resume-unkill: store unavailable (fail-open)")
		return 0
	}
	defer func() {
		if err := database.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal resume-unkill: close database (fail-open): %v\n", err)
		}
	}()
	if _, err := manager.UnkillResumed(context.Background(), hook.SessionID); err != nil {
		fmt.Fprintf(
			stderr,
			"pfm internal resume-unkill: lift kill on resumed %s (fail-open): %v\n",
			hook.SessionID,
			err,
		)
	}
	return 0
}
