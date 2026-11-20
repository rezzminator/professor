package installping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// post POSTs one ping of event within Timeout; anything but 204 is an error
// naming the status and the counter's one-line reason.
func post(ctx context.Context, options Options, event string) (returnErr error) {
	id, err := installID(options.StateDir)
	if err != nil {
		return err
	}
	plugins, err := claudeplugin.ListedEnabled(options.ClaudeConfigDir, pfmconfig.ProfessorMarketplace)
	if err != nil {
		// The plugins are one field: the ping still counts the install, with none.
		obs.Logger(ctx).Warn("install ping: enabled plugins unreadable, sending none",
			"path", options.ClaudeConfigDir, obs.FieldErr, err.Error())
		plugins = nil
	}
	ping, err := BuildPing(id, options.Version, options.OS, options.Arch, plugins, event)
	if err != nil {
		return err
	}
	body, err := json.Marshal(ping)
	if err != nil {
		return fmt.Errorf("encode the ping: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	// Best effort: a failed round trip is the caller's "install ping: not sent" WARN, never an ERROR record.
	request, err := http.NewRequestWithContext(
		obs.Presence(ctx),
		http.MethodPost,
		options.Endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := options.Client.Do(request)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close the counter's response: %w", err))
		}
	}()
	if response.StatusCode != http.StatusNoContent {
		reason, _ := io.ReadAll(io.LimitReader(response.Body, 200))
		return fmt.Errorf("counter answered %s: %s", response.Status, strings.TrimSpace(string(reason)))
	}
	return nil
}
