package mockengine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func (state *openCodeServerState) message(writer http.ResponseWriter, request *http.Request, sessionID string) {
	var input struct {
		NoReply bool            `json:"noReply"`
		Format  json.RawMessage `json:"format"`
		Parts   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&input); err != nil {
		http.Error(writer, fmt.Sprintf("decode opencode message: %v", err), http.StatusBadRequest)
		return
	}
	if input.NoReply || len(input.Format) != 0 {
		http.Error(writer, openCodeSchemaRefusal, http.StatusNotImplemented)
		return
	}
	var texts []string
	for _, part := range input.Parts {
		if part.Type == blockText {
			texts = append(texts, part.Text)
		}
	}
	prompt := strings.Join(texts, "\n")
	state.mu.Lock()
	state.next++
	number := state.next
	state.mu.Unlock()
	userID, assistantID := fmt.Sprintf("msg_user%08d", number), fmt.Sprintf("msg_assistant%08d", number)
	state.scriptMu.Lock()
	session, sessionErr := state.session(sessionID)
	if sessionErr != nil {
		state.scriptMu.Unlock()
		http.Error(writer, sessionErr.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := session.prompt(prompt); err != nil {
		state.scriptMu.Unlock()
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := session.recordUser(prompt); err != nil {
		state.scriptMu.Unlock()
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	running := state.proc.script.forPrompt(prompt)
	step, stepErr := session.headlessSteps(running)
	state.scriptMu.Unlock()
	if stepErr != nil && step.Type != StepCrash && step.Type != StepExit {
		http.Error(writer, stepErr.Error(), http.StatusInternalServerError)
		return
	}
	if step.Type == StepCrash || step.Type == StepExit {
		message := fmt.Sprintf("crash step: exit %d", step.ExitCode)
		if step.Reply != "" {
			message += "\n" + step.Reply
		}
		marker := map[string]any{
			openCodeSessionIDKey: sessionID,
			"userMessageID":      userID,
			"assistantIDs":       []string{},
			keyError: map[string]any{
				openCodeNameKey: "MockCrash",
				keyData:         map[string]string{payloadMessage: message},
			},
		}
		if err := state.marker(marker); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(writer, message, http.StatusInternalServerError)
		return
	}
	if step.Error != "" {
		if err := session.turnError(step); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(writer, step.Error, http.StatusInternalServerError)
		return
	}
	reply, busyMS, usage := running.turnReply(step)
	if !sleepOrCancel(request.Context(), time.Duration(busyMS)*time.Millisecond) {
		http.Error(writer, "opencode message cancelled", http.StatusInternalServerError)
		return
	}
	if err := session.recordAssistant(reply, usage); err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := session.stop(); err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now().UnixMilli()
	cost := usageCost(usage)
	tokens := map[string]any{
		openCodeInputKey:  usage.Input,
		openCodeOutputKey: usage.Output,
		"reasoning":       0,
		"cache":           map[string]int64{openCodeCacheReadKey: usage.CacheRead, "write": usage.CacheCreation},
	}
	partBase := map[string]any{openCodeSessionIDKey: sessionID, openCodeMessageIDKey: assistantID}
	textPart := map[string]any{
		"id":                 fmt.Sprintf("prt_text%08d", number),
		openCodeSessionIDKey: partBase[openCodeSessionIDKey],
		openCodeMessageIDKey: partBase[openCodeMessageIDKey],
		keyType:              blockText,
		blockText:            reply,
		openCodeTimeKey:      map[string]int64{"end": now},
	}
	finishPart := map[string]any{
		"id":                 fmt.Sprintf("prt_finish%08d", number),
		openCodeSessionIDKey: sessionID,
		openCodeMessageIDKey: assistantID,
		keyType:              "step-finish",
		"reason":             "stop",
		openCodeCostKey:      cost,
		openCodeTokensKey:    tokens,
	}
	snapshot := map[string]any{
		openCodeInfoKey: map[string]any{
			"id":                 assistantID,
			openCodeSessionIDKey: sessionID,
			openCodeRoleKey:      "assistant",
			"parentID":           userID,
			"finish":             "stop",
			openCodeCostKey:      cost,
			openCodeTokensKey:    tokens,
			openCodeTimeKey:      map[string]int64{openCodeCreatedKey: now, "completed": now},
		},
		openCodePartsKey: []any{textPart, finishPart},
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		http.Error(writer, fmt.Sprintf("encode assistant snapshot: %v", err), http.StatusInternalServerError)
		return
	}
	state.mu.Lock()
	state.messages[assistantID] = encoded
	state.mu.Unlock()
	marker := map[string]any{
		openCodeSessionIDKey: sessionID,
		"userMessageID":      userID,
		"assistantIDs":       []string{assistantID},
		keyError:             nil,
	}
	if err := state.marker(marker); err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	if _, err := writer.Write(encoded); err != nil {
		warn(state.proc.stderr, "write assistant response: %v", err)
	}
}

func (state *openCodeServerState) marker(value any) error {
	path := state.proc.env("PFM_OPENCODE_ASSISTANTS_FILE")
	if path == "" {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode opencode assistant marker: %w", err)
	}
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return fmt.Errorf("publish opencode assistant marker: %w", err)
	}
	return nil
}

func (state *openCodeServerState) session(id string) (*openCodeSession, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if session := state.engines[id]; session != nil {
		return session, nil
	}
	session := newOpenCodeSession(state.proc, openCodeInvocation{subcommand: openCodeRunSubcommand, sessionID: id})
	session.plugins = state.plugins
	if err := session.start(false); err != nil {
		return nil, err
	}
	state.plugins = session.plugins
	if state.engines == nil {
		state.engines = map[string]*openCodeSession{}
	}
	state.engines[id] = session
	return session, nil
}

func (state *openCodeServerState) close() {
	if state.plugins != nil {
		for _, session := range state.engines {
			if err := session.notification(Step{NotificationType: "global.disposed"}); err != nil {
				warn(state.proc.stderr, "dispose OpenCode plugins: %v", err)
			}
			break
		}
		if err := state.plugins.close(); err != nil {
			warn(state.proc.stderr, "close OpenCode plugins: %v", err)
		}
	}
	for _, session := range state.engines {
		if err := session.database.Close(); err != nil {
			warn(state.proc.stderr, "close OpenCode store: %v", err)
		}
	}
}
