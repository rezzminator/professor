package professor

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/deps"
	pfmpaths "hostops/pfm/internal/paths"
)

const UnknownSelfHostedSHA = "self-hosted@unknown"

type Store struct {
	Root      string
	Templates string
	Version   string
	SHA       string
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
	return InspectStore(blueprintRoot)
}

func InspectStore(root string) (Store, error) {
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
	sha, err := storeSHA(absolute)
	if err != nil {
		return Store{}, err
	}
	return Store{Root: absolute, Templates: templates, Version: version, SHA: sha}, nil
}

func storeSHA(root string) (string, error) {
	gitDir, useFenceGit := pfmpaths.DevRepoGitDir(root)
	if !useFenceGit {
		gitPath := filepath.Join(root, ".git")
		if _, err := os.Stat(gitPath); errors.Is(err, fs.ErrNotExist) {
			return UnknownSelfHostedSHA, nil
		} else if err != nil {
			return "", fmt.Errorf("UNREADABLE %s: %w", gitPath, err)
		}
	}
	command := exec.Command(deps.Executable("git"), "rev-parse", "--short", "HEAD")
	command.Dir = root
	if useFenceGit {
		command.Env = append(os.Environ(), "GIT_DIR="+gitDir, "GIT_WORK_TREE="+root)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("UNREADABLE blueprint git state %s: %w: %s", root, err, strings.TrimSpace(string(output)))
	}
	sha := strings.TrimSpace(string(output))
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
