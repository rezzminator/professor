package restart

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// dockerModel is docker over SearXNG containers by name and their running state.
type dockerModel struct {
	mu      sync.Mutex
	running map[string]bool
	psFail  bool
}

func (model *dockerModel) run(argv []string) (deps.RunResult, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	switch argv[1] {
	case "ps":
		if model.psFail {
			return exitWith(1, "Cannot connect to the Docker daemon")
		}
		var names []string
		for name := range model.running {
			names = append(names, name)
		}
		return answer(strings.Join(names, "\n"))
	case "inspect":
		if model.running[argv[len(argv)-1]] {
			return answer("true\n")
		}
		return answer("false\n")
	case "stop":
		model.running[argv[2]] = false
		return answer("")
	case "start":
		model.running[argv[2]] = true
		return answer("")
	}
	return exitWith(64, "unknown docker verb")
}

func searchPlan(t *testing.T, url string, model *dockerModel, probe func(context.Context, string) error) Plan {
	t.Helper()
	runner := &hostRunner{handle: model.run, paths: map[string]string{"docker": "/usr/bin/docker"}}
	dependencies := testDeps(t, runner, newKernel(), &fakeFleet{})
	dependencies.SearchURL = url
	if probe != nil {
		dependencies.HTTPProbe = probe
	}
	finder := &discoverer{deps: dependencies}
	finder.findSearch(context.Background())
	return finder.plan
}

func TestFindSearchNamesWhyThereIsNothingToRestart(t *testing.T) {
	for url, want := range map[string]string{
		"":                           "no search.searxngURL",
		"https://search.example.org": "not on this host's loopback",
	} {
		plan := searchPlan(t, url, &dockerModel{running: map[string]bool{}}, nil)
		if len(plan.Units) != 0 || plan.Findings[0].Err != nil || !strings.Contains(plan.Findings[0].Detail, want) {
			t.Fatalf("url %q = %+v, want %q", url, plan.Findings, want)
		}
	}
}

func TestFindSearchALocalBackendNothingRunsIsAFailedFind(t *testing.T) {
	plan := searchPlan(t, "http://127.0.0.1:8888", &dockerModel{running: map[string]bool{}}, nil)
	if plan.Findings[0].Err == nil || !strings.Contains(plan.Findings[0].Err.Error(), "pfm cannot restart it") {
		t.Fatalf("an unrestartable local SearXNG = %+v", plan.Findings)
	}
	plan = searchPlan(t, "http://localhost:8888", &dockerModel{running: map[string]bool{}, psFail: true}, nil)
	if plan.Findings[0].Err == nil || !strings.Contains(plan.Findings[0].Err.Error(), "Docker daemon") {
		t.Fatalf("an unreadable docker ps = %+v", plan.Findings)
	}
	plan = searchPlan(t, "::not a url", &dockerModel{running: map[string]bool{}}, nil)
	if plan.Findings[0].Err == nil || !strings.Contains(plan.Findings[0].Err.Error(), "is not a URL") {
		t.Fatalf("a malformed URL = %+v", plan.Findings)
	}
}

func TestSearchContainerStopsAndStartsVerified(t *testing.T) {
	model := &dockerModel{running: map[string]bool{"searxng": true}}
	probed := ""
	plan := searchPlan(t, "http://localhost:8888", model, func(_ context.Context, target string) error {
		probed = target
		return nil
	})
	if len(plan.Units) != 1 || plan.Units[0].Name != "docker container searxng" {
		t.Fatalf("units = %v", unitNames(plan.Units))
	}
	ctx := context.Background()
	if detail, err := plan.Units[0].Stop(
		ctx,
	); err != nil || model.running["searxng"] ||
		!strings.Contains(detail, "Running=false") {
		t.Fatalf("stop = %q, %v", detail, err)
	}
	if detail, err := plan.Units[0].Start(ctx); err != nil || !strings.Contains(detail, "search URL answered") ||
		probed != "http://localhost:8888" {
		t.Fatalf("start = %q, %v (probed %q)", detail, err, probed)
	}
}

func TestSearchContainerThatNeverAnswersIsAFailedStart(t *testing.T) {
	model := &dockerModel{running: map[string]bool{"searxng": false}}
	plan := searchPlan(t, "http://127.0.0.1:8888", model, func(context.Context, string) error {
		return errors.New("connection refused")
	})
	if _, err := plan.Units[0].Start(
		context.Background(),
	); err == nil ||
		!strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("start = %v, want the last probe named", err)
	}
}

func TestLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "10.0.0.2": false, "searx": false} {
		if got := loopbackHost(host); got != want {
			t.Fatalf("loopbackHost(%q) = %v", host, got)
		}
	}
}
