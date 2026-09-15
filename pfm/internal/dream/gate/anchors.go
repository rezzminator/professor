package gate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/dream/artifact"
)

type MapInput struct {
	Name string
	Text string
}

type GitObject struct {
	Hash string
	Type artifact.GitObjectType
}

// GitObjectReader is the anchor gate's only Git boundary. Tree is the
// recorded preflight commit/tree, never an implicit live HEAD.
type GitObjectReader interface {
	Resolve(tree, path string) (object GitObject, found bool, err error)
}

type AnchorRejection struct {
	MapPath string
	Reason  string
}

type AnchorResult struct {
	Accepted []string
	Rejected []AnchorRejection
}

// Anchors validates all supplied maps against one recorded repository tree.
// Bad maps are rejected individually; a Git check that cannot run aborts the
// whole gate because an unavailable mechanism is not evidence of absence.
func Anchors(recordedTree string, maps []MapInput, git GitObjectReader) (AnchorResult, error) {
	if recordedTree == "" {
		return AnchorResult{}, errors.New("anchor gate requires a recorded tree")
	}
	if git == nil {
		return AnchorResult{}, errors.New("anchor gate requires a Git object reader")
	}
	inputs := append([]MapInput(nil), maps...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
	for index := 1; index < len(inputs); index++ {
		if inputs[index-1].Name == inputs[index].Name {
			return AnchorResult{}, fmt.Errorf("duplicate staged map name: %s", inputs[index].Name)
		}
	}

	result := AnchorResult{}
	for _, input := range inputs {
		mapPath := "maps/" + input.Name
		if !artifact.ValidMapFilename(input.Name) {
			result.Rejected = append(result.Rejected, AnchorRejection{MapPath: mapPath, Reason: "invalid map filename"})
			continue
		}
		parsed, err := artifact.ParseMap(input.Text)
		if err != nil {
			result.Rejected = append(result.Rejected, AnchorRejection{MapPath: mapPath, Reason: err.Error()})
			continue
		}
		reason := ""
		for _, anchor := range parsed.Anchors {
			object, found, err := git.Resolve(recordedTree, anchor.LookupPath)
			if err != nil {
				return AnchorResult{}, fmt.Errorf(
					"verify anchor %s at recorded tree %s: %w",
					anchor.LookupPath,
					recordedTree,
					err,
				)
			}
			if !found {
				reason = "anchor path absent at recorded tree: " + anchor.LookupPath
				break
			}
			if !strings.HasPrefix(object.Hash, anchor.Hash) {
				reason = fmt.Sprintf(
					"anchor hash mismatch: %s expected=%s actual=%s",
					anchor.LookupPath,
					anchor.Hash,
					object.Hash,
				)
				break
			}
			if object.Type != anchor.ObjectType {
				reason = fmt.Sprintf(
					"anchor object type mismatch: %s expected=%s actual=%s",
					anchor.LookupPath,
					anchor.ObjectType,
					object.Type,
				)
				break
			}
		}
		if reason != "" {
			result.Rejected = append(result.Rejected, AnchorRejection{MapPath: mapPath, Reason: reason})
			continue
		}
		result.Accepted = append(result.Accepted, mapPath)
	}
	return result, nil
}

// CommandGitReader performs only the two read-only operations needed by the
// anchor gate. GIT_OPTIONAL_LOCKS=0 prevents incidental Git lock writes.
type CommandGitReader struct {
	Repo string
}

func (reader CommandGitReader) Resolve(tree, path string) (GitObject, bool, error) {
	if reader.Repo == "" {
		return GitObject{}, false, errors.New("Git reader requires a repository")
	}
	resolve := exec.Command(deps.Executable("git"), "-C", reader.Repo, "rev-parse", "--verify", "-q", tree+":"+path)
	resolve.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := resolve.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return GitObject{}, false, nil
		}
		return GitObject{}, false, fmt.Errorf("git rev-parse: %w", err)
	}
	hash := strings.TrimSpace(string(output))
	if hash == "" {
		return GitObject{}, false, errors.New("git rev-parse returned an empty object id")
	}
	typeCommand := exec.Command(deps.Executable("git"), "-C", reader.Repo, "cat-file", "-t", hash)
	typeCommand.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	typeOutput, err := typeCommand.Output()
	if err != nil {
		return GitObject{}, false, fmt.Errorf("git cat-file: %w", err)
	}
	objectType := artifact.GitObjectType(strings.TrimSpace(string(typeOutput)))
	if objectType != artifact.GitBlob && objectType != artifact.GitTree {
		return GitObject{}, false, fmt.Errorf("git cat-file returned unsupported object type %q", objectType)
	}
	return GitObject{Hash: hash, Type: objectType}, true, nil
}
