package installer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizedMemoryHelperFingerprintPinsHistoricalTemplatesAndRejectsShellSyntax(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		newName  string
		wantHash string
	}{
		{"wire", "memory-wire.sh", "ed3d1028ec8299e84d42c2fcd2e9797e64b6213116631cf1d9534df27c36c0a6"},
		{"consolidate", "memory-consolidate.sh", "d7d1672964f35cb2cd5a41a1ed38d5d3f5a2f1a37a4ba530d9e75bc48e6538a7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := oldMemoryHelperFixture(t, test.newName, "historical-invented-vault")
			got, err := normalizedMemoryHelperFingerprint(content)
			if err != nil || got != test.wantHash {
				t.Fatalf("fingerprint = %q, %v, want %q", got, err, test.wantHash)
			}
		})
	}

	template := oldMemoryHelperFixture(t, "memory-wire.sh", "{MEMORY_VAULT_DIR}")
	for _, value := range []string{"", "vault`command`", `vault\\escape`, `$HOME`, `"quoted"`, `{nested}`} {
		t.Run(fmt.Sprintf("reject_%q", value), func(t *testing.T) {
			content := bytes.Replace(template, []byte("{MEMORY_VAULT_DIR}"), []byte(value), 1)
			if _, err := normalizedMemoryHelperFingerprint(content); err == nil {
				t.Fatalf("accepted unsafe REPO substitution %q", value)
			}
		})
	}
}

func oldMemoryHelperFixture(t *testing.T, newName, vault string) []byte {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", ".."))
	content, err := os.ReadFile(filepath.Join(repoRoot, "templates", "project", "scripts", newName))
	if err != nil {
		t.Fatal(err)
	}
	switch newName {
	case "memory-wire.sh":
		content = bytes.Replace(content, []byte("# memory-wire.sh —"), []byte("# cc-memory-wire.sh —"), 1)
	case "memory-consolidate.sh":
		content = bytes.Replace(content, []byte("# memory-consolidate.sh —"), []byte("# cc-memory-consolidate.sh —"), 1)
		content = bytes.Replace(
			content,
			[]byte("SessionStart hook (memory-wire.sh)"),
			[]byte("SessionStart hook (cc-memory-wire.sh)"),
			1,
		)
	default:
		t.Fatalf("unknown memory helper template %q", newName)
	}
	if count := bytes.Count(content, []byte("{MEMORY_VAULT_DIR}")); count != 1 {
		t.Fatalf("template %s placeholder count = %d, want 1", newName, count)
	}
	return bytes.Replace(content, []byte("{MEMORY_VAULT_DIR}"), []byte(vault), 1)
}

func snapshotMemoryMigrationTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot[relative] = fmt.Sprintf("symlink:%o:%s", info.Mode().Perm(), target)
		case info.IsDir():
			snapshot[relative] = fmt.Sprintf("dir:%o", info.Mode().Perm())
		default:
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[relative] = fmt.Sprintf("file:%o:%x", info.Mode().Perm(), content)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
