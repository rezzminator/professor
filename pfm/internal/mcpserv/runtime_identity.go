package mcpserv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type chatRuntimeIdentityDocument struct {
	Version        int                 `json:"version"`
	Paths          paths.Values        `json:"paths"`
	Accounts       []pfmconfig.Account `json:"accounts"`
	ConfigPath     string              `json:"configPath"`
	ClaudeBinary   string              `json:"claudeBinary"`
	CodexBinary    string              `json:"codexBinary"`
	OpenCodeBinary string              `json:"openCodeBinary"`
}

func deriveChatRuntimeIdentity(runtime Runtime) (string, error) {
	document := chatRuntimeIdentityDocument{
		Version: 1, Paths: runtime.Paths, Accounts: runtime.Accounts, ConfigPath: runtime.ConfigPath,
		ClaudeBinary: runtime.ClaudeBinary, CodexBinary: runtime.CodexBinary, OpenCodeBinary: runtime.OpenCodeBinary,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode chat runtime identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// RuntimeIdentity returns the immutable opaque identity of the selected fleet runtime.
func (service *Service) RuntimeIdentity() string { return service.backend.runtimeIdentity }
