package mockengine

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

//go:embed testdata/capture/*.json testdata/capture/*.model
var claudeCaptureRequests embed.FS

var claudeCaptureModels = []string{"sonnet", "opus"}

func (session *claudeSession) captureRequest(prompt string) (bool, error) {
	proc := session.proc
	if !slices.Contains(proc.args, "--exclude-dynamic-system-prompt-sections") {
		return false, nil
	}
	base := proc.env("ANTHROPIC_BASE_URL")
	if base == "" {
		return false, nil
	}
	endpoint, err := url.Parse(base)
	if err != nil {
		return true, fmt.Errorf("mock Claude capture endpoint: %w", err)
	}
	ip := net.ParseIP(endpoint.Hostname())
	if endpoint.Scheme != "http" || ip == nil || !ip.IsLoopback() || endpoint.User != nil {
		return true, fmt.Errorf("mock Claude capture requires an HTTP loopback endpoint: %s", base)
	}
	if prompt != "x" || session.call.systemPrompt != "" || session.call.systemPromptFile != "" ||
		!claudeCaptureOneTurn(proc.args) {
		return true, fmt.Errorf(
			"unscripted Claude capture request: expected prompt x, max-turns 1 and the recorded system prompt",
		)
	}
	alias := session.model
	for _, known := range claudeCaptureModels {
		model, readErr := claudeCaptureRequests.ReadFile("testdata/capture/" + known + ".model")
		if readErr != nil {
			return true, fmt.Errorf("read mock Claude capture model: %w", readErr)
		}
		if session.model == strings.TrimSpace(string(model)) {
			alias = known
		}
	}
	if !slices.Contains(claudeCaptureModels, alias) {
		return true, fmt.Errorf("unscripted Claude capture model %q: record its request first", session.model)
	}
	body, err := claudeCaptureRequests.ReadFile("testdata/capture/" + alias + ".json")
	if err != nil {
		return true, fmt.Errorf("read mock Claude capture request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		proc.ctx,
		http.MethodPost,
		endpoint.JoinPath("v1", "messages").String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return true, fmt.Errorf("build mock Claude capture request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	request.Header.Set("x-api-key", "pfm-doctor-sink")
	client := obs.WrapClient(
		&http.Client{
			Timeout:       hookTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	)
	response, err := client.Do(request)
	if err != nil {
		return true, fmt.Errorf("send mock Claude capture request: %w", err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return true, fmt.Errorf("read mock Claude capture rejection: %w", readErr)
	}
	if closeErr != nil {
		return true, fmt.Errorf("close mock Claude capture rejection: %w", closeErr)
	}
	if response.StatusCode != http.StatusBadRequest {
		return true, fmt.Errorf("mock Claude capture expected HTTP 400, received %d", response.StatusCode)
	}
	return true, nil
}

func claudeCaptureOneTurn(args []string) bool {
	for index, arg := range args {
		if arg == "--max-turns=1" || (arg == "--max-turns" && index+1 < len(args) && args[index+1] == "1") {
			return true
		}
	}
	return false
}
