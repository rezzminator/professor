package restart

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

// searchName is the word every SearXNG unit or container is found by.
const searchName = "searxng"

// dockerContainer drives one Docker container by name.
type dockerContainer struct {
	serviceTools
	binary string
	name   string
	probe  func(context.Context) error
}

// running asks Docker whether the container runs. A non-answer is an error.
func (container dockerContainer) running(ctx context.Context) (bool, error) {
	result, err := container.run(ctx, container.binary, "inspect", "--format", "{{.State.Running}}", container.name)
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		return false, fmt.Errorf("docker inspect %s: exit %d: %s",
			container.name, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	switch answer := strings.TrimSpace(string(result.Stdout)); answer {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("docker inspect %s: unreadable State.Running %q", container.name, answer)
	}
}

func (container dockerContainer) stop(ctx context.Context) (string, error) {
	result, err := container.run(ctx, container.binary, "stop", container.name)
	if err != nil {
		return "", err
	}
	stopped, err := container.waitFor(ctx, serviceStopWithin, func() (bool, error) {
		up, err := container.running(ctx)
		return !up, err
	})
	if err != nil {
		return "", err
	}
	if !stopped {
		return "", fmt.Errorf("container %s still running after docker stop (exit %d: %s)",
			container.name, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return "docker inspect reads State.Running=false", nil
}

func (container dockerContainer) start(ctx context.Context) (string, error) {
	result, err := container.run(ctx, container.binary, "start", container.name)
	if err != nil {
		return "", err
	}
	var last error
	up, err := container.waitFor(ctx, serviceStartWithin, func() (bool, error) {
		running, err := container.running(ctx)
		if err != nil || !running {
			return false, err
		}
		last = container.probe(ctx)
		return last == nil, nil
	})
	if err != nil {
		return "", err
	}
	if !up {
		return "", fmt.Errorf("container %s not answering %s after docker start (exit %d: %s; last probe: %v)",
			container.name, serviceStartWithin, result.ExitCode, strings.TrimSpace(string(result.Stderr)), last)
	}
	return "State.Running=true, search URL answered", nil
}

// loopbackHost reports whether host names this machine's loopback.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// findSearch is the harvester search backend family: the SearXNG the
// harvester config names, when it runs on this host under a service manager
// or Docker. A local SearXNG nothing restartable runs is a failed find,
// never "nothing to restart".
func (finder *discoverer) findSearch(ctx context.Context) {
	configured := strings.TrimSpace(finder.deps.SearchURL)
	if configured == "" {
		finder.found(FamilySearch, "no search.searxngURL in harvester.config.json — no search backend to restart")
		return
	}
	parsed, err := url.Parse(configured)
	if err != nil || parsed.Hostname() == "" {
		finder.failed(FamilySearch, fmt.Errorf("search.searxngURL %q is not a URL: %w", configured, err))
		return
	}
	if !loopbackHost(parsed.Hostname()) {
		finder.found(
			FamilySearch,
			"SearXNG at "+configured+" is not on this host's loopback — not this host's to restart",
		)
		return
	}
	probe := func(ctx context.Context) error { return finder.deps.HTTPProbe(ctx, configured) }
	units, details, err := finder.searchUnits(ctx, probe)
	if err != nil {
		finder.failed(FamilySearch, err)
		return
	}
	if len(units) == 0 {
		finder.failed(FamilySearch, fmt.Errorf(
			"SearXNG is configured at %s on this host, but no user service or Docker container named %s runs it "+
				"(%s) — pfm cannot restart it", configured, searchName, strings.Join(details, "; ")))
		return
	}
	finder.found(FamilySearch, fmt.Sprintf("%d unit(s) for SearXNG at %s — %s",
		len(units), configured, strings.Join(details, "; ")))
	finder.plan.Units = append(finder.plan.Units, units...)
}

// searchUnits finds SearXNG under the service manager and under Docker.
func (finder *discoverer) searchUnits(
	ctx context.Context,
	probe func(context.Context) error,
) ([]Unit, []string, error) {
	var units []Unit
	var details []string
	if finder.deps.Services != nil {
		managed, detail, err := finder.deps.Services.Units(ctx, FamilySearch, func(name string) bool {
			return strings.Contains(strings.ToLower(name), searchName)
		}, func(string) func(context.Context) error { return probe })
		if err != nil {
			return nil, nil, err
		}
		units = append(units, managed...)
		details = append(details, detail)
	}
	containers, detail, err := finder.dockerUnits(ctx, probe)
	if err != nil {
		return nil, nil, err
	}
	return append(units, containers...), append(details, detail), nil
}

// dockerUnits lists every Docker container whose name holds searxng, each
// with its state read by docker inspect: a stopped one stays stopped, and one
// whose state cannot be read fails the find.
func (finder *discoverer) dockerUnits(ctx context.Context, probe func(context.Context) error) ([]Unit, string, error) {
	binary, err := finder.deps.Runner.LookPath("docker")
	if errors.Is(err, exec.ErrNotFound) {
		return nil, "no docker on PATH", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("resolve docker: %w", err)
	}
	tools := serviceTools{runner: finder.deps.Runner, clock: finder.deps.Clock}
	result, err := tools.run(ctx, binary, "ps", "--all", "--filter", "name="+searchName, "--format", "{{.Names}}")
	if err != nil {
		return nil, "", err
	}
	if result.ExitCode != 0 {
		return nil, "", fmt.Errorf("docker ps: exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	var units []Unit
	for _, name := range strings.Fields(string(result.Stdout)) {
		container := dockerContainer{serviceTools: tools, binary: binary, name: name, probe: probe}
		running, err := container.running(ctx)
		if err != nil {
			return nil, "", err
		}
		before := "State.Running=" + strconv.FormatBool(running)
		unit := Unit{
			Family:   FamilySearch,
			Name:     "docker container " + name,
			FoundBy:  "docker ps --all --filter name=" + searchName + ", docker inspect " + before,
			StopHow:  "docker stop " + name + "; verified: State.Running=false",
			StartHow: "docker start " + name + "; verified: State.Running=true and the search URL answers",
			Stop:     container.stop,
			Start:    container.start,
			Before:   before,
		}
		if !running {
			unit.StaysStopped, unit.Stop, unit.Start = true, nil, nil
		}
		units = append(units, unit)
	}
	return units, fmt.Sprintf("docker ps: %d container(s) named %s", len(units), searchName), nil
}
