package hookentry

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// epicContext is the epic manifest this chat's window names, once per session
// and epic name; empty when none is due.
func epicContext(
	ctx context.Context,
	step func(string),
	identity resolve.Identity,
	payload epicInjectPayload,
	stderr io.Writer,
) string {
	if strings.TrimSpace(payload.CWD) == "" {
		return ""
	}
	step("read window name")
	window, err := epicInjectWindowName(ctx, identity)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: read window name: %v\n", err)
		return ""
	}
	match := epicNamePattern.FindStringSubmatch(window)
	if len(match) != 2 {
		return ""
	}
	slug := match[1]
	step("read manifest")
	root, err := gitRootFrom(payload.CWD)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: find git root from %s: %v\n", payload.CWD, err)
		return ""
	}
	manifestPath, err := epicManifestPath(root, slug)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		fmt.Fprintf(stderr, "pfm internal epic-inject: locate manifest: %v\n", err)
		return ""
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: read %s: %v\n", manifestPath, err)
		return ""
	}
	sessionID := identity.ID
	if sessionID == "" {
		sessionID = identity.Session
	}
	if sessionID == "" && payload.TranscriptPath != "" {
		sessionID = strings.TrimSuffix(filepath.Base(payload.TranscriptPath), filepath.Ext(payload.TranscriptPath))
	}
	if sessionID == "" {
		return ""
	}
	step("record state")
	database := epicInjectStore
	if database == nil {
		opened, err := store.Open()
		if err != nil {
			fmt.Fprintf(stderr, "pfm internal epic-inject: open state: %v\n", err)
			return ""
		}
		database = opened
		defer func() {
			if err := database.Close(); err != nil {
				fmt.Fprintf(stderr, "pfm internal epic-inject: close state (fail-open): %v\n", err)
			}
		}()
	}
	seen, err := database.EpicInjected(ctx, sessionID, slug)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: check state: %v\n", err)
		return ""
	}
	if seen {
		return ""
	}
	if err := database.RecordEpicInjection(ctx, sessionID, slug); err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: record state: %v\n", err)
		return ""
	}
	return "INJECTED EPIC " + slug + "/manifest.md\n" + string(manifest)
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
