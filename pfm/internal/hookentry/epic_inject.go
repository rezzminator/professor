package hookentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
)

var epicNamePattern = regexp.MustCompile(`^E_([A-Za-z0-9][A-Za-z0-9-]*)_`)

type epicInjectPayload struct {
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
}

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

// EpicInject injects an epic manifest once per session and epic name. It
// fails open, so a step past promptHookDeadline lets the prompt through.
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
	if strings.TrimSpace(payload.CWD) == "" {
		return 0
	}
	step("identify chat")
	identity, err := epicInjectIdentify(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: identify chat: %v\n", err)
		return 0
	}
	step("read window name")
	window, err := epicInjectWindowName(ctx, identity)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: read window name: %v\n", err)
		return 0
	}
	match := epicNamePattern.FindStringSubmatch(window)
	if len(match) != 2 {
		return 0
	}
	slug := match[1]
	step("read manifest")
	root, err := gitRootFrom(payload.CWD)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: find git root from %s: %v\n", payload.CWD, err)
		return 0
	}
	manifestPath, err := epicManifestPath(root, slug)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		fmt.Fprintf(stderr, "pfm internal epic-inject: locate manifest: %v\n", err)
		return 0
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: read %s: %v\n", manifestPath, err)
		return 0
	}
	sessionID := identity.ID
	if sessionID == "" {
		sessionID = identity.Session
	}
	if sessionID == "" && payload.TranscriptPath != "" {
		sessionID = strings.TrimSuffix(filepath.Base(payload.TranscriptPath), filepath.Ext(payload.TranscriptPath))
	}
	if sessionID == "" {
		return 0
	}
	step("record state")
	database, err := store.Open()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: open state: %v\n", err)
		return 0
	}
	defer func() {
		if err := database.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal epic-inject: close state (fail-open): %v\n", err)
		}
	}()
	seen, err := database.EpicInjected(ctx, sessionID, slug)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: check state: %v\n", err)
		return 0
	}
	if seen {
		return 0
	}
	contextText := "INJECTED EPIC " + slug + "/manifest.md\n" + string(manifest)
	response := struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}{}
	response.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	response.HookSpecificOutput.AdditionalContext = contextText
	encoded, err := json.Marshal(response)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: encode context: %v\n", err)
		return 0
	}
	if err := database.RecordEpicInjection(ctx, sessionID, slug); err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: record state: %v\n", err)
		return 0
	}
	if _, err := fmt.Fprintln(stdout, string(encoded)); err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: write context: %v\n", err)
		return 1
	}
	return 0
}

func gitRootFrom(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(current); statErr == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no .git directory above %s", start)
		}
		current = parent
	}
}

func epicManifestPath(root, slug string) (string, error) {
	path := filepath.Join(root, "docs", "epics", slug, "manifest.md")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	lower := strings.ToLower(slug)
	if lower != slug {
		path = filepath.Join(root, "docs", "epics", lower, "manifest.md")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", os.ErrNotExist
}
