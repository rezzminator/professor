package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/obs"
)

// openCodeProbeTimeout bounds ONE startup probe, never the wait for the
// server: the health and tool-catalog polls retry until the run's own context
// expires, and a single attempt that hangs must not eat that whole budget.
const openCodeProbeTimeout = 2 * time.Second

// One spelling per OpenCode wire literal this package both writes and reads.
const (
	openCodeRunCommand     = "run"
	openCodeAttachFlag     = "--attach"
	openCodeStructuredTool = "StructuredOutput"
	openCodeToolEdit       = "edit"
	openCodeToolRead       = "read"
	openCodeStateCompleted = "completed"
	openCodePartTool       = "tool"
	openCodeToolApplyPatch = "apply_patch"
)

type openCodeServer struct {
	process deps.Process
	stdout  boundedBuffer
	stderr  boundedBuffer
	done    <-chan error
}

// OpenCode may choose another port if a requested port is occupied. Consume
// its published loopback address instead of reserving and releasing a port
// that another process can acquire before OpenCode binds it.
type openCodeListenWriter struct {
	capture   *boundedBuffer
	listening chan<- string
	pending   string
	published bool
}

func (writer *openCodeListenWriter) Write(body []byte) (int, error) {
	n, err := writer.capture.Write(body)
	if writer.published {
		return n, err
	}
	writer.pending += string(body)
	for {
		line, rest, found := strings.Cut(writer.pending, "\n")
		if !found {
			break
		}
		writer.pending = rest
		_, address, found := strings.Cut(line, "opencode server listening on ")
		if !found {
			continue
		}
		parsed, parseErr := url.Parse(strings.TrimSpace(address))
		if parseErr != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			continue
		}
		port, portErr := strconv.Atoi(parsed.Port())
		if portErr != nil || port <= 0 || port > 65535 {
			continue
		}
		writer.published = true
		writer.listening <- parsed.String()
		break
	}
	if len(writer.pending) > 4096 {
		writer.pending = writer.pending[len(writer.pending)-4096:]
	}
	return n, err
}

// openCodeRun is one prepared OpenCode invocation: the private environment
// the child inherits, the attached server's loopback address, and the teardown
// that stops the server and removes the private run directory.
type openCodeRun struct {
	environment []string
	address     string
	server      *openCodeServer
	release     func() error
}

// startOpenCode materializes the private plugin/config, launches the attached
// server, and records the plugin handshake path on request. Every failure
// leaves nothing behind: the private directory is removed before it returns.
func startOpenCode(ctx context.Context, request *Request, environment []string, cwd string) (openCodeRun, error) {
	private := append([]string(nil), environment...)
	release, ready, err := prepareOpenCode(environment, *request, cwd, &private)
	if err != nil {
		return openCodeRun{}, err
	}
	request.openCodePluginReady = ready
	server, address, serverErr := startOpenCodeServer(ctx, *request, private, cwd, ready)
	if serverErr != nil {
		if releaseErr := release(); releaseErr != nil {
			return openCodeRun{}, fmt.Errorf("%v; cleanup OpenCode run: %w", serverErr, releaseErr)
		}
		return openCodeRun{}, serverErr
	}
	return openCodeRun{environment: private, address: address, server: server, release: release}, nil
}

// stop tears the run down in launch order — server, private directory, then
// whatever cleanup the caller already held — and reports EVERY failure, so a
// leaked server never hides behind a successful directory removal.
func (run openCodeRun) stop(previous func() error) error {
	var failures []error
	if err := stopOpenCodeServer(run.server); err != nil {
		failures = append(failures, fmt.Errorf("stop OpenCode server: %w", err))
	}
	if run.release != nil {
		if err := run.release(); err != nil {
			failures = append(failures, fmt.Errorf("clean up OpenCode run: %w", err))
		}
	}
	if err := previous(); err != nil {
		failures = append(failures, fmt.Errorf("prior headless cleanup: %w", err))
	}
	return errors.Join(failures...)
}

func startOpenCodeServer(
	ctx context.Context,
	request Request,
	environment []string,
	cwd, ready string,
) (*openCodeServer, string, error) {
	startupCtx := ctx
	if request.Timeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			startupCtx, cancel = context.WithTimeout(ctx, request.Timeout)
			defer cancel()
		}
	}
	server := &openCodeServer{}
	server.stdout.limited = true
	server.stderr.limited = true
	listening := make(chan string, 1)
	runner := request.Runner
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	process, err := runner.Start(
		ctx,
		[]string{request.binaryPath, "serve", "--hostname", "127.0.0.1", "--port", "0"},
		deps.StartOptions{
			Env: environment, Dir: cwd,
			Stdout:       &openCodeListenWriter{capture: &server.stdout, listening: listening},
			Stderr:       &server.stderr,
			ProcessGroup: true, WaitDelay: processWaitAfterCancel,
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf("start OpenCode server: %w", err)
	}
	server.process = process
	done := make(chan error, 1)
	server.done = done
	go func() { done <- process.Wait() }()
	ticker := request.Clock.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var address string
	select {
	case address = <-listening:
	case err := <-done:
		return nil, "", fmt.Errorf(
			"OpenCode server exited before announcing its address: %v; stdout %q; stderr %q",
			err, boundedTail(server.stdout.String(), 1024), boundedTail(server.stderr.String(), 1024),
		)
	case <-startupCtx.Done():
		stopErr := stopOpenCodeServer(server)
		return nil, "", errors.Join(fmt.Errorf(
			"wait for OpenCode listening address: %w; stdout %q; stderr %q",
			startupCtx.Err(), boundedTail(server.stdout.String(), 1024), boundedTail(server.stderr.String(), 1024),
		), stopErr)
	}

	for {
		healthy, healthErr := openCodeServerHealthy(startupCtx, address, environment)
		if healthErr != nil {
			return nil, "", openCodeStartupFailure(server, fmt.Errorf(
				"%w; OpenCode startup stdout %q; stderr %q",
				healthErr, boundedTail(server.stdout.String(), 1024), boundedTail(server.stderr.String(), 1024),
			))
		}
		if healthy {
			catalogReady, catalogErr := openCodeToolCatalog(startupCtx, address, cwd, environment)
			if catalogErr != nil {
				return nil, "", openCodeStartupFailure(server, fmt.Errorf(
					"%w; OpenCode plugin startup stdout %q; stderr %q",
					catalogErr, boundedTail(server.stdout.String(), 1024), boundedTail(server.stderr.String(), 1024),
				))
			}
			if catalogReady {
				if err := verifyOpenCodePluginPath(ready); err != nil {
					return nil, "", openCodeStartupFailure(server, err)
				}
				return server, address, nil
			}
		}
		select {
		case err := <-done:
			return nil, "", openCodePreflightExit(err, boundedTail(server.stderr.String(), 1024))
		case <-startupCtx.Done():
			if stopErr := stopOpenCodeServer(server); stopErr != nil {
				return nil, "", fmt.Errorf(
					"wait for OpenCode plugin handshake: %v; stop OpenCode server: %w",
					startupCtx.Err(), stopErr,
				)
			}
			return nil, "", fmt.Errorf("wait for OpenCode plugin handshake: %w", startupCtx.Err())
		case <-ticker.C():
		}
	}
}

// openCodeStartupFailure stops the half-started server and returns cause with
// any stop failure named alongside it — a cleanup that itself failed is never
// swallowed behind the reason the caller is already reporting.
func openCodeStartupFailure(server *openCodeServer, cause error) error {
	if stopErr := stopOpenCodeServer(server); stopErr != nil {
		return fmt.Errorf("%v; stop OpenCode server: %w", cause, stopErr)
	}
	return cause
}

// openCodePreflightExit names a server that died before the plugin handshake.
// An empty stderr is stated as such rather than rendered as an empty quote the
// reader cannot tell from "we never looked".
func openCodePreflightExit(waitErr error, tail string) error {
	switch {
	case waitErr == nil && tail == "":
		return errors.New("OpenCode server exited before plugin preflight")
	case waitErr == nil:
		return fmt.Errorf("OpenCode server exited before plugin preflight; stderr tail %q", tail)
	case tail == "":
		return fmt.Errorf("OpenCode server exited before plugin preflight: %w", waitErr)
	default:
		return fmt.Errorf("OpenCode server exited before plugin preflight: %w; stderr tail %q", waitErr, tail)
	}
}

// openCodeClient is this package's one outbound HTTP door, wrapped so every
// request against the local OpenCode server lands in the activity log.
func openCodeClient() *http.Client { return obs.WrapClient(&http.Client{}) }

func openCodeAuthorize(request *http.Request, environment []string) {
	username := environmentValue(environment, "OPENCODE_SERVER_USERNAME")
	password := environmentValue(environment, "OPENCODE_SERVER_PASSWORD")
	if password != "" {
		request.SetBasicAuth(username, password)
	}
}

func openCodeServerHealthy(
	ctx context.Context,
	address string,
	environment []string,
) (healthy bool, probeErr error) {
	attemptCtx, cancel := context.WithTimeout(ctx, openCodeProbeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, address+"/global/health", http.NoBody)
	if err != nil {
		return false, fmt.Errorf("build OpenCode health request: %w", err)
	}
	openCodeAuthorize(request, environment)
	response, err := openCodeClient().Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// The server is still binding, or this one attempt ran out of its own
		// probe budget. The outer run context bounds the retries.
		return false, nil
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			probeErr = errors.Join(probeErr, fmt.Errorf("close OpenCode health response: %w", closeErr))
		}
	}()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return true, nil
	}
	if response.StatusCode >= http.StatusInternalServerError {
		return false, nil
	}
	return false, fmt.Errorf("OpenCode health request failed with HTTP status %d", response.StatusCode)
}

// openCodeToolCatalog forces OpenCode's ToolRegistry to initialize. The
// server is lazy: merely listening does not load the configured plugin, so a
// run could otherwise reach the model before a plugin failure was visible.
func openCodeToolCatalog(
	ctx context.Context,
	address, cwd string,
	environment []string,
) (ready bool, catalogErr error) {
	attemptCtx, cancel := context.WithTimeout(ctx, openCodeProbeTimeout)
	defer cancel()
	endpoint, err := url.Parse(address + "/experimental/tool/ids")
	if err != nil {
		return false, fmt.Errorf("build OpenCode tool catalog URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("directory", cwd)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, endpoint.String(), http.NoBody)
	if err != nil {
		return false, fmt.Errorf("build OpenCode tool catalog request: %w", err)
	}
	openCodeAuthorize(request, environment)
	response, err := openCodeClient().Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// The server is still binding or initializing. The outer run context,
		// including its explicit timeout, bounds these retries.
		return false, nil
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			catalogErr = errors.Join(catalogErr, fmt.Errorf("close OpenCode tool catalog response: %w", closeErr))
		}
	}()
	if response.StatusCode == http.StatusNotFound {
		return false, errors.New("OpenCode tool catalog endpoint is unavailable")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if response.StatusCode >= http.StatusInternalServerError {
			return false, fmt.Errorf(
				"OpenCode tool catalog request failed with HTTP status %d after server became healthy",
				response.StatusCode,
			)
		}
		return false, fmt.Errorf("OpenCode tool catalog request failed with HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return false, fmt.Errorf("read OpenCode tool catalog response: %w", err)
	}
	var ids []string
	if err := json.Unmarshal(body, &ids); err != nil {
		return false, fmt.Errorf("decode OpenCode tool catalog response: %w", err)
	}
	if catalogPath := environmentValue(environment, "PFM_OPENCODE_TOOL_IDS_FILE"); catalogPath != "" {
		encoded, err := json.Marshal(ids)
		if err != nil {
			return false, fmt.Errorf("encode OpenCode tool catalog: %w", err)
		}
		if err := os.WriteFile(catalogPath, encoded, 0o600); err != nil {
			return false, fmt.Errorf("write OpenCode tool catalog: %w", err)
		}
	}
	return true, nil
}

// primeOpenCodeSchema crosses the public prompt decoder once before the CLI
// run. OpenCode's json-schema format is an Effect Schema class, so the plugin
// cannot construct a compatible value with an object literal. The noReply
// request persists no model work and lets the plugin retain the decoder's real
// value for the subsequent run attached to this server.
func primeOpenCodeSchema(ctx context.Context, address, cwd string, environment []string, request Request) error {
	seedBody, err := openCodeJSON(ctx, http.MethodPost, address+"/session", cwd, environment, map[string]any{})
	if err != nil {
		return fmt.Errorf("create OpenCode schema seed session: %w", err)
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(seedBody, &session); err != nil {
		return fmt.Errorf("decode OpenCode schema seed session: %w", err)
	}
	if strings.TrimSpace(session.ID) == "" {
		return errors.New("OpenCode schema seed session response has no id")
	}
	removeSeed := func() error {
		endpoint := address + "/session/" + url.PathEscape(session.ID)
		if _, err := openCodeJSON(ctx, http.MethodDelete, endpoint, cwd, environment, nil); err != nil {
			return fmt.Errorf("remove OpenCode schema seed session: %w", err)
		}
		return nil
	}
	model, err := openCodeModelReference(request.Model)
	if err != nil {
		if removeErr := removeSeed(); removeErr != nil {
			return fmt.Errorf("%v; %w", err, removeErr)
		}
		return err
	}
	prompt := map[string]any{
		"noReply": true,
		"format": map[string]any{
			jsonKeyType:  "json_schema",
			"schema":     request.Schema,
			"retryCount": 2,
		},
		"parts": []map[string]string{{
			jsonKeyType:   jsonEventText,
			jsonEventText: "pfm schema format seed",
		}},
	}
	if model != nil {
		prompt["model"] = model
	}
	endpoint := address + "/session/" + url.PathEscape(session.ID) + "/message"
	_, promptErr := openCodeJSON(ctx, http.MethodPost, endpoint, cwd, environment, prompt)
	removeErr := removeSeed()
	if promptErr != nil {
		if removeErr != nil {
			return fmt.Errorf("prime OpenCode schema format: %v; %w", promptErr, removeErr)
		}
		return fmt.Errorf("prime OpenCode schema format: %w", promptErr)
	}
	if removeErr != nil {
		return removeErr
	}
	ready := environmentValue(environment, "PFM_OPENCODE_SCHEMA_READY")
	if ready == "" {
		return errors.New("OpenCode schema seed readiness path is missing")
	}
	return verifyOpenCodeSchemaPath(ready)
}

func openCodeJSON(
	ctx context.Context,
	method, endpoint, cwd string,
	environment []string,
	payload any,
) (_ []byte, apiErr error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("build OpenCode API URL: %w", err)
	}
	query := parsed.Query()
	query.Set("directory", cwd)
	parsed.RawQuery = query.Encode()
	var body io.Reader
	if payload != nil {
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return nil, fmt.Errorf("encode OpenCode API request: %w", marshalErr)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build OpenCode API request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	openCodeAuthorize(req, environment)
	response, err := openCodeClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("request OpenCode API: %w", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			apiErr = errors.Join(apiErr, fmt.Errorf("close OpenCode API response: %w", closeErr))
		}
	}()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxCapturedOutput))
	if readErr != nil {
		return nil, fmt.Errorf("read OpenCode API response: %w", readErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf(
			"OpenCode API %s %s failed with HTTP status %d",
			method,
			parsed.Path,
			response.StatusCode,
		)
	}
	if len(responseBody) == 0 && method != http.MethodDelete {
		return nil, fmt.Errorf("OpenCode API %s %s returned an empty response", method, parsed.Path)
	}
	return responseBody, nil
}

func openCodeModelReference(model string) (map[string]string, error) {
	model = openCodeModel(model)
	if model == "" {
		return nil, nil
	}
	provider, modelID, ok := strings.Cut(model, "/")
	if !ok || provider == "" || modelID == "" {
		return nil, fmt.Errorf("OpenCode model %q must use provider/model form", model)
	}
	return map[string]string{"providerID": provider, "modelID": modelID}, nil
}

func environmentValue(environment []string, name string) string {
	for _, item := range environment {
		key, value, ok := strings.Cut(item, "=")
		if ok && key == name {
			return value
		}
	}
	return ""
}

func stopOpenCodeServer(server *openCodeServer) error {
	if server == nil || server.process == nil {
		return nil
	}
	if server.done == nil {
		return errors.New("OpenCode server wait channel is missing")
	}
	killErr := server.process.KillGroup()
	waitErr := <-server.done
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return fmt.Errorf("stop OpenCode server process group: %w", killErr)
	}
	// A server this call killed exits on a signal by construction, so an
	// *exec.ExitError IS the successful stop. Only a wait that never reached
	// the process — a runner that lost it — is a failure worth naming.
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return fmt.Errorf("wait for OpenCode server: %w", waitErr)
	}
	return nil
}

func attachOpenCodeRun(args []string, address string) []string {
	if len(args) == 0 || args[0] != openCodeRunCommand {
		return append([]string{openCodeRunCommand, openCodeAttachFlag, address}, args...)
	}
	return append([]string{openCodeRunCommand, openCodeAttachFlag, address}, args[1:]...)
}
