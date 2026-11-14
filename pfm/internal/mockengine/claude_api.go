package mockengine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// claudeAPITransport carries the request a headless Claude sends its ANTHROPIC_BASE_URL; tests replace it.
var claudeAPITransport http.RoundTripper = http.DefaultTransport

const (
	envClaudeBaseURL = "ANTHROPIC_BASE_URL"
	envClaudeAPIKey  = "ANTHROPIC_API_KEY"
	// envClaudeNoRemoteFlags is Claude Code's switch that skips its remote feature-flag fetch.
	envClaudeNoRemoteFlags = "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"
	claudeAPITimeout       = 10 * time.Second
	// claudeConnectionError is Claude Code's reply when its API endpoint cannot be reached.
	claudeConnectionError = "API Error: Connection error."
)

// claudeHarnessInstructions is the fake's harness system prompt, the block pfm doctor's
// harness-prompt check hashes.
const claudeHarnessInstructions = `You are an interactive agent that helps users with software engineering tasks.

# System
 - All text you output outside of tool use is displayed to the user.

# Doing tasks
 - Read the code before you change it.`

// apiBlock is one system block of a Messages API request.
type apiBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// apiRequest is the Messages API request a headless Claude sends.
type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    []apiBlock   `json:"system"`
	Messages  []apiMessage `json:"messages"`
}

type apiMessage struct {
	Role    string     `json:"role"`
	Content []apiBlock `json:"content"`
}

// settingEnv reads a variable as Claude Code sees it: the env block of the account's settings.json,
// then the project's .claude/settings.json, then its .claude/settings.local.json, each over the one
// before and all over the process environment.
func (session *claudeSession) settingEnv(name string) (string, error) {
	value := session.proc.env(name)
	for _, path := range []string{
		filepath.Join(session.configDir, "settings.json"),
		filepath.Join(session.proc.cwd, ".claude", "settings.json"),
		filepath.Join(session.proc.cwd, ".claude", "settings.local.json"),
	} {
		content, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		var document struct {
			Env map[string]string `json:"env"`
		}
		if err := json.Unmarshal(content, &document); err != nil {
			return "", fmt.Errorf("decode %s: %w", path, err)
		}
		if set, ok := document.Env[name]; ok {
			value = set
		}
	}
	return value, nil
}

// systemBlocks is the system prompt a headless Claude sends: the billing header, the identity line and
// the harness instructions. Unless envClaudeNoRemoteFlags is 1 the instructions end with the
// experiment the remote flag service assigned, bucketed on the config dir as the real service buckets
// a fresh install, so two captures from fresh config dirs differ.
func (session *claudeSession) systemBlocks() ([]apiBlock, error) {
	instructions := claudeHarnessInstructions
	noFlags, err := session.settingEnv(envClaudeNoRemoteFlags)
	if err != nil {
		return nil, err
	}
	if noFlags != "1" {
		bucket := sha256.Sum256([]byte(session.configDir))
		instructions += "\n\n# Experiments\n - Experiment bucket " + hex.EncodeToString(bucket[:4]) + "."
	}
	build, _, _ := strings.Cut(session.proc.script.Version, " ")
	return []apiBlock{
		{Type: blockText, Text: "x-anthropic-billing-header: cc_version=" + build + "; cc_entrypoint=sdk-cli;"},
		{Type: blockText, Text: "You are Claude Code, Anthropic's official CLI for Claude."},
		{Type: blockText, Text: instructions},
	}, nil
}

// askAPI sends a headless prompt's Messages request to ANTHROPIC_BASE_URL when one is set, as the real
// CLI does before its turn. It returns the reply Claude prints when the endpoint refuses or cannot be
// reached, "" when there is no endpoint or it answered.
func (session *claudeSession) askAPI(prompt string) (string, error) {
	base, err := session.settingEnv(envClaudeBaseURL)
	if err != nil || base == "" {
		return "", err
	}
	key, err := session.settingEnv(envClaudeAPIKey)
	if err != nil {
		return "", err
	}
	system, err := session.systemBlocks()
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(apiRequest{
		Model: session.model, MaxTokens: 32000, System: system,
		Messages: []apiMessage{{Role: roleUser, Content: []apiBlock{{Type: blockText, Text: prompt}}}},
	})
	if err != nil {
		return "", fmt.Errorf("encode the Messages request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		session.proc.ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/messages", bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("build the Messages request to %s: %w", base, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Anthropic-Version", "2023-06-01")
	request.Header.Set("X-Api-Key", key)
	response, err := (&http.Client{Transport: claudeAPITransport, Timeout: claudeAPITimeout}).Do(request)
	if err != nil {
		return claudeConnectionError, nil
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusMultipleChoices {
		return "", nil
	}
	refusal, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("read the %d answer from %s: %w", response.StatusCode, base, err)
	}
	return fmt.Sprintf("API Error: %d %s", response.StatusCode, strings.TrimSpace(string(refusal))), nil
}
