package installer

import (
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/action"
)

// skillGitUsesSSH checks only remote operations, whose registry URL follows
// --. Local inspection and non-SSH transports do not consult SSH overrides.
func skillGitUsesSSH(args []string) bool {
	remoteOperation := false
	for index, arg := range args {
		if arg == "ls-remote" || arg == "clone" {
			remoteOperation = true
		}
		if arg == "--" && remoteOperation && index+1 < len(args) {
			remote := args[index+1]
			return strings.HasPrefix(remote, "ssh://") ||
				(!strings.Contains(remote, "://") && strings.Contains(remote, ":") && !filepath.IsAbs(remote))
		}
	}
	return remoteOperation // An unspecified transport cannot be proven non-SSH.
}

func skillGitRemoteOperation(args []string) bool {
	for _, arg := range args {
		if arg == "clone" || arg == "ls-remote" {
			return true
		}
	}
	return false
}

func skillSSHCommandNoninteractive(command string) bool {
	words, err := action.SplitShellWords(command)
	if err != nil || len(words) == 0 || filepath.Base(words[0]) != "ssh" ||
		strings.ContainsAny(command, "$`;|&<>()\n") {
		return false
	}
	for index := 1; index < len(words); index++ {
		word := words[index]
		switch {
		case word == "-o":
			index++
			if index >= len(words) {
				return false
			}
			word = words[index]
		case strings.HasPrefix(word, "-o"):
			word = strings.TrimPrefix(word, "-o")
		default:
			// Consume operands of options that take a separate argument.
			if len(word) == 2 && strings.IndexByte("BbcDEeFIiJLlmOpQRSWw", word[1]) >= 0 {
				index++
				if index >= len(words) {
					return false
				}
			} else if len(word) != 2 || !strings.HasPrefix(word, "-") || strings.IndexByte("46AaCfgKkMNnqsTtVXxYy", word[1]) < 0 {
				return false
			}
			continue
		}
		key, value, present := strings.Cut(word, "=")
		if strings.EqualFold(key, "BatchMode") {
			return present && strings.EqualFold(value, "yes")
		}
	}
	return false
}
