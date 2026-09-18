package reload

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/clock"
)

// LeftBehindSuffix is appended to the abandoned session's custom title by a
// `--new` reboot, so the resume row it leaves behind reads as what it is and
// no by-name verb mistakes it for the live chat.
const LeftBehindSuffix = " (before --new)"

// titleRecord is the shape index/claude.go reads a Claude custom title from
// (claudeRecord: type + customTitle, agentName as the fallback) and the
// shape every real custom-title record carries — type, customTitle,
// sessionId; nothing else — surveyed over a live store before this was
// written. The writer and the reader agree field for field.
type titleRecord struct {
	Type        string `json:"type"`
	CustomTitle string `json:"customTitle"`
	AgentName   string `json:"agentName,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
}

// TranscriptTitle is the custom title a Claude transcript wears — the LAST
// custom-title (or agent-name) record wins, exactly as index/claude.go
// resolves CustomTitle — so a `--new` reboot knows the name the session it
// abandons would keep. "" means the transcript has no custom title: its row
// is named from its prompts, and there is nothing to carry.
func TranscriptTitle(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read transcript %q for its title: %w", path, err)
	}
	title := ""
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"custom-title"`)) && !bytes.Contains(line, []byte(`"agent-name"`)) {
			continue
		}
		var record titleRecord
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		switch record.Type {
		case "custom-title", "agent-name":
			title = record.CustomTitle
			if title == "" {
				title = record.AgentName
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan transcript %q for its title: %w", path, err)
	}
	return title, nil
}

// TranscriptBackupDir is where every guarded transcript append parks the
// pre-append copy — cmd/pfm's dormant inject and this package's left-behind
// label share it.
func TranscriptBackupDir(home string) string {
	return filepath.Join(home, ".claude-sessions", ".chat-inject-backups")
}

// followName makes the name follow the live pane after a `--new` reboot
// (Wave 8 item 7, beat E1.06): the reborn session is auto-named from its
// first prompt and the abandoned one keeps the chat's name as a resume row,
// so every by-name verb reached the dead session. AFTER the --then steer —
// so the steer stays the reborn chat's first prompt — `/rename <name>` is
// typed through the same typist that proved the steer's submit, and the
// abandoned transcript gains a custom-title record naming it
// "<name> (before --new)". Both halves are courtesies to the reboot that
// already happened: a failure in either is a warning naming the recovery,
// never an error on a Run that succeeded.
func followName(ctx context.Context, request Request, options Options, tmux Tmux, proc Process, stderr io.Writer) {
	if request.Name == "" {
		return
	}
	if strings.ContainsAny(request.Name, "\r\n\x00") {
		// The typist sends the name as keystrokes; a line break would submit
		// half a command. /rename itself never records such a name, so this
		// is a corrupt record, reported rather than typed.
		fmt.Fprintf(stderr, "pfm chat reload --new: name %q is not one line — not carried\n", request.Name)
		return
	}
	rename := request
	rename.Then = "/rename " + request.Name
	if err := deliverThen(ctx, rename, options, tmux, proc, stderr); err != nil {
		fmt.Fprintf(
			stderr,
			"pfm chat reload --new: the reborn chat is NOT renamed %q: %v — run: pfm chat name %s %q\n",
			request.Name,
			err,
			request.Pane,
			request.Name,
		)
	} else {
		fmt.Fprintf(stderr, "pfm chat reload --new: reborn chat renamed %q\n", request.Name)
	}
	if request.Transcript == "" {
		return
	}
	title := request.Name + LeftBehindSuffix
	if err := labelTranscript(request.Transcript, title, TranscriptBackupDir(options.Home), options.Clock); err != nil {
		fmt.Fprintf(
			stderr,
			"pfm chat reload --new: the conversation left behind in %s is NOT relabelled %q: %v\n",
			request.Transcript,
			title,
			err,
		)
		return
	}
	fmt.Fprintf(
		stderr,
		"pfm chat reload --new: conversation left behind relabelled %q (%s)\n",
		title,
		request.Transcript,
	)
}

// labelTranscript appends one custom-title record to a Claude transcript
// under the guarded-append discipline (Transcript). The sessionId is the
// one the transcript's own records carry — its last record that states
// one — and, for a transcript that states none, the id the reader derives
// it from: the file's own stem.
func labelTranscript(path, title, backupDir string, clk clock.Clock) (returnErr error) {
	transcript, err := OpenTranscript(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := transcript.Close(); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	sessionID := lastSessionID(transcript.Raw())
	if sessionID == "" {
		sessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	record, err := json.Marshal(titleRecord{Type: "custom-title", CustomTitle: title, SessionID: sessionID})
	if err != nil {
		return fmt.Errorf("encode custom-title record: %w", err)
	}
	backup := filepath.Join(backupDir, fmt.Sprintf("%s-%d-left-behind.jsonl", sessionID, clk.Now().UTC().UnixNano()))
	return transcript.Append(record, backup)
}

// lastSessionID is the sessionId of the last record that states one.
func lastSessionID(raw []byte) string {
	sessionID := ""
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	for scanner.Scan() {
		var record struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(scanner.Bytes(), &record) == nil && record.SessionID != "" {
			sessionID = record.SessionID
		}
	}
	return sessionID
}
