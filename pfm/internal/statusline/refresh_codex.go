package statusline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
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

// takeStatuslineRefreshLock creates lockPath with O_EXCL, stamped now;
// held=false with a nil error means a live refresher owns it. A refresher
// ends within its App Server timeout, far inside ttl, so a lock older than ttl
// (or dated more than ttl ahead) is one whose refresher died before its
// deferred unlock; left in place it would suppress every refresh after it.
// It is cleared only under an flock on lockPath+".guard" and re-read there, so
// two renders that both see one orphan never remove the lock the other just
// took: one takes it, the other spawns nothing.
func takeStatuslineRefreshLock(lockPath string, now time.Time, ttl time.Duration) (bool, error) {
	held, err := createStatuslineRefreshLock(lockPath, now)
	if held || err != nil {
		return held, err
	}
	if age := statuslineFileAge(lockPath, now); age <= ttl && age >= -ttl {
		return false, nil
	}
	guard, err := os.OpenFile(lockPath+".guard", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, fmt.Errorf("open refresh lock guard: %w", err)
	}
	defer func() {
		if closeErr := guard.Close(); closeErr != nil {
			obs.Logger(context.Background()).
				Warn("close statusline refresh lock guard", "path", lockPath+".guard", obs.FieldErr, closeErr.Error())
		}
	}()
	if err := syscall.Flock(int(guard.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil // another render is clearing the orphan
		}
		return false, fmt.Errorf("lock refresh lock guard: %w", err)
	}
	info, err := os.Stat(lockPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return false, fmt.Errorf("inspect refresh lock: %w", err)
	case now.Sub(info.ModTime()) <= ttl && now.Sub(info.ModTime()) >= -ttl:
		return false, nil // taken afresh since the first look
	default:
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("clear orphaned refresh lock: %w", err)
		}
	}
	return createStatuslineRefreshLock(lockPath, now)
}

// createStatuslineRefreshLock is one O_EXCL create; an existing lock is
// held=false with a nil error. The lock is stamped now, the clock its age is
// judged by.
func createStatuslineRefreshLock(lockPath string, now time.Time) (bool, error) {
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create refresh lock: %w", err)
	}
	err = errors.Join(lock.Close(), os.Chtimes(lockPath, now, now))
	if err != nil {
		if removeErr := os.Remove(lockPath); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove refresh lock after the failed stamp: %w", removeErr))
		}
		return false, fmt.Errorf("stamp refresh lock: %w", err)
	}
	return true, nil
}
