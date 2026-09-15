package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hostops/pfm/internal/atomicfile"
)

const (
	themeManifestRelative = "templates/themes/sources.json"
	themeOwnershipName    = "theme-ownership.json"
	themeOwnerPlaceholder = "{GH_USER}"
	maxThemeDownloadBytes = 10 << 20
)

type themeManifest struct {
	Comment       string                  `json:"_comment,omitempty"`
	SourceFetched map[string]themeSource  `json:"source_fetched,omitempty"`
	Bundled       map[string]bundledTheme `json:"bundled,omitempty"`
}

type themeSource struct {
	Repo     string `json:"repo"`
	Raw      string `json:"raw"`
	Target   string `json:"target"`
	Activate string `json:"activate"`
	Requires string `json:"requires"`
	// local is the absolute path of a bundled palette read from the source
	// clone; empty for a source-fetched theme or a bundled one resolved
	// against the release manifest, both of which download Raw.
	local string
	// base names the source_fetched theme a bundled overlay is merged onto;
	// empty for a complete palette.
	base string
}

// bundledTheme is a palette the blueprint ships itself under templates/themes/:
// File names it beside sources.json, so it is read from the source clone
// when the manifest is, or downloaded from beside the release manifest.
// With Base set the file is an overlay — only `name` and the `overrides`
// keys that differ — written merged onto the fetched base, so the base is
// never vendored and cannot drift.
type bundledTheme struct {
	File     string `json:"file"`
	Base     string `json:"base,omitempty"`
	Target   string `json:"target"`
	Activate string `json:"activate"`
	Requires string `json:"requires"`
}

// themePalette is the Claude Code theme file shape both kinds share.
type themePalette struct {
	Name      string            `json:"name"`
	Base      string            `json:"base,omitempty"`
	Overrides map[string]string `json:"overrides"`
}

type themeOwnershipRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func (installer *engine) installThemes(ctx context.Context) {
	if !installer.options.InstallThemes {
		return
	}
	sources, err := loadThemeSources(ctx, installer.options)
	if err != nil {
		installer.skip("themes NOT installed: load " + themeManifestRelative + ": " + err.Error())
		return
	}
	ownershipPath := filepath.Join(installer.managedRoot, themeOwnershipName)
	ownership, err := readThemeOwnership(ownershipPath)
	if err != nil {
		installer.skip("themes NOT installed: read ownership " + ownershipPath + ": " + err.Error())
		return
	}

	bases := map[string][]byte{} // fetched base palettes, one download per run
	for _, name := range sortedThemeNames(sources) {
		source := sources[name]
		target, targetErr := themeTarget(installer.options.Home, source.Target)
		if targetErr != nil {
			installer.skip("theme " + name + " manifest target invalid: " + targetErr.Error())
			continue
		}
		existing, exists, readErr := readOptionalRegularFile(target)
		if readErr != nil {
			installer.skip("theme " + name + " inspect failed: " + readErr.Error())
			continue
		}
		record, owned := ownership[name]
		if owned {
			if filepath.Clean(record.Path) != filepath.Clean(target) {
				installer.skip(
					fmt.Sprintf("theme %s ownership target drift: ledger=%s manifest=%s", name, record.Path, target),
				)
				continue
			}
			if exists && contentSHA256(existing) != record.SHA256 {
				installer.skip("theme " + name + " locally modified; left in place at " + target)
				continue
			}
		} else if exists {
			installer.skip("theme " + name + " already exists but is not installer-owned; left in place at " + target)
			continue
		}

		if !installer.apply {
			if owned && exists {
				installer.ok("theme " + name + " currently installed; apply checks its source for updates")
			} else {
				verb := "fetch theme"
				if source.local != "" {
					verb = "read bundled theme"
				}
				if changeErr := installer.change(verb+" "+name+" -> "+target, nil); changeErr != nil {
					installer.skip("theme " + name + " preview failed: " + changeErr.Error())
				}
			}
			continue
		}

		content, loadErr := loadThemeContent(ctx, installer.options.ThemeHTTPClient, source)
		if loadErr != nil {
			installer.skip("theme " + name + " " + loadErr.Error())
			continue
		}
		if source.base != "" {
			base, cached := bases[source.base]
			if !cached {
				fetched, baseErr := loadThemeContent(ctx, installer.options.ThemeHTTPClient, sources[source.base])
				if baseErr != nil {
					installer.skip("theme " + name + " base " + source.base + " " + baseErr.Error())
					continue
				}
				base, bases[source.base] = fetched, fetched
			}
			merged, mergeErr := mergeThemeOverlay(base, content)
			if mergeErr != nil {
				installer.skip("theme " + name + " overlay onto " + source.base + " failed: " + mergeErr.Error())
				continue
			}
			content = merged
		}
		digest := contentSHA256(content)
		if exists && bytes.Equal(existing, content) && owned && record.SHA256 == digest {
			installer.ok("theme " + name + " unchanged at " + target)
			continue
		}

		next := cloneThemeOwnership(ownership)
		next[name] = themeOwnershipRecord{Path: target, SHA256: digest}
		if writeErr := atomicfile.Write(target, content, 0o644); writeErr != nil {
			installer.skip("theme " + name + " install failed: write " + target + ": " + writeErr.Error())
			continue
		}
		if ledgerErr := writeThemeOwnership(ownershipPath, next); ledgerErr != nil {
			rollbackErr := rollbackTheme(target, existing, exists)
			message := "theme " + name + " install failed: record ownership: " + ledgerErr.Error()
			if rollbackErr != nil {
				message += "; rollback failed: " + rollbackErr.Error()
			}
			installer.skip(message)
			continue
		}
		ownership = next
		if changeErr := installer.change("write theme "+name+" -> "+target, nil); changeErr != nil {
			installer.skip("theme " + name + " report failed after install: " + changeErr.Error())
		}
	}
}

func (installer *engine) uninstallThemes() {
	if !installer.options.InstallThemes {
		return
	}
	ownershipPath := filepath.Join(installer.managedRoot, themeOwnershipName)
	ownership, err := readThemeOwnership(ownershipPath)
	if err != nil {
		installer.skip("themes NOT removed: read ownership " + ownershipPath + ": " + err.Error())
		return
	}
	if len(ownership) == 0 {
		installer.ok("theme ownership ledger absent; no themes removed")
		return
	}

	for _, name := range sortedThemeOwnershipNames(ownership) {
		record := ownership[name]
		content, exists, readErr := readOptionalRegularFile(record.Path)
		if readErr != nil {
			installer.skip("theme " + name + " uninstall inspect failed: " + readErr.Error())
			continue
		}
		if exists && contentSHA256(content) != record.SHA256 {
			installer.skip(
				"theme " + name + " locally modified; left in place and retained recovery ownership at " + record.Path,
			)
			continue
		}
		if !installer.apply {
			if changeErr := installer.change("remove theme "+name+" "+record.Path, nil); changeErr != nil {
				installer.skip("theme " + name + " uninstall preview failed: " + changeErr.Error())
			}
			continue
		}

		next := cloneThemeOwnership(ownership)
		delete(next, name)
		if exists {
			if removeErr := os.Remove(record.Path); removeErr != nil {
				installer.skip("theme " + name + " uninstall failed: remove " + record.Path + ": " + removeErr.Error())
				continue
			}
		}
		if ledgerErr := writeThemeOwnership(ownershipPath, next); ledgerErr != nil {
			var rollbackErr error
			if exists {
				rollbackErr = atomicfile.Write(record.Path, content, 0o644)
			}
			message := "theme " + name + " uninstall failed: update ownership: " + ledgerErr.Error()
			if rollbackErr != nil {
				message += "; rollback failed: " + rollbackErr.Error()
			}
			installer.skip(message)
			continue
		}
		ownership = next
		if changeErr := installer.change("remove theme "+name+" "+record.Path, nil); changeErr != nil {
			installer.skip("theme " + name + " uninstall report failed: " + changeErr.Error())
		}
	}
	if installer.apply {
		themesDir := filepath.Join(installer.options.Home, ".claude", "themes")
		if removeErr := os.Remove(
			themesDir,
		); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) &&
			!errors.Is(removeErr, fs.ErrExist) {
			installer.skip("leave theme directory " + themesDir + ": " + removeErr.Error())
		}
	}
}

// loadThemeContent returns a theme's palette bytes: a bundled palette from the
// source clone is read from disk ("read failed: ..." names the path), anything
// else is downloaded ("fetch failed: ..."); either way non-JSON is refused.
func loadThemeContent(ctx context.Context, client *http.Client, source themeSource) ([]byte, error) {
	var content []byte
	if source.local != "" {
		read, err := os.ReadFile(source.local)
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
		}
		content = read
	} else {
		fetched, err := fetchTheme(ctx, client, source.Raw)
		if err != nil {
			return nil, fmt.Errorf("fetch failed: %w", err)
		}
		content = fetched
	}
	if !json.Valid(content) {
		if source.local != "" {
			return nil, fmt.Errorf("read failed: %s is not valid JSON", source.local)
		}
		return nil, errors.New("fetch failed: response is not valid JSON")
	}
	return content, nil
}

// mergeThemeOverlay writes the base palette with the overlay's name (when set)
// and its overrides on top; a key the overlay does not name keeps the base value.
func mergeThemeOverlay(base, overlay []byte) ([]byte, error) {
	var basePalette, overlayPalette themePalette
	if err := json.Unmarshal(base, &basePalette); err != nil {
		return nil, fmt.Errorf("decode base palette: %w", err)
	}
	if err := json.Unmarshal(overlay, &overlayPalette); err != nil {
		return nil, fmt.Errorf("decode overlay: %w", err)
	}
	if len(overlayPalette.Overrides) == 0 {
		return nil, errors.New("overlay carries no overrides")
	}
	if len(basePalette.Overrides) == 0 {
		return nil, errors.New("base palette carries no overrides")
	}
	for key, value := range overlayPalette.Overrides {
		basePalette.Overrides[key] = value
	}
	if overlayPalette.Name != "" {
		basePalette.Name = overlayPalette.Name
	}
	content, err := json.MarshalIndent(basePalette, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode merged palette: %w", err)
	}
	return append(content, '\n'), nil
}

// releaseManifestUnpublishedAlpha reports whether a release theme manifest
// URL names an -alpha version reference. professorThemeManifestURL builds
// this URL from VERSION, and pfm never publishes an -alpha tag on GitHub, so
// that raw.githubusercontent.com URL 404s every time; loadThemeSources turns
// that predictable failure into a named refusal instead of a bare HTTP
// error, and skips the doomed fetch entirely.
func releaseManifestUnpublishedAlpha(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if strings.HasSuffix(segment, "-alpha") {
			return true
		}
	}
	return false
}

func loadThemeSources(ctx context.Context, options Options) (map[string]themeSource, error) {
	var content []byte
	var origin string
	var localThemes string // templates/themes/ in the source clone when the manifest was read there
	var err error
	if strings.TrimSpace(options.SourceRepo) != "" {
		origin = filepath.Join(options.SourceRepo, filepath.FromSlash(themeManifestRelative))
		localThemes = filepath.Dir(origin)
		content, err = os.ReadFile(origin)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("read local manifest %s: %w", origin, err)
			}
			localErr := err
			localThemes = ""
			origin = strings.TrimSpace(options.ThemeManifestURL)
			if origin == "" {
				return nil, fmt.Errorf("read local manifest: %w; no release manifest URL is configured", localErr)
			}
			if releaseManifestUnpublishedAlpha(origin) {
				return nil, fmt.Errorf(
					"local theme manifest unavailable: %v; release manifest for an unpublished -alpha build; run pfm install from the source clone",
					localErr,
				)
			}
			content, err = fetchTheme(ctx, options.ThemeHTTPClient, origin)
			if err != nil {
				return nil, fmt.Errorf(
					"local theme manifest unavailable: %v; fetch release manifest %s: %w",
					localErr,
					origin,
					err,
				)
			}
		}
	} else {
		origin = strings.TrimSpace(options.ThemeManifestURL)
		if origin == "" {
			return nil, errors.New("no source repository or release manifest URL is configured")
		}
		if releaseManifestUnpublishedAlpha(origin) {
			return nil, errors.New(
				"release manifest for an unpublished -alpha build; run pfm install from the source clone",
			)
		}
		content, err = fetchTheme(ctx, options.ThemeHTTPClient, origin)
		if err != nil {
			return nil, fmt.Errorf("fetch release manifest %s: %w", origin, err)
		}
	}
	if bytes.Contains(content, []byte(themeOwnerPlaceholder)) {
		owner, ownerErr := themeManifestOwner(options)
		if ownerErr != nil {
			return nil, fmt.Errorf("resolve registered placeholder %s: %w", themeOwnerPlaceholder, ownerErr)
		}
		content = bytes.ReplaceAll(content, []byte(themeOwnerPlaceholder), []byte(owner))
	}
	var manifest themeManifest
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode %s: %w", origin, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode %s: multiple JSON values", origin)
		}
		return nil, fmt.Errorf("decode %s trailing content: %w", origin, err)
	}
	if len(manifest.SourceFetched) == 0 && len(manifest.Bundled) == 0 {
		return nil, fmt.Errorf("manifest %s has no source_fetched or bundled themes", origin)
	}
	sources := make(map[string]themeSource, len(manifest.SourceFetched)+len(manifest.Bundled))
	for name, source := range manifest.SourceFetched {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(source.Raw) == "" ||
			strings.TrimSpace(source.Target) == "" {
			return nil, fmt.Errorf("manifest %s theme %q is missing name, raw, or target", origin, name)
		}
		if err := validateThemeURL(source.Raw); err != nil {
			return nil, fmt.Errorf("manifest %s theme %q raw URL: %w", origin, name, err)
		}
		sources[name] = source
	}
	for name, bundled := range manifest.Bundled {
		file := strings.TrimSpace(bundled.File)
		if strings.TrimSpace(name) == "" || file == "" || strings.TrimSpace(bundled.Target) == "" {
			return nil, fmt.Errorf("manifest %s bundled theme %q is missing name, file, or target", origin, name)
		}
		if file != path.Base(file) || file == "." || file == ".." {
			return nil, fmt.Errorf(
				"manifest %s bundled theme %q file %q must be a bare file name beside the manifest",
				origin,
				name,
				file,
			)
		}
		if _, clash := sources[name]; clash {
			return nil, fmt.Errorf("manifest %s names theme %q as both source_fetched and bundled", origin, name)
		}
		base := strings.TrimSpace(bundled.Base)
		if base != "" {
			if _, known := manifest.SourceFetched[base]; !known {
				return nil, fmt.Errorf(
					"manifest %s bundled theme %q base %q is not a source_fetched theme",
					origin,
					name,
					base,
				)
			}
		}
		source := themeSource{
			Target:   bundled.Target,
			Activate: bundled.Activate,
			Requires: bundled.Requires,
			base:     base,
		}
		if localThemes != "" {
			source.local = filepath.Join(localThemes, file)
		} else {
			// The release manifest was fetched: the palette is published beside it.
			source.Raw = strings.TrimSuffix(origin, path.Base(origin)) + file
			source.Repo = source.Raw
			if err := validateThemeURL(source.Raw); err != nil {
				return nil, fmt.Errorf("manifest %s bundled theme %q release URL: %w", origin, name, err)
			}
		}
		sources[name] = source
	}
	return sources, nil
}

func themeManifestOwner(options Options) (string, error) {
	if strings.TrimSpace(options.SourceRepo) != "" {
		manifestPath := filepath.Join(options.SourceRepo, ".professor", "manifest.json")
		content, err := os.ReadFile(manifestPath)
		if err == nil {
			var manifest struct {
				InstalledFrom struct {
					Repo string `json:"repo"`
				} `json:"installed_from"`
			}
			if decodeErr := json.Unmarshal(content, &manifest); decodeErr != nil {
				return "", fmt.Errorf("decode %s: %w", manifestPath, decodeErr)
			}
			if owner := repositoryOwner(manifest.InstalledFrom.Repo); owner != "" {
				return owner, nil
			}
			return "", fmt.Errorf("%s installed_from.repo does not name owner/repo", manifestPath)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("read %s: %w", manifestPath, err)
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(options.ThemeManifestURL))
	if err != nil {
		return "", fmt.Errorf("parse release manifest URL: %w", err)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 || strings.TrimSpace(segments[0]) == "" {
		return "", fmt.Errorf("release manifest URL %q does not name an owner/repository", options.ThemeManifestURL)
	}
	return segments[0], nil
}

func repositoryOwner(repository string) string {
	value := strings.TrimSpace(strings.TrimSuffix(repository, ".git"))
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = strings.Trim(parsed.Path, "/")
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-2])
}

func validateThemeURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	if parsed.Scheme == "https" && parsed.Host != "" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (host == "127.0.0.1" || host == "::1" || host == "localhost") {
		return nil
	}
	return fmt.Errorf("must be HTTPS (HTTP is accepted only for loopback tests)")
}

func fetchTheme(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, fmt.Errorf("create GET %s: %w", raw, err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", raw, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, drainErr := io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		closeErr := response.Body.Close()
		if drainErr != nil {
			return nil, errors.Join(
				fmt.Errorf("GET %s: HTTP %s; drain response: %w", raw, response.Status, drainErr),
				closeErr,
			)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("GET %s: HTTP %s; close response: %w", raw, response.Status, closeErr)
		}
		return nil, fmt.Errorf("GET %s: HTTP %s", raw, response.Status)
	}
	limited := io.LimitReader(response.Body, maxThemeDownloadBytes+1)
	content, err := io.ReadAll(limited)
	closeErr := response.Body.Close()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read GET %s: %w", raw, err), closeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close GET %s response: %w", raw, closeErr)
	}
	if len(content) > maxThemeDownloadBytes {
		return nil, fmt.Errorf("GET %s exceeds %d bytes", raw, maxThemeDownloadBytes)
	}
	return content, nil
}

func themeTarget(home, target string) (string, error) {
	if !strings.HasPrefix(target, "~/") {
		return "", fmt.Errorf("target %q must start with ~/", target)
	}
	resolved := filepath.Clean(filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(target, "~/"))))
	root := filepath.Join(filepath.Clean(home), ".claude", "themes")
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target %q must name a file beneath ~/.claude/themes", target)
	}
	return resolved, nil
}

func readOptionalRegularFile(filePath string) ([]byte, bool, error) {
	info, err := os.Lstat(filePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s: %w", filePath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, true, fmt.Errorf("%s is not a regular file", filePath)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, true, fmt.Errorf("read %s: %w", filePath, err)
	}
	return content, true, nil
}

func readThemeOwnership(filePath string) (map[string]themeOwnershipRecord, error) {
	content, err := os.ReadFile(filePath)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]themeOwnershipRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := map[string]themeOwnershipRecord{}
	if err := json.Unmarshal(content, &records); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	for name, record := range records {
		if strings.TrimSpace(name) == "" || !filepath.IsAbs(record.Path) || len(record.SHA256) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid ownership record %q", name)
		}
		if _, err := hex.DecodeString(record.SHA256); err != nil {
			return nil, fmt.Errorf("ownership record %q sha256: %w", name, err)
		}
	}
	return records, nil
}

func writeThemeOwnership(filePath string, records map[string]themeOwnershipRecord) error {
	if len(records) == 0 {
		if err := os.Remove(filePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove empty ownership ledger %s: %w", filePath, err)
		}
		return nil
	}
	content, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ownership: %w", err)
	}
	content = append(content, '\n')
	if err := atomicfile.Write(filePath, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filePath, err)
	}
	return nil
}

func rollbackTheme(filePath string, previous []byte, existed bool) error {
	if existed {
		return atomicfile.Write(filePath, previous, 0o644)
	}
	if err := os.Remove(filePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func contentSHA256(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func cloneThemeOwnership(records map[string]themeOwnershipRecord) map[string]themeOwnershipRecord {
	cloned := make(map[string]themeOwnershipRecord, len(records))
	for name, record := range records {
		cloned[name] = record
	}
	return cloned
}

func sortedThemeNames(sources map[string]themeSource) []string {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedThemeOwnershipNames(records map[string]themeOwnershipRecord) []string {
	names := make([]string, 0, len(records))
	for name := range records {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
