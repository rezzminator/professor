package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

type harvesterReleaseFixture struct {
	binary []byte
	fail   string
	calls  []string
}

func (fixture *harvesterReleaseFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	fixture.calls = append(fixture.calls, request.URL.String())
	asset := "harvester_0.1.0_" + runtime.GOOS + "_" + runtime.GOARCH
	var content []byte
	status := http.StatusOK
	switch request.URL.String() {
	case "https://api.github.com/repos/rezzminator/harvester/releases/latest":
		content = []byte(`{"tag_name":"v0.1.0"}`)
		if fixture.fail == "metadata" {
			return nil, errors.New("fixture metadata download failed")
		}
		if fixture.fail == "invalid-tag" {
			content = []byte(`{"tag_name":"../unsafe"}`)
		}
	case "https://github.com/rezzminator/harvester/releases/download/v0.1.0/SHA256SUMS":
		content = fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(fixture.binary), asset)
		if fixture.fail == "checksum" {
			content = []byte(strings.Repeat("0", 64) + "  " + asset + "\n")
		}
		if fixture.fail == "missing-checksum" {
			content = []byte(strings.Repeat("0", 64) + "  other-platform\n")
		}
		if fixture.fail == "checksums-download" {
			status = http.StatusServiceUnavailable
		}
	case "https://github.com/rezzminator/harvester/releases/download/v0.1.0/" + asset:
		content = fixture.binary
		if fixture.fail == "binary-download" {
			return nil, errors.New("fixture binary download failed")
		}
	default:
		return nil, fmt.Errorf("unexpected release request: %s", request.URL)
	}
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d fixture", status),
		Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(content)), Request: request,
	}, nil
}

func TestHarvesterReleaseFailurePrecedesMigration(t *testing.T) {
	for _, failure := range []string{"metadata", "invalid-tag", "checksums-download", "missing-checksum", "binary-download", "checksum"} {
		t.Run(failure, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(t.TempDir(), "pfm.config.json")
			writeFixture(t, config, `{"version":2}`)
			legacy := filepath.Join(filepath.Dir(config), harvesterConfigFileName)
			original := `{"enabled":true,"external":{"enabled":true,"token":"opaque"}}`
			writeFixture(t, legacy, original)
			fixture := &harvesterReleaseFixture{binary: []byte("verified binary"), fail: failure}
			runner := &pluginRunner{}
			var output bytes.Buffer
			inst := &engine{options: Options{
				Home: home, Env: &paths.MapEnv{}, Stdout: &output,
				Version: "1.0.0", MCPConfigPath: config, ProcessRunner: runner,
				MCPEnabled:         map[string]bool{"harvester": true},
				GoPluginHTTPClient: &http.Client{Transport: fixture},
			}, apply: true}
			err := inst.installHarvester(context.Background())
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "harvester") {
				t.Fatalf("release failure must be named: %v", err)
			}
			if readFixture(t, legacy) != original || len(runner.calls) != 0 {
				t.Fatalf("failed release migrated config or ran setup: %v", runner.calls)
			}
			if _, err := os.Lstat(mcpFamilyBinary(home, "harvester")); !os.IsNotExist(err) {
				t.Fatalf("failed release published a PATH binary: %v", err)
			}
			if _, err := os.Stat(harvesterConfigPath(&paths.MapEnv{}, home)); !os.IsNotExist(err) {
				t.Fatalf("failed release wrote plugin config: %v", err)
			}
		})
	}
}

func TestHarvesterOfflineReleaseRejectsUnverifiedCache(t *testing.T) {
	for _, invalid := range []string{"absent", "receipt", "binary", "mode"} {
		t.Run(invalid, func(t *testing.T) {
			home := t.TempDir()
			fixture := &harvesterReleaseFixture{binary: []byte("verified binary")}
			var output bytes.Buffer
			inst := &engine{options: Options{
				Home: home, Env: &paths.MapEnv{}, Stdout: &output,
				GoPluginHTTPClient: &http.Client{Transport: fixture},
			}, apply: true}
			root, err := harvesterHome(inst.env(), home)
			if err != nil {
				t.Fatal(err)
			}
			if invalid != "absent" {
				binary, err := inst.ensureHarvesterRelease(context.Background(), root)
				if err != nil {
					t.Fatal(err)
				}
				switch invalid {
				case "receipt":
					writeFixture(
						t,
						filepath.Join(root, "bin", harvesterReleaseReceiptName),
						`{"version":"../../unsafe"}`,
					)
				case "binary":
					writeFixture(t, binary, "changed since verification")
				case "mode":
					if err := os.Chmod(binary, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			requests := len(fixture.calls)
			inst.options.Offline = true
			if _, err := inst.ensureHarvesterRelease(context.Background(), root); err == nil ||
				!strings.Contains(err.Error(), "harvester") {
				t.Fatalf("unverified offline cache accepted: %v", err)
			}
			if len(fixture.calls) != requests {
				t.Fatal("offline cache validation fetched from network")
			}
		})
	}
}

func TestHarvesterReleaseChecksumRejectsAmbiguity(t *testing.T) {
	asset := harvesterReleaseAsset("0.1.0")
	digest := strings.Repeat("a", 64)
	for _, manifest := range []string{
		digest + "  " + asset + "\n" + digest + "  " + asset,
		"invalid  " + asset,
		strings.Repeat("z", 64) + "  " + asset,
	} {
		if _, err := goPluginAssetChecksum([]byte(manifest), asset); err == nil {
			t.Fatalf("ambiguous or malformed checksum accepted: %q", manifest)
		}
	}
}
