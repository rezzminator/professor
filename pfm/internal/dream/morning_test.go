package dream

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/dream/gate"
	"hostops/pfm/internal/dream/seat"
)

func TestMorningContinuesAfterFailureAndDiscoversLanesWithoutDuplicateExplorer(t *testing.T) {
	root := t.TempDir()
	resources := filepath.Join(root, "resources")
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, directory := range []string{
		resources,
		filepath.Join(first, ".professor", "stm", "lanes"),
		filepath.Join(second, ".professor", "stm", "lanes"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(resources, "repos.list"),
		[]byte(first+"\n"+second+" qa\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(first, ".professor", "stm", "lanes", "tracer.md"),
		[]byte("Serves: Explore\n\nprofile\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(first, ".professor", "stm", "lanes", "reviewer.md"),
		filepath.Join(second, ".professor", "stm", "lanes", "qa.md"),
		filepath.Join(second, ".professor", "stm", "lanes", "writer.md"),
	} {
		if err := os.WriteFile(path, []byte("profile\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var calls []string
	fakeNight := func(_ context.Context, request NightRequest, dependencies NightDependencies) (NightResult, error) {
		if dependencies.Stdout == nil || dependencies.Stderr == nil {
			t.Fatal("morning did not isolate per-night output")
		}
		calls = append(calls, request.RepoRoot+"\t"+request.AgentType)
		_, _ = io.WriteString(dependencies.Stdout, "night-output "+request.AgentType+"\n")
		if request.RepoRoot == first && request.AgentType == "Explore" {
			return NightResult{}, errors.New("first night failed")
		}
		return NightResult{}, nil
	}
	result, err := morningWith(context.Background(), MorningRequest{
		RegistryBase:     root,
		ResourcesRoot:    resources,
		RepositoriesFile: filepath.Join(resources, "repos.list"),
	}, morningDependencies{
		night: fakeNight,
		probeOrgan: func(string, string) error {
			return nil
		},
		nightDependencies: NightDependencies{
			SeatLaw:       seat.RequiredSeatLaw(),
			NewSeatRunner: func(seat.EventSink) (NightSeatRunner, error) { return nil, errors.New("unused") },
			Git:           func(string) NightGitReader { return fakeMorningGit{} },
			Clock:         func() time.Time { return time.Date(2026, 8, 13, 7, 0, 0, 0, time.UTC) },
			AcquireLock:   func(string) (func() error, error) { return func() error { return nil }, nil },
			Stdout:        io.Discard,
			Stderr:        io.Discard,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		first + "\tExplore",
		first + "\treviewer",
		second + "\tqa",
		second + "\twriter",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("morning calls = %#v, want %#v", calls, wantCalls)
	}
	if !result.Failed || len(result.Runs) != 4 || strings.Count(result.Stdout, "agent=Explore") != 2 {
		t.Fatalf("morning result = %+v", result)
	}
	if len(result.Repositories) != 2 || result.Repositories[0].Outcome != MorningNightFailed ||
		result.Repositories[1].Outcome != MorningNightCompleted {
		t.Fatalf("repository outcomes = %+v", result.Repositories)
	}
	if !strings.Contains(result.Stderr, "dreamer-night: FAIL: first night failed\n") ||
		!strings.Contains(result.Stderr, "FAIL repo="+first+" agent=Explore rc=1\n") ||
		!strings.Contains(result.Stdout, "PASS repo="+second+" agent=writer\n") {
		t.Fatalf("morning stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestMorningRepositoryListFailsClosed(t *testing.T) {
	root := t.TempDir()
	resources := filepath.Join(root, "resources")
	if err := os.Mkdir(resources, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "empty", body: "# only comments\n"},
		{name: "too many fields", body: "/repo qa extra\n"},
		{name: "relative", body: "repo\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(resources, "repos.list"), []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := morningWith(context.Background(), MorningRequest{
				RegistryBase:     root,
				ResourcesRoot:    resources,
				RepositoriesFile: filepath.Join(resources, "repos.list"),
			}, morningDependencies{
				night: func(context.Context, NightRequest, NightDependencies) (NightResult, error) {
					t.Fatal("night ran after malformed repository list")
					return NightResult{}, nil
				},
				probeOrgan:        func(string, string) error { return nil },
				nightDependencies: validMorningDependencies(),
			})
			if err == nil {
				t.Fatal("malformed repository list passed")
			}
		})
	}
}

func TestMorningMissingRepositoryListNamesPathAndFormat(t *testing.T) {
	root := t.TempDir()
	repositoriesFile := filepath.Join(root, "config", "pfm", "repos.list")
	_, err := morningWith(context.Background(), MorningRequest{
		RegistryBase:     root,
		RepositoriesFile: repositoriesFile,
	}, morningDependencies{
		night: func(context.Context, NightRequest, NightDependencies) (NightResult, error) {
			t.Fatal("night ran without a repository list")
			return NightResult{}, nil
		},
		probeOrgan:        func(string, string) error { return nil },
		nightDependencies: validMorningDependencies(),
	})
	if err == nil || !strings.Contains(err.Error(), repositoriesFile) ||
		!strings.Contains(err.Error(), "/absolute/repo [agent-type] # optional comment") {
		t.Fatalf("missing repository list error = %v", err)
	}
}

func TestMorningRecordsMissingListedOrganAndContinuesToLaterRepository(t *testing.T) {
	root := t.TempDir()
	resources := filepath.Join(root, "resources")
	missing := filepath.Join(root, "missing-organ")
	healthy := filepath.Join(root, "healthy-organ")
	if err := os.Mkdir(resources, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, repository := range []string{missing, healthy} {
		if err := os.Mkdir(repository, 0o700); err != nil {
			t.Fatal(err)
		}
		hookGit(t, repository, "init", "-q", "--initial-branch=main")
		hookGit(t, repository, "config", "user.name", "Morning Test")
		hookGit(t, repository, "config", "user.email", "morning@test.invalid")
		writeHookFile(t, filepath.Join(repository, "tracked.txt"), "fixture\n")
		hookGit(t, repository, "add", "tracked.txt")
		hookGit(t, repository, "commit", "-q", "-m", "fixture")
	}
	if err := os.MkdirAll(filepath.Join(healthy, ".professor", "stm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(resources, "repos.list"),
		[]byte(missing+"\n"+healthy+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var calls []string
	result, err := morningWith(context.Background(), MorningRequest{
		RegistryBase:     root,
		ResourcesRoot:    resources,
		RepositoriesFile: filepath.Join(resources, "repos.list"),
	}, morningDependencies{
		night: func(_ context.Context, request NightRequest, _ NightDependencies) (NightResult, error) {
			calls = append(calls, request.RepoRoot)
			return NightResult{}, nil
		},
		probeOrgan:        morningOrganResolvable,
		nightDependencies: validMorningDependencies(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{healthy}) {
		t.Fatalf("night calls = %q, want only later healthy repository", calls)
	}
	if !result.Failed || len(result.Repositories) != 2 || len(result.Runs) != 2 {
		t.Fatalf("morning result = %+v", result)
	}
	if result.Repositories[0].Outcome != MorningOrganUnresolved ||
		!strings.Contains(result.Repositories[0].Reason, filepath.Join(missing, ".professor", "stm")) ||
		result.Repositories[1].Outcome != MorningNightCompleted {
		t.Fatalf("repository outcomes = %+v", result.Repositories)
	}
	if result.Runs[0].Outcome != MorningOrganUnresolved || result.Runs[1].Outcome != MorningNightCompleted {
		t.Fatalf("run outcomes = %+v", result.Runs)
	}
	if !strings.Contains(result.Stderr, "dreamer-morning: FAIL repo="+missing+" agent=Explore rc=1\n") ||
		!strings.Contains(result.Stdout, "dreamer-morning: PASS repo="+healthy+" agent=Explore\n") {
		t.Fatalf("morning stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

type fakeMorningGit struct{}

func (fakeMorningGit) Head(context.Context) (string, error) { return strings.Repeat("a", 40), nil }
func (fakeMorningGit) Resolve(string, string) (gate.GitObject, bool, error) {
	return gate.GitObject{}, false, nil
}

func validMorningDependencies() NightDependencies {
	return NightDependencies{
		SeatLaw:       seat.RequiredSeatLaw(),
		NewSeatRunner: func(seat.EventSink) (NightSeatRunner, error) { return nil, errors.New("unused") },
		Git:           func(string) NightGitReader { return fakeMorningGit{} },
		Clock:         time.Now,
		AcquireLock:   func(string) (func() error, error) { return func() error { return nil }, nil },
		Stdout:        io.Discard,
		Stderr:        io.Discard,
	}
}
