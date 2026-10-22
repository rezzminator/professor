package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	goPluginReleaseTimeout       = 120 * time.Second
	goPluginReleaseMetadataLimit = 1 << 20
	goPluginReleaseAssetLimit    = 128 << 20
	harvesterReleaseReceiptName  = ".harvester-release.json"
)

var goPluginReleaseTag = regexp.MustCompile(`^v(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)$`)

type goPluginReleaseReceipt struct {
	Version string `json:"version"`
	Asset   string `json:"asset"`
	SHA256  string `json:"sha256"`
}

// ensureHarvesterRelease provisions the binary independently of every engine's plugin cache.
func (installer *engine) ensureHarvesterRelease(ctx context.Context, root string) (string, error) {
	if (runtime.GOOS != "linux" && runtime.GOOS != "darwin") ||
		(runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return "", fmt.Errorf("harvester release: unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if !installer.apply {
		return filepath.Join(
				root,
				"bin",
				"latest",
				mcpServerHarvester,
			), installer.change(
				"install latest checksum-verified harvester release (offline requires a verified cache)",
				nil,
			)
	}
	receiptPath := filepath.Join(root, "bin", harvesterReleaseReceiptName)
	if installer.options.Offline {
		return installer.cachedHarvesterRelease(receiptPath)
	}
	ctx, cancel := context.WithTimeout(ctx, goPluginReleaseTimeout)
	defer cancel()
	descriptor := goBinaryPlugins[mcpServerHarvester]
	latestURL := "https://api.github.com/repos/" + descriptor.repository + "/releases/latest"
	latest, err := installer.fetchGoPluginRelease(ctx, latestURL, goPluginReleaseMetadataLimit)
	if err != nil {
		return "", fmt.Errorf("resolve latest harvester release: %w", err)
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(latest, &release); err != nil {
		return "", fmt.Errorf("parse latest harvester release: %w", err)
	}
	match := goPluginReleaseTag.FindStringSubmatch(release.Tag)
	if match == nil {
		return "", fmt.Errorf("latest harvester release tag %q is invalid", release.Tag)
	}
	version := match[1]
	asset := harvesterReleaseAsset(version)
	base := installer.env().Get(descriptor.releaseBaseEnv)
	if base == "" {
		base = "https://github.com/" + descriptor.repository + "/releases/download"
	}
	if err := validateThemeURL(base); err != nil {
		return "", fmt.Errorf("harvester release base %q: %w", base, err)
	}
	releaseURL := strings.TrimRight(base, "/") + "/" + release.Tag + "/"
	sums, err := installer.fetchGoPluginRelease(ctx, releaseURL+"SHA256SUMS", goPluginReleaseMetadataLimit)
	if err != nil {
		return "", fmt.Errorf("harvester release checksums: %w", err)
	}
	digest, err := goPluginAssetChecksum(sums, asset)
	if err != nil {
		return "", fmt.Errorf("harvester release checksums: %w", err)
	}
	binary, err := descriptor.binaryPath(installer.env(), installer.options.Home, version)
	if err != nil {
		return "", err
	}
	current, readErr := os.ReadFile(binary)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect harvester release binary %s: %w", binary, readErr)
	}
	if readErr != nil || contentSHA256(current) != digest || !callmeterExecutable(binary) {
		content, err := installer.fetchGoPluginRelease(ctx, releaseURL+asset, goPluginReleaseAssetLimit)
		if err != nil {
			return "", fmt.Errorf("download harvester release asset %s: %w", asset, err)
		}
		if len(content) == 0 || contentSHA256(content) != digest {
			return "", fmt.Errorf("harvester release asset %s checksum mismatch", asset)
		}
		if err := installer.change(
			"install verified harvester "+version+" at "+binary,
			func() error { return atomicfile.Write(binary, content, 0o755) },
		); err != nil {
			return "", err
		}
	}
	receipt, err := json.Marshal(goPluginReleaseReceipt{Version: version, Asset: asset, SHA256: digest})
	if err != nil {
		return "", fmt.Errorf("encode harvester verified release receipt: %w", err)
	}
	if err := atomicfile.Write(receiptPath, append(receipt, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("record verified harvester release %s: %w", receiptPath, err)
	}
	return binary, nil
}

func harvesterReleaseAsset(version string) string {
	return mcpServerHarvester + "_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH
}

func (installer *engine) cachedHarvesterRelease(receiptPath string) (string, error) {
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		return "", fmt.Errorf("harvester offline install requires a verified release cache at %s: %w", receiptPath, err)
	}
	var receipt goPluginReleaseReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return "", fmt.Errorf("parse harvester verified release cache %s: %w", receiptPath, err)
	}
	if !goPluginReleaseTag.MatchString("v"+receipt.Version) ||
		receipt.Asset != harvesterReleaseAsset(receipt.Version) ||
		len(receipt.SHA256) != sha256.Size*2 {
		return "", fmt.Errorf(
			"harvester offline verified release cache %s has invalid version, platform or checksum",
			receiptPath,
		)
	}
	if _, err := hex.DecodeString(receipt.SHA256); err != nil {
		return "", fmt.Errorf("harvester offline verified release cache %s checksum: %w", receiptPath, err)
	}
	binary, err := goBinaryPlugins[mcpServerHarvester].binaryPath(
		installer.env(),
		installer.options.Home,
		receipt.Version,
	)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(binary)
	if err != nil {
		return "", fmt.Errorf("read harvester offline release binary %s: %w", binary, err)
	}
	if !callmeterExecutable(binary) || len(content) == 0 || contentSHA256(content) != receipt.SHA256 {
		return "", fmt.Errorf(
			"harvester offline release binary %s is not executable or checksum verification failed",
			binary,
		)
	}
	installer.ok("harvester verified offline release " + binary)
	return binary, nil
}

func goPluginAssetChecksum(manifest []byte, asset string) (string, error) {
	digest := ""
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		if digest != "" {
			return "", fmt.Errorf("duplicate checksum for %s", asset)
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", fmt.Errorf("invalid checksum for %s", asset)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("invalid checksum for %s: %w", asset, err)
		}
		digest = strings.ToLower(fields[0])
	}
	if digest == "" {
		return "", fmt.Errorf("no checksum for %s", asset)
	}
	return digest, nil
}

func (installer *engine) fetchGoPluginRelease(ctx context.Context, raw string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(obs.Presence(ctx), http.MethodGet, raw, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create Go plugin release GET %s: %w", raw, err)
	}
	request.Header.Set("User-Agent", "pfm")
	client := installer.options.GoPluginHTTPClient
	if client == nil {
		client = obs.WrapClient(&http.Client{Timeout: goPluginReleaseTimeout})
	}
	response, err := obs.WrapClient(client).Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", raw, err)
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read GET %s: %w", raw, errors.Join(readErr, closeErr))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: HTTP %s", raw, response.Status)
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("GET %s exceeds %d bytes", raw, limit)
	}
	return content, nil
}
