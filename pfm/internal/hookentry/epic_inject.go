package hookentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
)

var epicNamePattern = regexp.MustCompile(`^E_([A-Za-z0-9][A-Za-z0-9-]*)_`)

type epicInjectPayload struct {
	SessionID      string `json:"session_id"`
	Prompt         string `json:"prompt"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
}

// epicInjectStore, when set, is the store EpicInject records into and the
// caller closes; nil opens the on-disk store for the call.
var epicInjectStore *store.Store

var epicInjectIdentify = func(ctx context.Context) (resolve.Identity, error) {
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
	if err != nil {
		return resolve.Identity{}, err
	}
	return identifier.Identify(ctx)
}

var epicInjectWindowName = func(ctx context.Context, identity resolve.Identity) (string, error) {
	// A native Herdr chat has no tmux window: its socket is Herdr's, and its
	// window name is its seat name.
	if identity.Backend == pfmcall.HerdrBackend {
		if identity.Session == "" {
			return "", fmt.Errorf("native chat on %s has no seat name", identity.SocketPath)
		}
		return identity.Session, nil
	}
	target := identity.Pane
	if target == "" {
		target = identity.Session
	}
	if identity.SocketPath == "" || target == "" {
		return "", resolve.ErrNotInChat
	}
	return (pfmcall.TmuxCaller{}).WindowName(ctx, identity.SocketPath, target)
}

// EpicInject is the UserPromptSubmit hook that carries text into a prompt's
// context: a native reload's --then message it could not type, on the reborn
// chat's first prompt (reload.ClaimThen), and an epic manifest once per
// session and epic name. It fails open, so a step past promptHookDeadline lets
// the prompt through.
func EpicInject(stdin io.Reader, stdout, stderr io.Writer) (exitCode int) {
	return RunPromptHook("pfm internal epic-inject", stdout, stderr, 0,
		func(ctx context.Context, step func(string), stdout, stderr io.Writer) int {
			return epicInjectBody(ctx, step, stdin, stdout, stderr)
		})
}

func epicInjectBody(ctx context.Context, step func(string), stdin io.Reader, stdout, stderr io.Writer) int {
	step("read hook payload")
	var payload epicInjectPayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: decode hook payload: %v\n", err)
		return 0
	}
	step("identify chat")
	identity, err := epicInjectIdentify(ctx)
	if err != nil {
		if strings.TrimSpace(payload.CWD) != "" {
			fmt.Fprintf(stderr, "pfm internal epic-inject: identify chat: %v\n", err)
		}
		return 0
	}
	manifest := epicContext(ctx, step, identity, payload, stderr)
	// The claim removes the stored follow-up, so it is the last step, inside
	// a commit span that ends once its text is in the hook's output.
	step("claim reload --then message")
	var contexts []string
	if end, open := deps.BeginCommit(ctx); open {
		defer end()
		if message := reloadThenContext(ctx, identity, payload, stderr); message != "" {
			contexts = append(contexts, reloadThenNotTyped+message)
		}
	}
	if manifest != "" {
		contexts = append(contexts, manifest)
	}
	if len(contexts) == 0 {
		return 0
	}
	response := struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}{}
	response.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	response.HookSpecificOutput.AdditionalContext = strings.Join(contexts, "\n\n")
	encoded, err := json.Marshal(response)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: encode context: %v\n", err)
		return 0
	}
	if _, err := fmt.Fprintln(stdout, string(encoded)); err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: write context: %v\n", err)
		return 1
	}
	return 0
}
