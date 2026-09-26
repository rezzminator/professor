package statusline

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// CodexOptions describes the detached Codex App Server cache refresh.
type CodexOptions struct {
	CachePath      string
	Binary         string
	ReadRateLimits func(context.Context) ([]byte, error)
	Now            func() time.Time
	// Env is the host-environment seam (pfm/TESTPLAN.md § Seams, paths.Env)
	// the default CachePath is resolved through; nil defaults to
	// paths.OSEnv{}.
	Env paths.Env
}

// RefreshCodex extracts the id=1 account/rateLimits/read response from an App
// Server JSONL exchange and atomically replaces the cache.
func RefreshCodex(ctx context.Context, options CodexOptions) error {
	if options.CachePath == "" {
		env := options.Env
		if env == nil {
			env = paths.OSEnv{}
		}
		options.CachePath = CodexStatuslineCachePath(env.Get(paths.EnvHome), os.Getuid())
	}
	defer removeRefreshLock(options.CachePath)
	if options.ReadRateLimits == nil {
		options.ReadRateLimits = func(ctx context.Context) ([]byte, error) {
			return ReadCodexRateLimitsWithBinary(ctx, options.Binary)
		}
	}
	if options.Now == nil {
		options.Now = clock.Real.Now
	}
	body, err := options.ReadRateLimits(ctx)
	if err != nil {
		return err
	}
	rateLimits, err := parseCodexRateLimits(body)
	if err != nil {
		return err
	}
	primary, primaryOK := rateLimits["primary"]
	secondary, secondaryOK := rateLimits["secondary"]
	if !primaryOK && !secondaryOK {
		return fmt.Errorf("response from App Server carried no primary or secondary rate limit")
	}
	output := map[string]any{
		"primary":   primary,
		"secondary": secondary,
		"planType":  rateLimits["planType"],
		"ts":        options.Now().Unix(),
	}
	if credits, ok := rateLimits["credits"].(map[string]any); ok {
		output["credits"] = credits["balance"]
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	return atomicfile.Write(options.CachePath, encoded, 0o600)
}

func parseCodexRateLimits(body []byte) (map[string]any, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result struct {
				RateLimits map[string]any `json:"rateLimits"`
			} `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &message) != nil || string(message.ID) != "1" {
			continue
		}
		if len(message.Result.RateLimits) == 0 {
			return nil, fmt.Errorf("response id=1 from App Server omitted rateLimits")
		}
		return message.Result.RateLimits, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("response from App Server omitted id=1 rate limits")
}

func removeRefreshLock(cachePath string) {
	_ = os.Remove(strings.TrimSuffix(cachePath, ".json") + ".lock")
}
