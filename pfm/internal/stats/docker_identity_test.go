package stats

import (
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

func TestDockerInspectorReadsIdentityFromJailedSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "probe-docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	id := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/containers/"+id+"/json" {
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if _, err := writer.Write(
			[]byte(`{"Name":"/professor-web","Config":{"Image":"registry.example/professor:web"}}`),
		); err != nil {
			t.Errorf("write jailed Docker response: %v", err)
		}
	})}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close jailed Docker server: %v", err)
		}
		if err := <-serveErrors; err != nil && err != http.ErrServerClosed {
			t.Errorf("serve jailed Docker fixture: %v", err)
		}
	})

	name, image, err := newDockerInspector(socket)(id)
	if err != nil {
		t.Fatal(err)
	}
	if name != "/professor-web" || image != "registry.example/professor:web" {
		t.Fatalf("Docker inspector identity = %q %q", name, image)
	}
}

// TestNewSamplerWithDockerSocketWiresResolveDockerIdentities proves the
// "production only" NewSampler construction path — normally reachable only
// against the real host's /var/run/docker.sock — runs in the fence: the
// seam (NewSamplerWithDockerSocket) points it at a jailed unix socket, and
// resolveDockerIdentities is exercised end to end through the wiring
// NewSampler itself builds, not just newDockerInspector called directly.
func TestNewSamplerWithDockerSocketWiresResolveDockerIdentities(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "probe-docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"Name":"/professor-web","Config":{"Image":"registry.example/professor:web"}}`))
	})}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close jailed Docker server: %v", err)
		}
		if err := <-serveErrors; err != nil && err != http.ErrServerClosed {
			t.Errorf("serve jailed Docker fixture: %v", err)
		}
	})

	sampler := NewSamplerWithDockerSocket(t.TempDir(), t.TempDir(), socket)
	if sampler.DockerInspect == nil {
		t.Fatal("NewSamplerWithDockerSocket built a Sampler with a nil DockerInspect")
	}
	containers := []Container{{ID: "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"}}
	if warnings := sampler.resolveDockerIdentities(containers); len(warnings) != 0 {
		t.Fatalf("resolveDockerIdentities warnings = %v, want none", warnings)
	}
	if containers[0].Name != "professor-web" || containers[0].Image != "registry.example/professor:web" {
		t.Fatalf("resolved container = %+v", containers[0])
	}
}
