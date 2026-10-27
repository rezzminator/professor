package statusline

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/sky"
)

func fleetSegment(runtime Runtime) string {
	counts, err := fleetCountsWithError(runtime)
	if err != nil {
		fmt.Fprintf(runtime.Warn, "statusline: %v\n", err)
		return cWarn + "fleet!" + reset
	}
	return sky.SnapshotCounts(counts)
}

func gitSegment(ctx context.Context, runtime Runtime, directory string) string {
	if directory == "" {
		return ""
	}
	failed := func(err error) string {
		fmt.Fprintf(runtime.Warn, "statusline: git %s: %v\n", directory, err)
		return cWarn + "git!" + reset
	}
	if err := os.MkdirAll(runtime.CacheDir, 0o700); err != nil {
		return failed(err)
	}
	digest := md5.Sum([]byte(directory))
	path := filepath.Join(runtime.CacheDir, "cc-sl-"+hex.EncodeToString(digest[:]))
	if statuslineFileAge(path, runtime.now()) > 5*time.Second {
		commandCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		branch, err := runtime.Command.Output(
			commandCtx,
			"git",
			"-C",
			directory,
			"--no-optional-locks",
			"symbolic-ref",
			"--quiet",
			"--short",
			"HEAD",
		)
		cancel()
		if err != nil || strings.TrimSpace(string(branch)) == "" {
			// A detached HEAD succeeds here; an ordinary non-repository exits
			// 128 with Git's explicit diagnostic. Other failures stay visible.
			probeCtx, probeCancel := context.WithTimeout(ctx, 750*time.Millisecond)
			inside, probeErr := runtime.Command.Output(
				probeCtx,
				"git",
				"-C",
				directory,
				"rev-parse",
				"--is-inside-work-tree",
			)
			probeCancel()
			if probeErr != nil {
				var exit *commandExitError
				if errors.As(probeErr, &exit) && exit.code == 128 &&
					strings.HasPrefix(exit.stderr, "fatal: not a git repository") {
					return ""
				}
				return failed(fmt.Errorf("branch: %v; worktree probe: %w", err, probeErr))
			}
			if strings.TrimSpace(string(inside)) != "true" {
				return ""
			}
			var exit *commandExitError
			if !errors.As(err, &exit) || exit.code != 1 || exit.stderr != "" {
				return failed(fmt.Errorf("symbolic-ref: %v", err))
			}
			branch = []byte("HEAD")
		}
		staged, err := gitDiffCount(ctx, runtime, directory, true)
		if err != nil {
			return failed(err)
		}
		modified, err := gitDiffCount(ctx, runtime, directory, false)
		if err != nil {
			return failed(err)
		}
		if err := atomicfile.Write(
			path,
			[]byte(fmt.Sprintf("%s|%d|%d", strings.TrimSpace(string(branch)), staged, modified)),
			0o600,
		); err != nil {
			return failed(err)
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return failed(err)
	}
	fields := strings.Split(strings.TrimSpace(string(body)), "|")
	if len(fields) != 3 || fields[0] == "" {
		return failed(fmt.Errorf("malformed branch cache"))
	}
	staged, err := strconv.Atoi(fields[1])
	if err != nil || staged < 0 {
		return failed(fmt.Errorf("invalid staged count %q", fields[1]))
	}
	modified, err := strconv.Atoi(fields[2])
	if err != nil || modified < 0 {
		return failed(fmt.Errorf("invalid modified count %q", fields[2]))
	}
	color, suffix := green, ""
	if staged > 0 {
		suffix += fmt.Sprintf(" %s+%d", green, staged)
		color = yellow
	}
	if modified > 0 {
		suffix += fmt.Sprintf(" %s~%d", yellow, modified)
		color = yellow
	}
	return color + "🌿 " + fields[0] + suffix + reset
}

func gitDiffCount(ctx context.Context, runtime Runtime, directory string, staged bool) (int, error) {
	args := []string{"-C", directory, "--no-optional-locks", "diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--numstat")
	commandCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	output, err := runtime.Command.Output(commandCtx, "git", args...)
	if err != nil {
		return 0, fmt.Errorf("diff staged=%t: %w", staged, err)
	}
	return len(strings.FieldsFunc(strings.TrimSpace(string(output)), func(r rune) bool { return r == '\n' })), nil
}
