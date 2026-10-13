package professor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// BaselineVersion is the version of a baseline that carries no decision
// records; one holding a keep (`kept`) or an accepted retired-name rule is
// written as BaselineDecisionsVersion, so an older pfm — a rolled-back binary,
// a teammate's machine — refuses the file instead of rewriting it without them.
const (
	BaselineVersion          = 1
	BaselineDecisionsVersion = 2
)

// versionFor is the version a baseline is written at.
func versionFor(baseline Baseline) int {
	if len(baseline.RetiredAccepted) != 0 {
		return BaselineDecisionsVersion
	}
	for _, pin := range baseline.Files {
		if pin.Kept != nil {
			return BaselineDecisionsVersion
		}
	}
	return BaselineVersion
}

type Baseline struct {
	Version   int                `json:"version"`
	Blueprint BlueprintPin       `json:"blueprint"`
	Files     map[string]FilePin `json:"files"`
	Ignored   []string           `json:"ignored,omitempty"`
	// RetiredAccepted are the recorded decisions that a retired name's mention
	// stays: `pfm update accept` writes them, the report counts the hits they
	// cover as accepted, never as review.
	RetiredAccepted []RetiredRule `json:"retiredAccepted,omitempty"`
}

type BlueprintPin struct {
	Version string `json:"version"`
	SHA     string `json:"sha"`
}

type FilePin struct {
	Template     string `json:"template"`
	TemplateHash string `json:"templateHash"`
	PinnedSHA    string `json:"pinnedSha"`
	PinnedAt     string `json:"pinnedAt"`
	// Kept is the recorded decision that this file deliberately stays apart
	// from the upstream change the pin now acknowledges (`pfm update pin
	// --keep`); nil for a pin whose change was ported.
	Kept *KeptNote `json:"kept,omitempty"`
}

// KeptNote records what a session chose not to take from upstream: Reason is
// its words, FromSHA the blueprint commit before the first change left
// untaken (the deferred change is `git diff FromSHA <pinnedSha>`), At the date.
type KeptNote struct {
	Reason  string `json:"reason"`
	FromSHA string `json:"fromSha"`
	At      string `json:"at"`
}

// RetiredRule accepts every retired-name hit at Path (a file, or a directory
// and everything under it) and, when Name is set, only that retired name.
type RetiredRule struct {
	Path   string `json:"path"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

// Covers reports whether the rule accepts a hit of name at path.
func (rule RetiredRule) Covers(path, name string) bool {
	if rule.Name != "" && rule.Name != name {
		return false
	}
	return path == rule.Path || strings.HasPrefix(path, rule.Path+"/")
}

// PinSummary spells WHICH pins a baseline holds and when they were stamped —
// the evidence `pfm init`'s refusal shows for "this project is already
// scaffolded". Files is a map, so ranging it for "the" date reported an
// arbitrary entry's, which is well-defined only while every pin shares one
// stamp; `pfm update pin` (one file, today's date) breaks that. The newest
// stamp is the answer, and pins that disagree say so rather than picking a
// winner silently. A baseline that pins NOTHING is its own state, never a
// date that could not be found: it is the degenerate shape a second init
// would leave behind, and the refusal exists to preserve it.
func (baseline Baseline) PinSummary() string {
	if len(baseline.Files) == 0 {
		return "pinning no files at all"
	}
	newest := ""
	spread := false
	for _, pin := range baseline.Files {
		switch {
		case newest == "":
			newest = pin.PinnedAt
		case pin.PinnedAt != newest:
			spread = true
			if pin.PinnedAt > newest {
				newest = pin.PinnedAt
			}
		}
	}
	if spread {
		return fmt.Sprintf("%d file(s) pinned, the newest on %s — pins span several dates", len(baseline.Files), newest)
	}
	return fmt.Sprintf("%d file(s) pinned by pfm init on %s", len(baseline.Files), newest)
}

func BaselinePath(root string) string {
	return filepath.Join(root, ".professor", "baseline.json")
}

func Load(root string) (Baseline, error) {
	path := BaselinePath(root)
	raw, err := readStoreFile(path)
	if err != nil {
		return Baseline{}, fmt.Errorf("UNREADABLE %s: %w", path, err)
	}
	var baseline Baseline
	if err := json.Unmarshal(raw, &baseline); err != nil {
		return Baseline{}, fmt.Errorf("BASELINE-MALFORMED %s: %w", path, err)
	}
	if baseline.Version != BaselineVersion && baseline.Version != BaselineDecisionsVersion {
		return Baseline{}, fmt.Errorf("BASELINE-VERSION %d: unsupported", baseline.Version)
	}
	if baseline.Files == nil {
		baseline.Files = make(map[string]FilePin)
	}
	baseline.Ignored = normalizeIgnored(baseline.Ignored)
	baseline.RetiredAccepted = normalizeRetiredRules(baseline.RetiredAccepted)
	return baseline, nil
}

// normalizeIgnored returns a sorted, duplicate-free copy of an Ignored list
// (nil for an empty result, so json:",omitempty" drops it cleanly). Each
// value is trimmed first and empties are dropped, so a hand-edited baseline
// with stray whitespace or a blank entry never survives a round-trip.
func normalizeIgnored(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		trimmed = append(trimmed, value)
	}
	if len(trimmed) == 0 {
		return nil
	}
	sort.Strings(trimmed)
	deduped := make([]string, 0, len(trimmed))
	for index, value := range trimmed {
		if index == 0 || value != trimmed[index-1] {
			deduped = append(deduped, value)
		}
	}
	return deduped
}

// normalizeRetiredRules sorts the rules by path then name, one rule per
// path+name (the later reason wins), nil for none.
func normalizeRetiredRules(rules []RetiredRule) []RetiredRule {
	byKey := make(map[[2]string]RetiredRule, len(rules))
	for _, rule := range rules {
		byKey[[2]string{rule.Path, rule.Name}] = rule
	}
	if len(byKey) == 0 {
		return nil
	}
	result := make([]RetiredRule, 0, len(byKey))
	for _, rule := range byKey {
		result = append(result, rule)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func Save(root string, baseline Baseline) error {
	if baseline.Version != BaselineVersion && baseline.Version != BaselineDecisionsVersion {
		return fmt.Errorf("BASELINE-VERSION %d: unsupported", baseline.Version)
	}
	if baseline.Files == nil {
		baseline.Files = make(map[string]FilePin)
	}
	baseline.Ignored = normalizeIgnored(baseline.Ignored)
	baseline.RetiredAccepted = normalizeRetiredRules(baseline.RetiredAccepted)
	baseline.Version = versionFor(baseline)
	raw, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}
	raw = append(raw, '\n')
	path := BaselinePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create baseline directory %s: %w", filepath.Dir(path), err)
	}
	return atomicfile.Write(path, raw, 0o644)
}
