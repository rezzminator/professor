// Package lane owns lane naming, profiles, membership, rendered map surfaces,
// and the best-effort drift annotation shown to spawned agents.
package lane

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"hostops/pfm/internal/dream/artifact"
	"hostops/pfm/internal/dream/resources"
)

var (
	lanePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	mapFilePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.md$`)
)

// Slug applies canonical ASCII byte normalization without agent aliases.
func Slug(agentType string) (string, error) {
	if agentType == "" || asciiWhitespaceOnly(agentType) {
		return "", fmt.Errorf("agent type must not be empty or ASCII-whitespace-only")
	}
	var slug strings.Builder
	slug.Grow(len(agentType))
	for index := 0; index < len(agentType); index++ {
		value := agentType[index]
		switch {
		case value >= 'A' && value <= 'Z':
			slug.WriteByte(value + ('a' - 'A'))
		case value >= 'a' && value <= 'z', value >= '0' && value <= '9', value == '-':
			slug.WriteByte(value)
		default:
			slug.WriteByte('-')
		}
	}
	result := slug.String()
	if !lanePattern.MatchString(result) {
		return "", fmt.Errorf("agent type does not yield a lane slug: %s", agentType)
	}
	return result, nil
}

// FromAgentType is the alias-free lane name for an agent type. Aliases are
// declared by the lanes themselves; resolve through FromAgentTypeIn wherever a
// repository's profiles are reachable.
func FromAgentType(agentType string) (string, error) {
	return Slug(agentType)
}

func asciiWhitespaceOnly(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case ' ', '\t', '\n', '\r', '\v', '\f':
		default:
			return false
		}
	}
	return true
}

// ResolveProfile applies the resource layer order to one lane profile.
func ResolveProfile(agentType, slug string, resourceSet resources.Resources) (artifact.LaneProfile, error) {
	if !lanePattern.MatchString(slug) {
		return artifact.LaneProfile{}, fmt.Errorf("invalid lane slug: %s", slug)
	}
	normalized, err := FromAgentTypeIn(agentType, resourceSet)
	if err != nil {
		return artifact.LaneProfile{}, err
	}
	if normalized != slug {
		return artifact.LaneProfile{}, fmt.Errorf(
			"agent type %s resolves to lane %s, not %s",
			agentType,
			normalized,
			slug,
		)
	}
	name := "lanes/" + slug + ".md"
	body, source, err := resourceSet.ReadFileWithSource(name)
	if err != nil {
		return artifact.LaneProfile{}, fmt.Errorf("lane has no profile: %w", err)
	}
	return artifact.LaneProfile{
		AgentType: agentType,
		Lane:      slug,
		Path:      source,
		Body:      string(body),
	}, nil
}

// MembershipRequest distinguishes a missing pre-lane ledger from a present
// but incomplete ledger. PreviousMaps is the old pool; FinalMaps is the pool
// that will remain after the apply.
type MembershipRequest struct {
	FinalMaps     []string
	PreviousMaps  []string
	Existing      artifact.LaneMembership
	LedgerPresent bool
	CurrentLane   string
}

// BuildMembership rebuilds the whole final-pool ledger deterministically.
func BuildMembership(request MembershipRequest) (artifact.LaneMembership, error) {
	if !lanePattern.MatchString(request.CurrentLane) {
		return nil, fmt.Errorf("invalid current lane: %s", request.CurrentLane)
	}
	if err := validateMembership(request.Existing); err != nil {
		return nil, err
	}
	if !request.LedgerPresent && len(request.Existing) != 0 {
		return nil, fmt.Errorf("lane membership supplied while ledger is absent")
	}
	previous, err := filenameSet(request.PreviousMaps)
	if err != nil {
		return nil, fmt.Errorf("previous maps: %w", err)
	}
	final, err := filenameSet(request.FinalMaps)
	if err != nil {
		return nil, fmt.Errorf("final maps: %w", err)
	}
	if request.LedgerPresent {
		for name := range previous {
			if _, exists := request.Existing[name]; !exists {
				return nil, fmt.Errorf("pre-existing map carries no lane row: %s", name)
			}
		}
	}

	result := make(artifact.LaneMembership, len(final))
	for name := range final {
		if _, old := previous[name]; old {
			if request.LedgerPresent {
				result[name] = request.Existing[name]
				continue
			}
			result[name] = "explorer"
			continue
		}
		result[name] = request.CurrentLane
	}
	return result, nil
}

func validateMembership(membership artifact.LaneMembership) error {
	for mapFile, mapLane := range membership {
		if !mapFilePattern.MatchString(mapFile) || !lanePattern.MatchString(mapLane) {
			return fmt.Errorf("invalid lane membership: %s -> %s", mapFile, mapLane)
		}
	}
	return nil
}

func filenameSet(values []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !mapFilePattern.MatchString(value) {
			return nil, fmt.Errorf("invalid map filename: %s", value)
		}
		if _, exists := result[value]; exists {
			return nil, fmt.Errorf("duplicate map filename: %s", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

// CachedTitles renders the deduplication surface shown to the distill seat.
// A map without a Question is not silently reduced to its bare title.
func CachedTitles(mapsDirectory string) ([]string, error) {
	files, err := mapFiles(mapsDirectory)
	if err != nil {
		return nil, err
	}
	titles := make(map[string]struct{}, len(files))
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return nil, artifact.ErrorAt(file, fmt.Errorf("read map %s: %w", file, err))
		}
		title, question, err := titleAndQuestion(string(body))
		if err != nil {
			return nil, artifact.ErrorAt(file, fmt.Errorf("dedup map %s: %w", file, err))
		}
		titles[title+" — "+question] = struct{}{}
	}
	result := make([]string, 0, len(titles))
	for title := range titles {
		result = append(result, title)
	}
	sort.Strings(result)
	return result, nil
}

func titleAndQuestion(body string) (string, string, error) {
	rows := splitLines(body)
	if len(rows) == 0 || !strings.HasPrefix(rows[0], "# ") {
		return "", "", fmt.Errorf("map lacks H1")
	}
	title := strings.TrimPrefix(rows[0], "# ")
	if err := validateTitle(title); err != nil {
		return "", "", err
	}
	question := -1
	answer := len(rows)
	for index, row := range rows {
		switch row {
		case "## Question":
			if question >= 0 {
				return "", "", fmt.Errorf("Question heading count is not one")
			}
			question = index
		case "## Answer":
			if answer != len(rows) {
				return "", "", fmt.Errorf("Answer heading count is not one")
			}
			answer = index
		}
	}
	if question < 0 {
		return "", "", fmt.Errorf("map lacks Question")
	}
	if answer <= question {
		return "", "", fmt.Errorf("Question section order mismatch")
	}
	for _, row := range rows[question+1 : answer] {
		candidate := strings.TrimSpace(strings.ReplaceAll(row, "\t", " "))
		if candidate != "" {
			return title, candidate, nil
		}
	}
	return "", "", fmt.Errorf("map Question is empty")
}

func mapFiles(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read maps directory %s: %w", directory, err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".md" || !entry.Type().IsRegular() {
			continue
		}
		if !mapFilePattern.MatchString(entry.Name()) {
			return nil, fmt.Errorf("invalid map filename: %s", entry.Name())
		}
		files = append(files, filepath.Join(directory, entry.Name()))
	}
	sort.Strings(files)
	return files, nil
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	rows := strings.Split(text, "\n")
	if rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

func validateTitle(title string) error {
	if title == "" || !utf8.ValidString(title) || strings.TrimSpace(title) != title {
		return fmt.Errorf("invalid map title")
	}
	if strings.Contains(title, " -> maps/") {
		return fmt.Errorf("unsafe map title")
	}
	for _, value := range []byte(title) {
		if value < 0x20 || value == 0x7f {
			return fmt.Errorf("unsafe map title")
		}
	}
	return nil
}

// servesPattern reads a lane profile's declaration of the agent types it serves:
//
//	Serves: tracer, Explore, wave-tracer
//
// The alias belongs to the lane, not to this package. A new agent joins an
// existing memory by being named in that lane's profile — no engine change.
var servesPattern = regexp.MustCompile(`(?m)^Serves:[ \t]*(.+?)[ \t]*$`)

// FromAgentTypeIn resolves an agent type to its lane using the lane profiles
// available to this repository in resource layer order. A type named in a
// profile's Serves list takes that lane; anything unnamed falls back to the
// canonical byte normalizer, so an agent with no profile still gets its own lane.
func FromAgentTypeIn(agentType string, resourceSet resources.Resources) (string, error) {
	if agentType == "" {
		return Slug(agentType)
	}
	entries, err := resourceSet.ReadDirByLayer("lanes")
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			names = append(names, entry.Name())
		}
	}
	for _, name := range names {
		body, err := resourceSet.ReadFile("lanes/" + name)
		if err != nil {
			return "", err
		}
		match := servesPattern.FindSubmatch(body)
		if match == nil {
			continue
		}
		for _, served := range strings.Split(string(match[1]), ",") {
			if strings.TrimSpace(served) != agentType {
				continue
			}
			slug := strings.TrimSuffix(name, ".md")
			if !lanePattern.MatchString(slug) {
				return "", fmt.Errorf("lane profile %s does not yield a lane slug", name)
			}
			return slug, nil
		}
	}
	return Slug(agentType)
}
