//go:build e2e

package e2e

import (
	"errors"
	"sort"
	"testing"
)

func snapshotDifferences(left, right surfaceSnapshot) ([]string, error) {
	if len(left) == 0 && len(right) == 0 {
		return nil, errors.New("both snapshots empty — nothing was compared")
	}
	seen := make(map[string]bool, len(left)+len(right))
	for key := range left {
		seen[key] = true
	}
	for key := range right {
		seen[key] = true
	}
	var differences []string
	for key := range seen {
		if left[key] != right[key] {
			differences = append(differences, key)
		}
	}
	sort.Strings(differences)
	return differences, nil
}

func TestSnapshotDifferencesRejectsEmptySnapshots(t *testing.T) {
	_, err := snapshotDifferences(nil, nil)
	if err == nil || err.Error() != "both snapshots empty — nothing was compared" {
		t.Fatalf("empty snapshot error = %v, want named refusal", err)
	}
}
