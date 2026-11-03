package mockengine

func (session *claudeSession) turnError(step Step) error {
	record := session.assistantRecord([]contentPart{{Type: blockText, Text: step.Error}}, Tokens{}, false)
	record.IsAPIErrorMessage, record.Error = true, "unknown"
	if err := session.write(record); err != nil {
		return err
	}
	payload := session.payload("StopFailure")
	payload.Error, payload.ErrorDetails = "unknown", step.Error
	payload.LastAssistantMessage = "API Error: " + step.Error
	_, err := session.fire("StopFailure", "unknown", payload)
	return err
}

func (session *codexSession) turnError(step Step) error {
	return session.write(codexRecord{Type: "event_msg", Payload: map[string]any{
		keyType:              "task_complete",
		"last_agent_message": nil,
		keyError:             map[string]string{payloadMessage: step.Error, "codex_error_info": "other"},
	}})
}

func (session *openCodeSession) turnError(step Step) error {
	if err := session.event("session.error", map[string]any{
		openCodeSessionIDKey: session.id,
		keyError: map[string]any{
			openCodeNameKey: "UnknownError",
			keyData:         map[string]string{payloadMessage: step.Error},
		},
	}); err != nil {
		return err
	}
	return session.stop()
}
