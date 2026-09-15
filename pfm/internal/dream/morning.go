package dream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hostops/pfm/internal/dream/lane"
	"hostops/pfm/internal/dream/organ"
	"hostops/pfm/internal/dream/resources"
)

type MorningRequest struct {
	RegistryBase     string
	ResourcesRoot    string
	RepositoriesFile string
}

type MorningRun struct {
	RepoRoot  string
	AgentType string
	Outcome   MorningOutcome
	Result    NightResult
	Err       error
}

type MorningOutcome string

const (
	MorningNightCompleted  MorningOutcome = "night-completed"
	MorningNightFailed     MorningOutcome = "night-failed"
	MorningOrganUnresolved MorningOutcome = "organ-unresolved"
)

type MorningRepositoryRun struct {
	RepoRoot  string
	AgentType string
	Outcome   MorningOutcome
	Reason    string
}

type MorningResult struct {
	Runs         []MorningRun
	Repositories []MorningRepositoryRun
	Stdout       string
	Stderr       string
	Failed       bool
}

type morningDependencies struct {
	night             func(context.Context, NightRequest, NightDependencies) (NightResult, error)
	nightDependencies NightDependencies
	probeOrgan        func(string, string) error
}

// Morning is the production sequential launcher used by the CLI.
func Morning(ctx context.Context, request MorningRequest) (MorningResult, error) {
	return morningWith(ctx, request, morningDependencies{
		night:             Night,
		nightDependencies: DefaultNightDependencies(),
		probeOrgan:        morningOrganResolvable,
	})
}

// Morning runs every configured repository and organ-local lane sequentially.
// An individual failed night is recorded and does not hide later repositories;
// malformed launcher configuration fails the whole operation immediately.
func morningWith(
	ctx context.Context,
	request MorningRequest,
	dependencies morningDependencies,
) (MorningResult, error) {
	if ctx == nil {
		return MorningResult{}, errors.New("dream morning requires a context")
	}
	if err := validateResourcesRoot(request.ResourcesRoot); err != nil {
		return MorningResult{}, err
	}
	if request.RegistryBase == "" || !filepath.IsAbs(request.RegistryBase) ||
		filepath.Clean(request.RegistryBase) != request.RegistryBase {
		return MorningResult{}, fmt.Errorf(
			"dream registry base must be absolute and canonical: %s",
			request.RegistryBase,
		)
	}
	if request.RepositoriesFile == "" || !filepath.IsAbs(request.RepositoriesFile) ||
		filepath.Clean(request.RepositoriesFile) != request.RepositoriesFile {
		return MorningResult{}, fmt.Errorf(
			"dream repository list path must be absolute and canonical: %s",
			request.RepositoriesFile,
		)
	}
	if dependencies.night == nil {
		return MorningResult{}, errors.New("dream morning requires a night runner")
	}
	if dependencies.probeOrgan == nil {
		return MorningResult{}, errors.New("dream morning requires an organ probe")
	}
	if err := validateNightDependencies(dependencies.nightDependencies); err != nil {
		return MorningResult{}, err
	}
	repositories, err := readMorningRepositories(request.RepositoriesFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return MorningResult{}, fmt.Errorf(
				"dream repository list is missing at %s; create it with one line per repository: /absolute/repo [agent-type] # optional comment: %w",
				request.RepositoriesFile,
				err,
			)
		}
		return MorningResult{}, err
	}
	if len(repositories) == 0 {
		return MorningResult{}, errors.New("dreamer-morning: repository list is empty")
	}

	var result MorningResult
	var stdout, stderr strings.Builder
	recordFailure := func(repoRoot, agentType string, outcome MorningOutcome, runErr error) MorningRun {
		fmt.Fprintf(&stdout, "dreamer-morning: BEGIN repo=%s agent=%s\n", repoRoot, agentType)
		result.Failed = true
		fmt.Fprintf(&stderr, "dreamer-night: FAIL: %s\n", oneLine(runErr.Error()))
		fmt.Fprintf(&stderr, "dreamer-morning: FAIL repo=%s agent=%s rc=1\n", repoRoot, agentType)
		fmt.Fprintf(&stdout, "dreamer-morning: END repo=%s agent=%s\n", repoRoot, agentType)
		run := MorningRun{RepoRoot: repoRoot, AgentType: agentType, Outcome: outcome, Err: runErr}
		result.Runs = append(result.Runs, run)
		return run
	}
	runLane := func(repoRoot, agentType string) (MorningRun, error) {
		if err := ctx.Err(); err != nil {
			return MorningRun{}, err
		}
		fmt.Fprintf(&stdout, "dreamer-morning: BEGIN repo=%s agent=%s\n", repoRoot, agentType)
		var nightOut, nightErr bytes.Buffer
		runDependencies := dependencies.nightDependencies
		runDependencies.Stdout = &nightOut
		runDependencies.Stderr = &nightErr
		startedAt := dependencies.nightDependencies.Clock()
		nightResult, runErr := dependencies.night(ctx, NightRequest{
			RepoRoot:      repoRoot,
			RegistryBase:  request.RegistryBase,
			ResourcesRoot: request.ResourcesRoot,
			AgentType:     agentType,
			StartedAt:     startedAt,
		}, runDependencies)
		stdout.WriteString(nightOut.String())
		stderr.WriteString(nightErr.String())
		outcome := MorningNightCompleted
		if runErr == nil {
			fmt.Fprintf(&stdout, "dreamer-morning: PASS repo=%s agent=%s\n", repoRoot, agentType)
		} else {
			outcome = MorningNightFailed
			result.Failed = true
			fmt.Fprintf(&stderr, "dreamer-night: FAIL: %s\n", oneLine(runErr.Error()))
			fmt.Fprintf(&stderr, "dreamer-morning: FAIL repo=%s agent=%s rc=1\n", repoRoot, agentType)
		}
		fmt.Fprintf(&stdout, "dreamer-morning: END repo=%s agent=%s\n", repoRoot, agentType)
		run := MorningRun{
			RepoRoot: repoRoot, AgentType: agentType, Outcome: outcome, Result: nightResult, Err: runErr,
		}
		result.Runs = append(result.Runs, run)
		return run, nil
	}

	for _, configured := range repositories {
		repositoryRun := MorningRepositoryRun{
			RepoRoot: configured.RepoRoot, AgentType: configured.AgentType, Outcome: MorningNightCompleted,
		}
		if probeErr := dependencies.probeOrgan(configured.RepoRoot, request.RegistryBase); probeErr != nil {
			recordFailure(configured.RepoRoot, configured.AgentType, MorningOrganUnresolved, probeErr)
			repositoryRun.Outcome = MorningOrganUnresolved
			repositoryRun.Reason = oneLine(probeErr.Error())
			result.Repositories = append(result.Repositories, repositoryRun)
			continue
		}
		configuredRun, err := runLane(configured.RepoRoot, configured.AgentType)
		if err != nil {
			return result, err
		}
		if configuredRun.Outcome != MorningNightCompleted {
			repositoryRun.Outcome = MorningNightFailed
			repositoryRun.Reason = oneLine(configuredRun.Err.Error())
		}
		configuredLane, err := lane.FromAgentTypeIn(
			configured.AgentType,
			resources.NewResources(
				request.ResourcesRoot,
				filepath.Join(configured.RepoRoot, ".professor", "stm"),
			),
		)
		if err != nil {
			return result, fmt.Errorf("normalize configured morning lane for %s: %w", configured.RepoRoot, err)
		}
		profiles, err := discoverMorningLanes(configured.RepoRoot)
		if err != nil {
			result.Failed = true
			repositoryRun.Outcome = MorningNightFailed
			repositoryRun.Reason = oneLine(err.Error())
			fmt.Fprintf(
				&stderr,
				"dreamer-morning: FAIL repo=%s lane-discovery: %s\n",
				configured.RepoRoot,
				oneLine(err.Error()),
			)
			result.Repositories = append(result.Repositories, repositoryRun)
			continue
		}
		for _, profileLane := range profiles {
			// The default agent type Explore resolves onto this lane; skipping it
			// keeps the default night from running twice.
			if profileLane == configuredLane {
				continue
			}
			laneRun, err := runLane(configured.RepoRoot, profileLane)
			if err != nil {
				return result, err
			}
			if laneRun.Outcome != MorningNightCompleted {
				repositoryRun.Outcome = MorningNightFailed
				repositoryRun.Reason = oneLine(laneRun.Err.Error())
			}
		}
		result.Repositories = append(result.Repositories, repositoryRun)
	}
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	return result, nil
}

func morningOrganResolvable(repoRoot, registryBase string) error {
	repository, err := organ.Resolve(repoRoot, registryBase)
	if err != nil {
		return fmt.Errorf("organ unresolved for listed repository %s: %w", repoRoot, err)
	}
	info, err := os.Lstat(repository.Organ)
	if err != nil {
		return fmt.Errorf("organ unresolved for listed repository %s at %s: %w", repoRoot, repository.Organ, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf(
			"organ unresolved for listed repository %s at %s: not a real directory",
			repoRoot,
			repository.Organ,
		)
	}
	return nil
}

// defaultAgentType is the agent every repository runs without an explicit
// entry; defaultAgentLane is the lane it resolves onto.
const (
	defaultAgentType = "Explore"
	defaultAgentLane = "tracer"
)

type morningRepository struct {
	RepoRoot  string
	AgentType string
}

func readMorningRepositories(path string) ([]morningRepository, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect dream repository list %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("dream repository list is not a regular non-symlink file: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dream repository list %s: %w", path, err)
	}
	var result []morningRepository
	for lineNumber, rawLine := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line := rawLine
		if index := strings.IndexByte(line, '#'); index >= 0 {
			line = line[:index]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) > 2 {
			return nil, fmt.Errorf("dream repository list line %d has more than repo and agent", lineNumber+1)
		}
		repoRoot := fields[0]
		if !filepath.IsAbs(repoRoot) || filepath.Clean(repoRoot) != repoRoot {
			return nil, fmt.Errorf("dream repository list line %d has noncanonical root %s", lineNumber+1, repoRoot)
		}
		agentType := defaultAgentType
		if len(fields) == 2 {
			agentType = fields[1]
		}
		if _, err := lane.FromAgentType(agentType); err != nil {
			return nil, fmt.Errorf("dream repository list line %d has invalid agent: %w", lineNumber+1, err)
		}
		result = append(result, morningRepository{RepoRoot: repoRoot, AgentType: agentType})
	}
	return result, nil
}

func discoverMorningLanes(repoRoot string) ([]string, error) {
	root := filepath.Join(repoRoot, ".professor", "stm", "lanes")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read organ lane profiles %s: %w", root, err)
	}
	var result []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect lane profile %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("lane profile is not a regular non-symlink file: %s", path)
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		normalized, err := lane.FromAgentType(name)
		if err != nil || normalized != name {
			return nil, fmt.Errorf("organ lane profile has noncanonical name: %s", entry.Name())
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}
