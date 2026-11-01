package doctor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// pfmPathWarnings checks both precedence and byte identity. A copied binary
// later on PATH can become the next active binary after a shell/toolchain
// change, so checking command resolution alone is insufficient.
func pfmPathWarnings(home, pathEnvironment string) []string {
	return pfmPathWarningsWithEnv(home, pathEnvironment, paths.OSEnv{})
}

func pfmPathWarningsWithEnv(home, pathEnvironment string, env paths.Env) []string {
	canonical := filepath.Join(home, ".local", "bin", "pfm")
	canonical, _ = filepath.Abs(canonical)
	targetHome, _ := filepath.Abs(home)
	jailed := env.Get(paths.EnvHome) != "" || env.Get("PFM_DEV_FENCE") != ""
	// A canonical binary that cannot be hashed leaves only the byte comparison
	// unknown: which pfm PATH resolves first is still reported.
	var warnings []string
	canonicalHash, canonicalErr := executableHash(canonical)
	if canonicalErr != nil {
		warnings = append(warnings, fmt.Sprintf("pfm_canonical=%s error=%v", canonical, canonicalErr))
	}

	seen := make(map[string]bool)
	candidates := make([]string, 0)
	for _, directory := range filepath.SplitList(pathEnvironment) {
		if directory == "" {
			directory = "."
		}
		candidate, err := filepath.Abs(filepath.Join(directory, "pfm"))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("pfm_path_entry=%s error=%v", directory, err))
			continue
		}
		candidate = filepath.Clean(candidate)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if jailed {
			relative, err := filepath.Rel(targetHome, candidate)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				continue
			}
		}
		info, err := os.Stat(candidate)
		if err != nil {
			if !os.IsNotExist(err) {
				warnings = append(warnings, fmt.Sprintf("pfm_path_entry=%s error=%v", candidate, err))
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		warnings = append(warnings, "pfm_path_resolves=not-found canonical="+canonical)
		return warnings
	}
	if candidates[0] != canonical {
		warnings = append(warnings, fmt.Sprintf(
			"pfm_path_resolves=%s canonical=%s",
			candidates[0],
			canonical,
		))
	}
	if canonicalErr != nil {
		return warnings
	}
	for _, candidate := range candidates {
		hash, err := executableHash(candidate)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("pfm_hash_read=%s error=%v", candidate, err))
			continue
		}
		if hash != canonicalHash {
			warnings = append(warnings, fmt.Sprintf(
				"pfm_hash_mismatch=%s canonical=%s",
				candidate,
				canonical,
			))
		}
	}
	return warnings
}

func executableHash(path string) ([sha256.Size]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(content), nil
}
