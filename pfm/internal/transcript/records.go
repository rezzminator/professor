package transcript

// ResponseKey names the API response an assistant record belongs to: one
// response is one key, however many lines repeat its usage. The message id,
// else the request id, else the record's uuid, else at (where the record sits,
// such as file:line); each form is namespaced, so a request id never equals a
// message id. "" when every form is empty.
func ResponseKey(messageID, requestID, uuid, at string) string {
	switch {
	case messageID != "":
		return "message:" + messageID
	case requestID != "":
		return "request:" + requestID
	case uuid != "":
		return "uuid:" + uuid
	case at != "":
		return "at:" + at
	}
	return ""
}

// CodexTokenUsage is one usage object of a Codex token_count event.
// CachedInputTokens is a part of InputTokens, ReasoningOutputTokens a part of
// OutputTokens.
type CodexTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

// Total is the usage's total_tokens; 0 for a usage the event did not carry.
func (usage *CodexTokenUsage) Total() int64 {
	if usage == nil {
		return 0
	}
	return usage.TotalTokens
}

// CodexTokenInfo is a token_count event's info: the thread's cumulative usage
// (it resets on resume and compaction), the last request's, and the model's
// window. A usage the event does not carry is nil.
type CodexTokenInfo struct {
	TotalTokenUsage    *CodexTokenUsage `json:"total_token_usage"`
	LastTokenUsage     *CodexTokenUsage `json:"last_token_usage"`
	ModelContextWindow int64            `json:"model_context_window"`
}
