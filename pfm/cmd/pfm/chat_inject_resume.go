package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/agentopen"
	pfmchat "hostops/pfm/internal/chat"
	"hostops/pfm/internal/clock"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/paths"
)

const transcriptRoleUser = "user"

type resumeTarget struct {
	ID   string
	Path string
}

// resolveResumeTarget is the dormant half of the documented inject ladder.
// It runs only after every live namespace missed, so a live tmux session name
// always wins over a transcript id, path, or excerpt that happens to resemble
// it.
func resolveResumeTarget(target string, runtimes ...commandRuntime) (resumeTarget, bool, error) {
	if info, err := os.Stat(target); err == nil {
		if !info.Mode().IsRegular() {
			return resumeTarget{}, false, fmt.Errorf("transcript target is not a regular file: %s", target)
		}
		path, err := filepath.Abs(target)
		if err != nil {
			return resumeTarget{}, false, fmt.Errorf("resolve transcript path %q: %w", target, err)
		}
		return resumeTarget{ID: composeTranscriptIDFromPath(path), Path: path}, true, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return resumeTarget{}, false, fmt.Errorf("stat transcript target %q: %w", target, err)
	}

	files, err := pfmchat.ClaudeTranscripts(firstRuntime(runtimes))
	if err != nil {
		return resumeTarget{}, false, err
	}
	if sessionToken(target) {
		exact := make([]resumeTarget, 0, 1)
		prefix := make([]resumeTarget, 0, 2)
		for _, path := range files {
			id := composeTranscriptIDFromPath(path)
			switch {
			case id == target:
				exact = append(exact, resumeTarget{ID: id, Path: path})
			case len(target) >= 8 && strings.HasPrefix(id, target):
				prefix = append(prefix, resumeTarget{ID: id, Path: path})
			}
		}
		matches := exact
		if len(matches) == 0 {
			matches = prefix
		}
		switch len(matches) {
		case 1:
			return matches[0], true, nil
		case 0:
		default:
			return resumeTarget{}, false, fmt.Errorf(
				"session id %q is ambiguous across %d transcripts",
				target,
				len(matches),
			)
		}
	}

	if len(pfmchat.ExcerptNeedles(target)) == 0 {
		return resumeTarget{}, false, nil
	}
	matches, err := pfmchat.Find(
		context.Background(),
		firstRuntime(runtimes),
		pfmchat.FindRequest{Excerpt: target, Self: pfmchat.AskingSession()},
	)
	if err != nil {
		if errors.Is(err, pfmchat.ErrNoExcerptMatch) || errors.Is(err, pfmchat.ErrNoTranscriptRegistry) {
			return resumeTarget{}, false, nil
		}
		return resumeTarget{}, false, err
	}
	matches = deduplicateTranscriptMatches(matches)
	match := matches[0]
	if len(matches) > 1 && matches[1].Hits == match.Hits {
		return resumeTarget{}, false, fmt.Errorf(
			"excerpt is ambiguous between %s and %s",
			match.ID,
			matches[1].ID,
		)
	}
	return resumeTarget{ID: match.ID, Path: match.Path}, true, nil
}

// deduplicateTranscriptMatches collapses account or registry copies of one
// session before the ambiguity check. Find already orders matches by best hit
// count and then path, so retaining the first row preserves that deterministic
// winner while distinct session IDs remain candidates for ambiguity.
func deduplicateTranscriptMatches(matches []pfmchat.TranscriptMatch) []pfmchat.TranscriptMatch {
	unique := matches[:0]
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		if seen[match.ID] {
			continue
		}
		seen[match.ID] = true
		unique = append(unique, match)
	}
	return unique
}

func sessionToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' {
			return false
		}
	}
	return true
}

// composeTranscriptIDFromPath is the cmd seam for compose's canonical, currently
// unexported transcriptIDFromPath. Exporting that helper requires a compose edit,
// which this package-scoped change cannot make.
func composeTranscriptIDFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

func runningClaudeSession(procRoot, id string) (bool, error) {
	entries, err := os.ReadDir(procRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read process jail %q: %w", procRoot, err)
	}
	needle := []byte("CLAUDE_CODE_SESSION_ID=" + id + "\x00")
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil || !entry.IsDir() {
			continue
		}
		path := filepath.Join(procRoot, entry.Name(), "environ")
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("read process environment %q: %w", path, err)
		}
		if bytes.Contains(raw, needle) {
			return true, nil
		}
	}
	return false, nil
}

func uuidSessionID(id string) bool {
	parts := strings.Split(id, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 ||
		len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		return false
	}
	for _, part := range parts {
		for _, character := range part {
			if (character < '0' || character > '9') &&
				(character < 'a' || character > 'f') &&
				(character < 'A' || character > 'F') {
				return false
			}
		}
	}
	return true
}

// liveCrumbSession restores the portable half of the live-session guard. A
// chat can be absent from /proc (or its environment unreadable) while its
// socket crumb still points at a live tmux pane. The crumb is evidence only
// after the jailed tmux server answers; a stale socket file is not life.
func liveCrumbSession(
	ctx context.Context,
	resolved paths.Values,
	id string,
) (string, bool, error) {
	entries, err := os.ReadDir(resolved.SIDDir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read session crumbs %q: %w", resolved.SIDDir, err)
	}
	client := gather.TmuxProbe{TmuxTmpDir: filepath.Dir(resolved.TmuxDir)}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		socket, paneID, ok := gather.ParseCrumbName(entry.Name())
		if !ok {
			continue
		}
		content, err := os.ReadFile(filepath.Join(resolved.SIDDir, entry.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("read session crumb %q: %w", entry.Name(), err)
		}
		crumbID := composeTranscriptIDFromPath(strings.TrimSpace(string(content)))
		if crumbID != id {
			continue
		}
		panes, err := client.ListPanes(ctx, socket)
		if errors.Is(err, gather.ErrServerGone) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("prove crumb socket %q: %w", socket, err)
		}
		for index := range panes {
			pane := &panes[index]
			if paneID == "" || pane.PaneID == paneID {
				return socket + ":" + pane.PaneID, true, nil
			}
		}
	}
	return "", false, nil
}

// registeredDaemonSession asks every configured account. A partial or failed
// registry query cannot be interpreted as absence: the exact session hidden
// behind the failed account is the one a transcript append would lose.
func registeredDaemonSession(
	ctx context.Context,
	resolved paths.Values,
	machine pfmconfig.Config,
	id string,
) (string, bool, error) {
	configs := make([]string, 0, len(resolved.Roots[pfmengine.Claude]))
	seen := make(map[string]bool)
	for _, root := range resolved.Roots[pfmengine.Claude] {
		if filepath.Base(root) != "projects" {
			continue
		}
		config := filepath.Dir(root)
		if seen[config] {
			continue
		}
		seen[config] = true
		configs = append(configs, config)
	}
	if len(configs) == 0 {
		return "", false, nil
	}
	binaryName := machine.Claude.Binary
	if binaryName == "" {
		binaryName = pfmengine.MustLookup(pfmengine.Claude).Binary
	}
	binary, err := deps.Resolve(binaryName)
	if err != nil {
		if filepath.IsAbs(binaryName) {
			binary = binaryName
		} else {
			binary = filepath.Join(resolved.Home, ".local", "bin", binaryName)
		}
	}
	for _, config := range configs {
		queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		// A registry read is not a conversation: no prompt material, no
		// autonomy flags, but the fleet's one hygiene strip so a query fired
		// from inside a chat cannot inherit that chat's identity. The loop
		// walks bare config directories, so each is stated as a one-account
		// roster — the door never takes a bare directory.
		command, buildErr := action.ClaudeSpawn{
			Purpose: action.PurposeQuery,
			Account: 1,
			Args:    []string{agentsCommand, "--json"},
			Machine: pfmconfig.Config{
				Claude:   pfmconfig.ClaudePrefs{Binary: binary},
				Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: config}},
			},
		}.Command(queryCtx)
		if buildErr != nil {
			cancel()
			return "", false, fmt.Errorf("build agent registry query for %q: %w", config, buildErr)
		}
		output, queryErr := command.Output()
		cancel()
		if queryErr != nil {
			return "", false, fmt.Errorf("query agent registry for %q: %w", config, queryErr)
		}
		agents, err := agentopen.ParseAgents(output)
		if err != nil {
			return "", false, fmt.Errorf("parse agent registry for %q: %w", config, err)
		}
		for _, agent := range agents {
			if agent.SessionID == id ||
				(agent.ShortID != "" && strings.HasPrefix(id, agent.ShortID)) {
				return config, true, nil
			}
		}
	}
	return "", false, nil
}

type resumeReceipt struct {
	SessionID string
	EventID   string
	ParentID  string
	Backup    string
}

func appendResumeInjection(
	ctx context.Context,
	resolved paths.Values,
	target resumeTarget,
	message string,
	clk clock.Clock,
) (receipt resumeReceipt, returnErr error) {
	clk = defaultClock(clk)
	if err := ctx.Err(); err != nil {
		return resumeReceipt{}, err
	}
	file, err := os.OpenFile(target.Path, os.O_RDWR, 0)
	if err != nil {
		return resumeReceipt{}, fmt.Errorf("open transcript %q: %w", target.Path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript %q: %w", target.Path, err))
		}
	}()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return resumeReceipt{}, fmt.Errorf("lock transcript %q: %w", target.Path, err)
	}
	defer func() {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("unlock transcript %q: %w", target.Path, err))
		}
	}()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return resumeReceipt{}, fmt.Errorf("rewind transcript %q: %w", target.Path, err)
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return resumeReceipt{}, fmt.Errorf("read transcript %q: %w", target.Path, err)
	}
	tail, err := lastUUIDEvent(raw)
	if err != nil {
		return resumeReceipt{}, fmt.Errorf("read transcript tail %q: %w", target.Path, err)
	}
	parent, _ := tail["uuid"].(string)
	sessionID, _ := tail["sessionId"].(string)
	if parent == "" || sessionID == "" {
		return resumeReceipt{}, fmt.Errorf("transcript %q has no uuid-bearing session tail", target.Path)
	}
	eventID, err := randomUUID()
	if err != nil {
		return resumeReceipt{}, fmt.Errorf("create injected event id: %w", err)
	}
	promptID, err := randomUUID()
	if err != nil {
		return resumeReceipt{}, fmt.Errorf("create injected prompt id: %w", err)
	}
	event := map[string]any{
		"type":        transcriptRoleUser,
		"userType":    "external",
		"entrypoint":  "cli",
		"sessionId":   sessionID,
		"parentUuid":  parent,
		"uuid":        eventID,
		"promptId":    promptID,
		"timestamp":   clk.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"isSidechain": false,
		"isMeta":      false,
		"message": map[string]any{
			"role":    transcriptRoleUser,
			"content": message,
		},
	}
	for _, key := range []string{"cwd", versionCommand, "gitBranch"} {
		if value, exists := tail[key]; exists {
			event[key] = value
		}
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return resumeReceipt{}, fmt.Errorf("encode injected transcript event: %w", err)
	}
	backupDir := filepath.Join(resolved.Home, ".claude-sessions", ".chat-inject-backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return resumeReceipt{}, fmt.Errorf("create transcript backup directory %q: %w", backupDir, err)
	}
	backup := filepath.Join(
		backupDir,
		fmt.Sprintf("%s-%d-%s.jsonl", sessionID, clk.Now().UTC().UnixNano(), eventID[:8]),
	)
	backupFile, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return resumeReceipt{}, fmt.Errorf("create transcript backup %q: %w", backup, err)
	}
	if _, err := backupFile.Write(raw); err != nil {
		_ = backupFile.Close()
		return resumeReceipt{}, fmt.Errorf("write transcript backup %q: %w", backup, err)
	}
	if err := backupFile.Sync(); err != nil {
		_ = backupFile.Close()
		return resumeReceipt{}, fmt.Errorf("sync transcript backup %q: %w", backup, err)
	}
	if err := backupFile.Close(); err != nil {
		return resumeReceipt{}, fmt.Errorf("close transcript backup %q: %w", backup, err)
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return resumeReceipt{}, fmt.Errorf("seek transcript %q for append: %w", target.Path, err)
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		if _, err := file.Write([]byte{'\n'}); err != nil {
			return resumeReceipt{}, fmt.Errorf("separate transcript append %q: %w", target.Path, err)
		}
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return resumeReceipt{}, fmt.Errorf("append transcript %q: %w", target.Path, err)
	}
	if err := file.Sync(); err != nil {
		return resumeReceipt{}, fmt.Errorf("sync transcript %q: %w", target.Path, err)
	}
	return resumeReceipt{SessionID: sessionID, EventID: eventID, ParentID: parent, Backup: backup}, nil
}

func lastUUIDEvent(raw []byte) (map[string]any, error) {
	var tail map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("decode JSONL line: %w", err)
		}
		if uuid, _ := event["uuid"].(string); uuid != "" {
			tail = event
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if tail == nil {
		return nil, errors.New("no uuid-bearing event")
	}
	return tail, nil
}

func randomUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := make([]byte, 32)
	hex.Encode(encoded, raw[:])
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" +
		string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" +
		string(encoded[20:32]), nil
}
