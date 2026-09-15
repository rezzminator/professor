package harvestpy

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

//go:embed assets/converter.py assets/pyproject.toml assets/uv.lock assets/targets.json assets/size-report.json
//go:embed assets/browser/browser.py assets/browser/pyproject.toml assets/browser/uv.lock
var embeddedAssets embed.FS

// Platform is the native target for which the managed runtime is provisioned.
type Platform struct {
	GOOS   string
	GOARCH string
}

func (platform Platform) String() string {
	return platform.GOOS + "-" + platform.GOARCH
}

type Artifact struct {
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	BinarySHA256 string `json:"binary_sha256,omitempty"`
}

// Target contains the complete, immutable download inputs for one supported
// native platform.  The archive digests are checked before extraction.
type Target struct {
	Platform      Platform
	UV            Artifact
	Python        Artifact
	UVVersion     string
	PythonVersion string
}

type targetManifest struct {
	Schema        int                    `json:"schema"`
	UVVersion     string                 `json:"uv_version"`
	PythonVersion string                 `json:"python_version"`
	Targets       map[string]targetEntry `json:"targets"`
}

type targetEntry struct {
	UV     Artifact `json:"uv"`
	Python Artifact `json:"python"`
}

var immutableTargets = loadTargets()

func loadTargets() map[Platform]Target {
	body, err := embeddedAssets.ReadFile("assets/targets.json")
	if err != nil {
		panic(fmt.Sprintf("harvestpy embedded target manifest: %v", err))
	}
	var manifest targetManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		panic(fmt.Sprintf("harvestpy embedded target manifest JSON: %v", err))
	}
	if manifest.Schema != 1 || manifest.UVVersion == "" || manifest.PythonVersion == "" {
		panic("harvestpy embedded target manifest has unsupported schema")
	}
	result := make(map[Platform]Target, len(manifest.Targets))
	for name, entry := range manifest.Targets {
		var platform Platform
		switch name {
		case "linux-amd64":
			platform = Platform{GOOS: "linux", GOARCH: "amd64"}
		case "linux-arm64":
			platform = Platform{GOOS: "linux", GOARCH: "arm64"}
		case "darwin-amd64":
			platform = Platform{GOOS: "darwin", GOARCH: "amd64"}
		case "darwin-arm64":
			platform = Platform{GOOS: "darwin", GOARCH: "arm64"}
		default:
			panic(fmt.Sprintf("harvestpy embedded target manifest has unknown target %q", name))
		}
		for label, item := range map[string]Artifact{"uv": entry.UV, "python": entry.Python} {
			if item.URL == "" || len(item.SHA256) != sha256.Size*2 {
				panic(fmt.Sprintf("harvestpy target %s has invalid %s pin", name, label))
			}
			if _, err := hex.DecodeString(item.SHA256); err != nil {
				panic(fmt.Sprintf("harvestpy target %s has invalid %s sha256: %v", name, label, err))
			}
		}
		result[platform] = Target{
			Platform:      platform,
			UV:            entry.UV,
			Python:        entry.Python,
			UVVersion:     manifest.UVVersion,
			PythonVersion: manifest.PythonVersion,
		}
	}
	return result
}

// Targets returns a copy so callers cannot mutate the process-wide pins.
func Targets() map[Platform]Target {
	result := make(map[Platform]Target, len(immutableTargets))
	for platform := range immutableTargets {
		result[platform] = immutableTargets[platform]
	}
	return result
}

// ConverterSource returns the exact embedded Python worker source.
func ConverterSource() []byte {
	body, err := embeddedAssets.ReadFile("assets/converter.py")
	if err != nil {
		panic(fmt.Sprintf("read embedded converter source: %v", err))
	}
	return append([]byte(nil), body...)
}

// ProjectMetadata returns the minimal conversion-only pyproject.
func ProjectMetadata() []byte {
	body, err := embeddedAssets.ReadFile("assets/pyproject.toml")
	if err != nil {
		panic(fmt.Sprintf("read embedded conversion project: %v", err))
	}
	return append([]byte(nil), body...)
}

// LockMetadata returns the frozen transitive lock generated for the project.
func LockMetadata() []byte {
	body, err := embeddedAssets.ReadFile("assets/uv.lock")
	if err != nil {
		panic(fmt.Sprintf("read embedded conversion lock: %v", err))
	}
	return append([]byte(nil), body...)
}

// BrowserWorkerSource returns the exact embedded real-browser worker source.
func BrowserWorkerSource() []byte {
	body, err := embeddedAssets.ReadFile("assets/browser/browser.py")
	if err != nil {
		panic(fmt.Sprintf("read embedded browser worker source: %v", err))
	}
	return append([]byte(nil), body...)
}

// BrowserProjectMetadata returns the minimal opt-in browser pyproject.
func BrowserProjectMetadata() []byte {
	body, err := embeddedAssets.ReadFile("assets/browser/pyproject.toml")
	if err != nil {
		panic(fmt.Sprintf("read embedded browser project: %v", err))
	}
	return append([]byte(nil), body...)
}

// BrowserLockMetadata returns the frozen transitive lock for the browser env.
func BrowserLockMetadata() []byte {
	body, err := embeddedAssets.ReadFile("assets/browser/uv.lock")
	if err != nil {
		panic(fmt.Sprintf("read embedded browser lock: %v", err))
	}
	return append([]byte(nil), body...)
}

// SizeReportMetadata returns measured shipping/runtime sizes.  Unknown fields
// are intentionally recorded as unknown rather than inferred from another
// target's wheel closure.
func SizeReportMetadata() []byte {
	body, err := embeddedAssets.ReadFile("assets/size-report.json")
	if err != nil {
		panic(fmt.Sprintf("read embedded harvestpy size report: %v", err))
	}
	return append([]byte(nil), body...)
}
