package installer

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
)

const (
	memoryRepoTemplateLine = `REPO="${CLAUDE_MEMORY_REPO:-$HOME/work/{MEMORY_VAULT_DIR}}"`
	memoryRepoLinePrefix   = `REPO="${CLAUDE_MEMORY_REPO:-$HOME/work/`
	memoryRepoLineSuffix   = `}"`
)

var retiredMemoryHelpers = []struct {
	oldName          string
	newName          string
	normalizedSHA256 string
}{
	{
		oldName:          "cc-memory-wire.sh",
		newName:          "memory-wire.sh",
		normalizedSHA256: "ed3d1028ec8299e84d42c2fcd2e9797e64b6213116631cf1d9534df27c36c0a6",
	},
	{
		oldName:          "cc-memory-consolidate.sh",
		newName:          "memory-consolidate.sh",
		normalizedSHA256: "d7d1672964f35cb2cd5a41a1ed38d5d3f5a2f1a37a4ba530d9e75bc48e6538a7",
	},
}

func normalizedMemoryHelperFingerprint(content []byte) (string, error) {
	lines := bytes.Split(content, []byte("\n"))
	repoLines := 0
	for index, rawLine := range lines {
		line := string(rawLine)
		if !strings.HasPrefix(line, "REPO=") {
			continue
		}
		repoLines++
		if !strings.HasPrefix(line, memoryRepoLinePrefix) || !strings.HasSuffix(line, memoryRepoLineSuffix) {
			return "", fmt.Errorf("REPO line is not a placeholder-only vault path substitution")
		}
		value := strings.TrimSuffix(strings.TrimPrefix(line, memoryRepoLinePrefix), memoryRepoLineSuffix)
		if value == "" || (value != "{MEMORY_VAULT_DIR}" && strings.ContainsAny(value, "\"${}`\\")) {
			return "", fmt.Errorf("REPO line is not a placeholder-only vault path substitution")
		}
		lines[index] = []byte(memoryRepoTemplateLine)
	}
	if repoLines != 1 {
		return "", fmt.Errorf("expected exactly one canonical REPO line, found %d", repoLines)
	}
	normalized := bytes.Join(lines, []byte("\n"))
	digest := sha256.Sum256(normalized)
	return fmt.Sprintf("%x", digest), nil
}
