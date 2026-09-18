package professor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/obs"
	pfmpaths "hostops/pfm/internal/paths"
)

const UnknownSelfHostedSHA = "self-hosted@unknown"

type Store struct {
	Root      string
	Templates string
	Version   string
	SHA       string
	runner    deps.Runner
}

type projectManifest struct {
	Interview struct {
		BlueprintClonePath string `json:"blueprint_clone_path"`
	} `json:"interview"`
}

func HashTemplate(path string) (string, error) {
	raw, err := readStoreFile(path)
	if err != nil {
		return "", fmt.Errorf("UNREADABLE %s: %w", path, err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), nil
}

func ResolveStore(projectRoot, home string) (Store, error) {
	blueprintRoot := filepath.Join(home, ".professor")
	manifestPath := filepath.Join(projectRoot, ".professor", "manifest.json")
	raw, err := readStoreFile(manifestPath)
	if err == nil {
		var manifest projectManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return Store{}, fmt.Errorf("UNREADABLE %s: malformed JSON: %w", manifestPath, err)
		}
		if configured := strings.TrimSpace(manifest.Interview.BlueprintClonePath); configured != "" {
			blueprintRoot = expandStorePath(configured, projectRoot, home)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Store{}, fmt.Errorf("UNREADABLE %s: %w", manifestPath, err)
	}
	return InspectStoreWithRunner(blueprintRoot, obs.Runner(deps.RealRunner{}))
}

func InspectStore(root string) (Store, error) {
	return InspectStoreWithRunner(root, obs.Runner(deps.RealRunner{}))
}

func InspectStoreWithRunner(root string, runner deps.Runner) (Store, error) {
	if runner == nil {
		return Store{}, errors.New("inspect blueprint store: runner is nil")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Store{}, fmt.Errorf("resolve blueprint store %s: %w", root, err)
	}
	templates := filepath.Join(absolute, "templates")
	info, err := os.Stat(templates)
	if err != nil {
		return Store{}, fmt.Errorf("UNREADABLE %s: %w", templates, err)
	}
	if !info.IsDir() {
		return Store{}, fmt.Errorf("UNREADABLE %s: not a directory", templates)
	}
	versionPath := filepath.Join(absolute, "VERSION")
	versionRaw, err := readStoreFile(versionPath)
	if err != nil {
		return Store{}, fmt.Errorf("UNREADABLE %s: %w", versionPath, err)
	}
	version := strings.TrimSpace(string(versionRaw))
	if version == "" {
		return Store{}, fmt.Errorf("UNREADABLE %s: empty version", versionPath)
	}
	sha, err := storeSHAWithRunner(absolute, runner)
	if err != nil {
		return Store{}, err
	}
	return Store{Root: absolute, Templates: templates, Version: version, SHA: sha, runner: runner}, nil
}

func storeSHA(root string) (string, error) {
	return storeSHAWithRunner(root, obs.Runner(deps.RealRunner{}))
}

func storeSHAWithRunner(root string, runner deps.Runner) (string, error) {
	if runner == nil {
		return "", errors.New("UNREADABLE blueprint git state: runner is nil")
	}
	gitDir, useFenceGit := pfmpaths.DevRepoGitDir(root)
	if !useFenceGit {
		gitPath := filepath.Join(root, ".git")
		if _, err := os.Stat(gitPath); errors.Is(err, fs.ErrNotExist) {
			return UnknownSelfHostedSHA, nil
		} else if err != nil {
			return "", fmt.Errorf("UNREADABLE %s: %w", gitPath, err)
		}
	}
	argv := []string{deps.Executable("git"), "rev-parse", "--short", "HEAD"}
	env := os.Environ()
	if useFenceGit {
		env = append(env, "GIT_DIR="+gitDir, "GIT_WORK_TREE="+root)
	}
	result, err := runner.Run(context.Background(), argv, deps.RunOptions{Dir: root, Env: env})
	if err != nil {
		return "", fmt.Errorf(
			"UNREADABLE blueprint git state %s: %w: %s",
			root,
			err,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf(
			"UNREADABLE blueprint git state %s: git exited with status %d: %s",
			root,
			result.ExitCode,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	sha := strings.TrimSpace(string(result.Stdout))
	if sha == "" {
		return "", fmt.Errorf("UNREADABLE blueprint git state %s: empty revision", root)
	}
	return sha, nil
}

func expandStorePath(value, projectRoot, home string) string {
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(value, "~/")))
	}
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(projectRoot, filepath.FromSlash(value))
}

func readStoreFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o444 == 0 {
		return nil, &os.PathError{Op: "read", Path: path, Err: fs.ErrPermission}
	}
	return os.ReadFile(path)
}

func adoptGit(runner deps.Runner, root string, args ...string) (string, string, error) {
	if runner == nil {
		return "", "", errors.New("adopt Git: runner is nil")
	}
	result, err := runner.Run(
		context.Background(),
		append([]string{deps.Executable("git")}, args...),
		deps.RunOptions{Dir: root},
	)
	if err != nil {
		return string(result.Stdout), string(result.Stderr), err
	}
	if result.ExitCode != 0 {
		return string(result.Stdout), string(result.Stderr), gitExitError{code: result.ExitCode}
	}
	return string(result.Stdout), string(result.Stderr), nil
}

type gitExitError struct{ code int }

func (err gitExitError) Error() string { return fmt.Sprintf("git exited with status %d", err.code) }
func (err gitExitError) ExitCode() int { return err.code }

func adoptGitFailure(action string, err error, stderrText string) error {
	return fmt.Errorf("%s: %w: %s", action, err, strings.TrimSpace(stderrText))
}

func adoptGitShowTemplate(runner deps.Runner, root, ref, template string) ([]byte, bool, error) {
	stdout, stderrText, gitErr := adoptGit(runner, root, "show", ref+":templates/"+template)
	if gitErr == nil {
		return []byte(stdout), false, nil
	}
	trimmed := strings.TrimSpace(stderrText)
	var exitErr interface{ ExitCode() int }
	if errors.As(gitErr, &exitErr) && exitErr.ExitCode() == 128 &&
		(strings.Contains(trimmed, "does not exist in") || strings.Contains(trimmed, "exists on disk, but not in")) {
		return nil, true, nil
	}
	return nil, false, adoptGitFailure(fmt.Sprintf("show %s at %s", template, ref), gitErr, stderrText)
}
