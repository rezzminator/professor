package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

const dockerIdentityResponseLimit = 4 << 20

type dockerIdentity struct {
	name  string
	image string
	err   string
}

// dockerSocketPath is where a real Docker daemon's control socket lives.
const dockerSocketPath = "/var/run/docker.sock"

// NewSampler wires the Docker identity resolver against the real daemon
// socket. Resource pressure still comes exclusively from cgroups; the
// daemon is contacted at most once for each newly observed immutable
// container ID.
func NewSampler(procRoot, cgroupRoot string) *Sampler {
	return NewSamplerWithDockerSocket(procRoot, cgroupRoot, dockerSocketPath)
}

// NewSamplerWithDockerSocket is NewSampler with the daemon socket path as a
// seam: a test wires it to a jailed unix socket (hostfixture-style) instead
// of the real, host-only /var/run/docker.sock, so the "production" wiring
// path runs — and is proven — in the fence, not only newDockerInspector in
// isolation.
func NewSamplerWithDockerSocket(procRoot, cgroupRoot, dockerSocket string) *Sampler {
	return &Sampler{
		ProcRoot: procRoot, CgroupRoot: cgroupRoot,
		DockerInspect: newDockerInspector(dockerSocket),
	}
}

func (sampler *Sampler) resolveDockerIdentities(containers []Container) []string {
	if sampler.DockerInspect == nil {
		return nil
	}
	sampler.dockerMu.Lock()
	defer sampler.dockerMu.Unlock()
	if sampler.dockerIdentities == nil {
		sampler.dockerIdentities = make(map[string]dockerIdentity)
	}
	var warnings []string
	for index := range containers {
		identity, found := sampler.dockerIdentities[containers[index].ID]
		if !found {
			name, image, err := sampler.DockerInspect(containers[index].ID)
			identity = dockerIdentity{
				name:  strings.TrimPrefix(strings.TrimSpace(name), "/"),
				image: strings.TrimSpace(image),
			}
			if err != nil {
				identity.err = err.Error()
			} else if identity.name == "" || identity.image == "" {
				identity.err = "daemon response omitted the container name or image"
			}
			sampler.dockerIdentities[containers[index].ID] = identity
		}
		if identity.err != "" {
			warnings = append(warnings, fmt.Sprintf(
				"resolve Docker identity %s: %s",
				shortDockerID(containers[index].ID), identity.err,
			))
			continue
		}
		containers[index].Name = identity.name
		containers[index].Image = identity.image
	}
	return warnings
}

func newDockerInspector(socketPath string) func(string) (string, string, error) {
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 750 * time.Millisecond}
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	client := obs.WrapClient(&http.Client{Transport: transport, Timeout: time.Second})
	return func(id string) (string, string, error) {
		request, err := http.NewRequest(
			http.MethodGet,
			"http://docker/containers/"+url.PathEscape(id)+"/json",
			http.NoBody,
		)
		if err != nil {
			return "", "", fmt.Errorf("build Docker identity request: %w", err)
		}
		response, err := client.Do(request)
		if err != nil {
			return "", "", fmt.Errorf("query Docker identity: %w", err)
		}
		if response.StatusCode != http.StatusOK {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<10))
			closeErr := response.Body.Close()
			if readErr != nil {
				return "", "", fmt.Errorf("query Docker identity: HTTP %s; read response: %w", response.Status, readErr)
			}
			if closeErr != nil {
				return "", "", fmt.Errorf(
					"query Docker identity: HTTP %s; close response: %w",
					response.Status,
					closeErr,
				)
			}
			return "", "", fmt.Errorf(
				"query Docker identity: HTTP %s: %s",
				response.Status,
				strings.TrimSpace(string(body)),
			)
		}
		var payload struct {
			Name   string `json:"Name"`
			Config struct {
				Image string `json:"Image"`
			} `json:"Config"`
		}
		decoder := json.NewDecoder(io.LimitReader(response.Body, dockerIdentityResponseLimit))
		decodeErr := decoder.Decode(&payload)
		closeErr := response.Body.Close()
		if decodeErr != nil {
			return "", "", fmt.Errorf("decode Docker identity response: %w", decodeErr)
		}
		if closeErr != nil {
			return "", "", fmt.Errorf("close Docker identity response: %w", closeErr)
		}
		return payload.Name, payload.Config.Image, nil
	}
}
