package reload

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/inject"
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
	options.defaults()
	// The steer was only proven TYPED and submitted (deliverThen) — its turn
	// is still running. A slash command typed into a running turn is ordinary
	// conversation text, not a TUI command, so the rename has to wait out that
	// turn first. The wait is inject's, the one this repo has
	// (inject.SettledTurn); a second copy here would drift from it.
	if request.Then != "" {
		settled := inject.SettledTurn{
			Capture: func(ctx context.Context) (string, error) {
				return tmux.Capture(ctx, request.SocketPath, request.Pane)
			},
			Sleep:      func(ctx context.Context, duration time.Duration) { _ = options.Clock.Sleep(ctx, duration) },
			Pane:       request.Pane,
			Min:        options.Poll,
			Poll:       options.Poll,
			Settle:     options.Poll,
			BusyTries:  renameBusyTries,
			IdleTries:  options.IdleTries,
			IdleStable: renameIdleStable,
		}
		switch observed, err := settled.Run(ctx); {
		case err != nil:
			fmt.Fprintf(
				stderr,
				"pfm chat reload --new: could not read %s while waiting out the steer's turn: %v — typing the rename anyway\n",
				request.Pane,
				err,
			)
		case !observed:
			fmt.Fprintf(
				stderr,
				"pfm chat reload --new: no turn boundary seen after the steer — the rename may land inside its turn\n",
			)
		}
	}
	rename := request
	rename.Then = "/rename " + request.Name
	since := options.Clock.Now()
	if err := deliverThen(ctx, rename, options, tmux, proc, stderr); err != nil {
		fmt.Fprintf(
			stderr,
			"pfm chat reload --new: the reborn chat is NOT renamed %q: %v — run: pfm chat name %s %q\n",
			request.Name,
			err,
			request.Pane,
			request.Name,
		)
	} else if landed, err := waitRenameLanded(ctx, request, options, since); landed {
		fmt.Fprintf(stderr, "pfm chat reload --new: reborn chat renamed %q\n", request.Name)
	} else {
		// Typed is not renamed. deliverThen proves keystrokes reached the
		// composer and were submitted; whether the harness executed them as a
		// slash command is a different claim, and printing the success line on
		// the first one is how a chat kept its auto-name while stderr said it
		// had been renamed. The claim now rests on the reborn transcript's own
		// custom-title record — and when that cannot be read, the line says so
		// and carries the recovery command.
		reason := "no custom-title record naming it appeared in the reborn transcript"
		if err != nil {
			reason = err.Error()
		}
		fmt.Fprintf(
			stderr,
			"pfm chat reload --new: the rename was typed but NOT confirmed (%s) — run: pfm chat name %s %q\n",
			reason,
			request.Pane,
			request.Name,
		)
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

const (
	// renameBusyTries bounds how many polls the settled-turn wait spends
	// looking for the steer's turn to BEGIN before it falls back to steady
	// idle. A steer whose turn started and finished inside one poll is the
	// ordinary short answer, not a failure.
	renameBusyTries = 15
	// renameIdleStable is how many consecutive quiet samples end that turn —
	// the same two-in-a-row proof waitCallerIdle uses for the /exit.
	renameIdleStable = 2
	// renameConfirmTries bounds the readback, polled at options.Poll: the
	// harness writes its custom-title record when it executes /rename, which
	// is one short turn away, not minutes — and a rename that has not landed
	// by then is reported as unconfirmed rather than waited out in silence.
	renameConfirmTries = 15
	// renameMTimeSkew is the slack waitRenameLanded allows between this
	// process's clock and the filesystem's timestamp granularity.
	renameMTimeSkew = 2 * time.Second
)

// waitRenameLanded reads the rename back rather than assuming it. A Claude
// /rename writes a custom-title record into the LIVE session's transcript
// (the shape TranscriptTitle reads, and index/claude.go after it), so the
// reborn transcript — any transcript under the account's roots written since
// the keystrokes went in, other than the one this reboot abandoned — carrying
// exactly this name is the rename having taken effect.
//
// The three states stay distinct: landed, not landed, and could-not-look. An
// engine whose rename leaves no such record, or a caller that handed down no
// roots to look in, is the last one — an error, never a quiet "no".
func waitRenameLanded(ctx context.Context, request Request, options Options, since time.Time) (bool, error) {
	if request.Engine != pfmengine.Claude {
		return false, fmt.Errorf(
			"a %s rename leaves no custom-title record this can read back",
			engineLabel(request.Engine),
		)
	}
	if len(options.ClaudeRoots) == 0 {
		return false, errors.New("no Claude session roots were given to read the new name back from")
	}
	// The cutoff is nudged back because a file's mtime and this process's
	// clock are not the same clock: a filesystem with coarse timestamp
	// granularity can stamp a file written just now slightly EARLIER than the
	// instant we read before typing, and a confirmation missed that way would
	// report a rename that did happen as one that did not.
	cutoff := since.Add(-renameMTimeSkew)
	var lastErr error
	for attempt := 0; attempt < renameConfirmTries; attempt++ {
		found, err := renameRecorded(options.ClaudeRoots, request.Transcript, request.Name, cutoff)
		if err != nil {
			lastErr = err
		}
		if found {
			return true, nil
		}
		if err := options.Clock.Sleep(ctx, options.Poll); err != nil {
			return false, err
		}
	}
	if lastErr != nil {
		return false, fmt.Errorf("could not read the reborn transcript back: %w", lastErr)
	}
	return false, nil
}

// renameRecorded is one sweep of the roots for a transcript written since the
// rename was typed whose resolved title is exactly the requested name.
func renameRecorded(roots []string, skip, name string, since time.Time) (bool, error) {
	found := false
	var walkErr error
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			switch {
			case found:
				return filepath.SkipAll
			case err != nil:
				// A directory that could not be walked is a failure to LOOK.
				// It is remembered and the sweep continues: another root may
				// still hold the answer, and a partial look must never report
				// itself as "not renamed".
				walkErr = errors.Join(walkErr, err)
				return nil
			case entry.IsDir() || filepath.Ext(path) != ".jsonl" || path == skip:
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				walkErr = errors.Join(walkErr, err)
				return nil
			}
			if info.ModTime().Before(since) {
				return nil
			}
			title, err := TranscriptTitle(path)
			if err != nil {
				walkErr = errors.Join(walkErr, err)
				return nil
			}
			if title == name {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil {
			walkErr = errors.Join(walkErr, err)
		}
	}
	return found, walkErr
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
