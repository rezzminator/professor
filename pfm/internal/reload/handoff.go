package reload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

type handoffRecord struct {
	Engine     pfmengine.ID `json:"engine"`
	SessionID  string       `json:"session_id"`
	LeftBehind string       `json:"left_behind"`
	Account    int          `json:"account"`
	Cache1H    bool         `json:"cache_1h"`
	CWD        string       `json:"cwd"`
	WrittenAt  time.Time    `json:"written_at"`
}

func prepareReload(request Request) (Request, string, bool, error) {
	if request.SocketPath == "" || request.Pane == "" {
		return request, "", false, errors.New("reload requires a socket and pane")
	}
	if !rosterContains(request.AccountIDs, request.Account) {
		return request, "", false, fmt.Errorf("account %d is not in the configured roster", request.Account)
	}
	wasNew := request.New || request.fresh || request.SessionID == ""
	if request.SessionID == "" && request.Engine == pfmengine.Claude {
		id, err := claudelaunch.NewSessionID()
		if err != nil {
			return request, "", false, fmt.Errorf("new reload session id: %w", err)
		}
		request.SessionID, request.fresh = id, true
	}
	run, err := engineRun(request)
	if err != nil {
		return request, "", false, err
	}
	if run == "" {
		if descriptor, lookupErr := pfmengine.Lookup(request.Engine); lookupErr == nil {
			return request, "", false, fmt.Errorf("%s does not support in-place reload", descriptor.Short)
		}
		return request, "", false, fmt.Errorf("engine %q does not support in-place reload", request.Engine)
	}
	return request, run, wasNew, nil
}

func continueFromHandoff(
	lock *os.File,
	lockPath string,
	request Request,
	entry time.Time,
	sidDir string,
	stderr io.Writer,
) (Request, string, string, bool, error) {
	record, exists, err := readHandoff(lock)
	if err != nil {
		return request, "", "", false, fmt.Errorf(
			"reload handoff %s unreadable: %w — remove it, then reload again",
			lockPath,
			err,
		)
	}
	if !exists {
		return request, "", request.LeftBehind, false, nil
	}
	updated, continuedID, adopted, err := adoptHandoff(request, record, entry, sidDir)
	if err != nil {
		return request, "", "", false, err
	}
	if !adopted {
		return request, "", continuedID, false, nil
	}
	updated, run, wasNew, err := prepareReload(updated)
	if err != nil {
		return request, "", "", false, err
	}
	fmt.Fprintf(
		stderr,
		"pfm chat reload: the reload before this one rebooted this pane — continuing from session %s on account %d\n",
		continuedID,
		record.Account,
	)
	return updated, run, continuedID, wasNew, nil
}

func readHandoff(lock *os.File) (handoffRecord, bool, error) {
	if _, err := lock.Seek(0, io.SeekStart); err != nil {
		return handoffRecord{}, false, fmt.Errorf("seek reload handoff: %w", err)
	}
	content, err := io.ReadAll(lock)
	if err != nil {
		return handoffRecord{}, false, fmt.Errorf("read reload handoff: %w", err)
	}
	if len(content) == 0 {
		return handoffRecord{}, false, nil
	}
	var record handoffRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return handoffRecord{}, false, err
	}
	if record.Engine == "" || record.Account <= 0 || record.CWD == "" || record.WrittenAt.IsZero() {
		return handoffRecord{}, false, errors.New("reload handoff is missing required fields")
	}
	if _, err := pfmengine.Lookup(record.Engine); err != nil {
		return handoffRecord{}, false, fmt.Errorf("reload handoff engine: %w", err)
	}
	// The record stays until the next respawn overwrites it: a holder that
	// adopts it and fails before respawn leaves the pane on that reboot.
	return record, true, nil
}

func writeHandoff(lock *os.File, record handoffRecord) error {
	content, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode reload handoff: %w", err)
	}
	if err := lock.Truncate(0); err != nil {
		return fmt.Errorf("clear reload handoff: %w", err)
	}
	if _, err := lock.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek reload handoff: %w", err)
	}
	if _, err := lock.Write(content); err != nil {
		return fmt.Errorf("write reload handoff: %w", err)
	}
	return nil
}

func adoptHandoff(
	request Request,
	record handoffRecord,
	entry time.Time,
	sidDir string,
) (Request, string, bool, error) {
	if record.Engine != request.Engine {
		return request, request.LeftBehind, false, nil
	}
	crumb := filepath.Join(sidDir, filepath.Base(request.SocketPath)+"."+request.Pane)
	info, err := os.Stat(crumb)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return request, "", false, fmt.Errorf("stat reload breadcrumb %s: %w", crumb, err)
	}
	if !record.WrittenAt.After(entry) && err == nil && !info.ModTime().Before(record.WrittenAt) {
		return request, request.LeftBehind, false, nil
	}
	continuedID := record.SessionID
	if !request.New {
		if record.SessionID == "" {
			if record.Engine != pfmengine.Codex || request.SessionID == "" ||
				request.Transcript == "" || record.LeftBehind == "" ||
				request.SessionID == record.LeftBehind {
				return request, "", false, errors.New(
					"the reload before this one started a new Codex conversation whose id is not known yet — nothing changed; reload again once that chat has answered",
				)
			}
			continuedID = request.SessionID
		} else {
			values, err := pfmconfig.ResolvePaths()
			if err != nil {
				return request, "", false, fmt.Errorf("resolve reload transcript paths: %w", err)
			}
			transcript, err := SessionTranscript(values, request.Machine, request.Engine, record.SessionID)
			if err != nil {
				return request, "", false, fmt.Errorf("find reload handoff transcript: %w", err)
			}
			request.SessionID, request.Transcript = record.SessionID, transcript
			request.fresh = transcript == ""
		}
	}
	if !request.AccountGiven {
		selection, err := ValidateAccount(request.Machine, request.Engine, record.Account)
		if err != nil {
			return request, "", false, fmt.Errorf("validate reload handoff account: %w", err)
		}
		request.Account = record.Account
		request.AccountIDs = selection.IDs
		request.CodexHome, request.CodexBinary, request.CodexYolo = selection.CodexHome, selection.CodexBinary, selection.CodexYolo
	}
	if !request.CacheGiven {
		request.Cache1H = record.Cache1H
	}
	if info, err := os.Stat(record.CWD); err == nil && info.IsDir() {
		request.CWD = record.CWD
	}
	return request, continuedID, true, nil
}
