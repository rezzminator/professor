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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
	"github.com/rezzminator/professor/pfm/internal/updatecheck"
)

const (
	// themeProfilesRelative is the Professor theme directory: every *.json in
	// it is one theme's profile, and nothing else is.
	themeProfilesRelative = "templates/themes"
	themeOwnershipName    = "theme-ownership.json"
	maxThemeDownloadBytes = 10 << 20
)

// themeProfile is one Professor theme install ships: a {name}.json profile in
// templates/themes/, read from the source clone or, with no clone, downloaded
// from the release.
type themeProfile struct {
	file  string // {name}.json
	local string // its absolute path in the source clone; empty when downloaded
	raw   string // its release URL; empty when read from the source clone
}

type themeOwnershipRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func (installer *engine) installThemes(ctx context.Context) {
	if !installer.options.InstallThemes {
		return
	}
	profiles, err := loadThemeProfiles(ctx, installer.options)
	if err != nil {
		installer.skip("themes NOT installed: load " + themeProfilesRelative + ": " + err.Error())
		return
	}
	ownershipPath := filepath.Join(installer.managedRoot, themeOwnershipName)
	ownership, err := readThemeOwnership(ownershipPath)
	if err != nil {
		installer.skip("themes NOT installed: read ownership " + ownershipPath + ": " + err.Error())
		return
	}
	ownership = installer.retireThemes(ownershipPath, profiles, ownership)

	for _, name := range sortedThemeNames(profiles) {
		source := profiles[name]
		target := filepath.Join(installer.options.Home, ".claude", "themes", source.file)
		claude := themeFile{key: name, path: target}
		if skip := claude.inspect(name, ownership); skip != "" {
			installer.skip(skip)
			continue
		}

		if !installer.apply {
			profile := themeFile{
				key:   profileLedgerKey(name),
				label: " profile copy",
				path:  themesync.ProfilePath(installer.options.Home, name),
			}
			if skip := profile.inspect(name, ownership); skip != "" {
				installer.skip(skip)
				continue
			}
			if profile.exists && !profile.owned {
				installer.ok("theme " + name + " profile copy exists at " + profile.path +
					" and is not installer-owned; apply adopts it only when byte-identical to the install," +
					" else skips the theme")
			}
			switch {
			case claude.owned && claude.exists:
				installer.ok("theme " + name + " currently installed; apply checks its profile for updates")
			case claude.exists:
				installer.ok("theme " + name + " exists at " + target +
					" and is not installer-owned; apply adopts it only when byte-identical to the install")
			default:
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
		files, filesErr := themeFiles(installer.options.Home, name, source.file, claude, content)
		if filesErr != nil {
			installer.skip("theme " + name + " " + filesErr.Error())
			continue
		}
		ownership = installer.installThemeFiles(name, ownershipPath, ownership, files)
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

	for _, key := range sortedThemeOwnershipNames(ownership) {
		ownership = installer.removeOwnedTheme(ownershipPath, ownership, key, themeUninstall)
	}
	if installer.apply {
		for _, directory := range []string{
			filepath.Join(installer.options.Home, ".claude", "themes"),
			themesync.ProfileDir(installer.options.Home),
		} {
			if removeErr := os.Remove(
				directory,
			); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) &&
				!errors.Is(removeErr, fs.ErrExist) {
				installer.skip("leave theme directory " + directory + ": " + removeErr.Error())
			}
		}
	}
}

// loadThemeContent returns a profile's bytes: read from the source clone
// ("read failed: ..." names the path) or downloaded from the release
// ("fetch failed: ..."); either way non-JSON is refused.
func loadThemeContent(ctx context.Context, client *http.Client, source themeProfile) ([]byte, error) {
	if source.local != "" {
		content, err := os.ReadFile(source.local)
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
		}
		if !json.Valid(content) {
			return nil, fmt.Errorf("read failed: %s is not valid JSON", source.local)
		}
		return content, nil
	}
	content, err := fetchTheme(ctx, client, source.raw)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	if !json.Valid(content) {
		return nil, errors.New("fetch failed: response is not valid JSON")
	}
	return content, nil
}

// ThemeReleaseURL is the release-matched URL of templates/themes/ for
// currentVersion ("main" for a development or empty version), ending in "/":
// install's Options.ThemeReleaseURL, where the profiles are downloaded from
// when no source clone holds them, and the {GH_USER} fallback the skill
// sources resolve their owner from (registeredOwner), as pfm doctor does.
func ThemeReleaseURL(currentVersion string) string {
	reference := strings.TrimSpace(currentVersion)
	if reference == "" || reference == pfmconfig.DevelopmentVersion {
		reference = "main"
	}
	return "https://raw.githubusercontent.com/" + updatecheck.ProfessorRepo + "/" + reference + "/" +
		themeProfilesRelative + "/"
}

// ownerPlaceholder is the registered {GH_USER} placeholder a skill source's
// repo may carry; resolveOwnerPlaceholder fills it.
const ownerPlaceholder = "{GH_USER}"

// resolveOwnerPlaceholder replaces the registered {GH_USER} placeholder in the
// global skill sources with the blueprint repo owner registeredOwner
// resolves. Content without the placeholder is returned untouched; a
// resolution failure is an error naming the placeholder, never a silently
// unresolved URL.
func resolveOwnerPlaceholder(content []byte, sourceRepo, releaseURL string) ([]byte, error) {
	if !bytes.Contains(content, []byte(ownerPlaceholder)) {
		return content, nil
	}
	owner, err := registeredOwner(sourceRepo, releaseURL)
	if err != nil {
		return nil, fmt.Errorf("resolve registered placeholder %s: %w", ownerPlaceholder, err)
	}
	return bytes.ReplaceAll(content, []byte(ownerPlaceholder), []byte(owner)), nil
}

// registeredOwner resolves {GH_USER}: the owner in the source clone's
// .professor/manifest.json installed_from.repo, else the owner segment of the
// release theme URL (ThemeReleaseURL).
func registeredOwner(sourceRepo, releaseURL string) (string, error) {
	if strings.TrimSpace(sourceRepo) != "" {
		manifestPath := filepath.Join(sourceRepo, ".professor", "manifest.json")
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
	parsed, err := url.Parse(strings.TrimSpace(releaseURL))
	if err != nil {
		return "", fmt.Errorf("parse release theme URL: %w", err)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 || strings.TrimSpace(segments[0]) == "" {
		return "", fmt.Errorf("release theme URL %q does not name an owner/repository", releaseURL)
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

// releaseUnpublishedAlpha reports whether a release theme URL names an
// -alpha version reference. ThemeReleaseURL builds this URL from VERSION, and
// pfm never publishes an -alpha tag on GitHub, so that URL 404s every time;
// releaseThemeProfiles turns that predictable failure into a named refusal
// instead of a bare HTTP error, and skips the doomed fetch entirely.
func releaseUnpublishedAlpha(rawURL string) bool {
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

// loadThemeProfiles finds the themes install ships, keyed by name: every
// profile in the source clone's templates/themes/, or, with no source clone
// or one without that directory, every profile there at the release.
func loadThemeProfiles(ctx context.Context, options Options) (map[string]themeProfile, error) {
	if strings.TrimSpace(options.SourceRepo) == "" {
		profiles, err := releaseThemeProfiles(ctx, options)
		if err != nil {
			return nil, fmt.Errorf("no source repository: %w", err)
		}
		return profiles, nil
	}
	directory := filepath.Join(options.SourceRepo, filepath.FromSlash(themeProfilesRelative))
	files, err := localProfileFiles(directory)
	if errors.Is(err, fs.ErrNotExist) {
		profiles, releaseErr := releaseThemeProfiles(ctx, options)
		if releaseErr != nil {
			return nil, fmt.Errorf("local themes unavailable: %v; %w", err, releaseErr)
		}
		return profiles, nil
	}
	if err != nil {
		return nil, err
	}
	profiles := make(map[string]themeProfile, len(files))
	for _, file := range files {
		name := strings.TrimSuffix(file, ".json")
		if err := validateThemeName(name); err != nil {
			return nil, fmt.Errorf("profile %s: %w", file, err)
		}
		profiles[name] = themeProfile{file: file, local: filepath.Join(directory, file)}
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no profile in %s: a theme is a *.json file there", directory)
	}
	return profiles, nil
}

// releaseThemeProfiles lists the profiles at Options.ThemeReleaseURL and
// names each one's download URL: refused, before any fetch, for an
// unpublished -alpha release or with the theme fetch switched off.
func releaseThemeProfiles(ctx context.Context, options Options) (map[string]themeProfile, error) {
	base := strings.TrimSpace(options.ThemeReleaseURL)
	if base == "" {
		return nil, errors.New("no release theme URL is configured")
	}
	if releaseUnpublishedAlpha(base) {
		return nil, errors.New(
			"release themes for an unpublished -alpha build; run pfm install from the source clone",
		)
	}
	if options.ThemesOffline {
		return nil, fmt.Errorf("fetch skipped: %s=1", paths.EnvThemesOffline)
	}
	files, err := releaseProfileFiles(ctx, options.ThemeHTTPClient, base)
	if err != nil {
		return nil, err
	}
	profiles := make(map[string]themeProfile, len(files))
	for _, file := range files {
		name := strings.TrimSuffix(file, ".json")
		if err := validateThemeName(name); err != nil {
			return nil, fmt.Errorf("profile %s: %w", file, err)
		}
		raw := strings.TrimSuffix(base, "/") + "/" + file
		if err := validateThemeURL(raw); err != nil {
			return nil, fmt.Errorf("profile %s release URL: %w", file, err)
		}
		profiles[name] = themeProfile{file: file, raw: raw}
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no profile in %s", base)
	}
	return profiles, nil
}

// isProfileFile reports whether a file in templates/themes/ is a profile:
// every JSON file there is one.
func isProfileFile(name string) bool {
	return strings.HasSuffix(name, ".json")
}

// localProfileFiles are the profiles in the source clone's templates/themes/,
// sorted: every regular *.json file. A profile is its file alone, so dropping
// one there is all adding it takes.
func localProfileFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list profiles in %s: %w", dir, err)
	}
	var files []string
	for _, entry := range entries {
		if !isProfileFile(entry.Name()) {
			continue
		}
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("profile %s in %s is not a regular file (%s)", entry.Name(), dir, entry.Type())
		}
		files = append(files, entry.Name())
	}
	return files, nil
}

// githubContentsFile is the contents API's type for a file entry, as against
// "dir", "symlink" or "submodule".
const githubContentsFile = "file"

// releaseProfileFiles lists the profiles at a release theme URL on
// raw.githubusercontent.com through GitHub's contents API, the one listing
// that host has: the files of templates/themes/ at the URL's ref.
func releaseProfileFiles(ctx context.Context, client *http.Client, releaseURL string) ([]string, error) {
	listing, err := themeListingURL(releaseURL)
	if err != nil {
		return nil, err
	}
	content, err := fetchTheme(ctx, client, listing)
	if err != nil {
		return nil, fmt.Errorf("list release profiles: %w", err)
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(content, &entries); err != nil {
		return nil, fmt.Errorf("list release profiles: decode %s: %w", listing, err)
	}
	var files []string
	for _, entry := range entries {
		if entry.Type == githubContentsFile && isProfileFile(entry.Name) {
			files = append(files, entry.Name)
		}
	}
	sort.Strings(files)
	return files, nil
}

// themeListingURL turns a release theme URL,
// https://raw.githubusercontent.com/{owner}/{repo}/{ref}/templates/themes/,
// into the contents API URL listing that directory at that ref; any other
// shape is an error naming it.
func themeListingURL(releaseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(releaseURL))
	if err != nil {
		return "", fmt.Errorf("list release profiles: parse %s: %w", releaseURL, err)
	}
	suffix := strings.Split(themeProfilesRelative, "/")
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if parsed.Host != "raw.githubusercontent.com" || len(segments) < 3+len(suffix) ||
		strings.Join(segments[len(segments)-len(suffix):], "/") != themeProfilesRelative {
		return "", fmt.Errorf(
			"list release profiles: %s is not https://raw.githubusercontent.com/{owner}/{repo}/{ref}/%s/",
			releaseURL, themeProfilesRelative,
		)
	}
	ref := strings.Join(segments[2:len(segments)-len(suffix)], "/")
	return "https://api.github.com/repos/" + segments[0] + "/" + segments[1] + "/contents/" +
		themeProfilesRelative + "?ref=" + url.QueryEscape(ref), nil
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
	if parsed.Scheme == httpProtocol && (host == "127.0.0.1" || host == "::1" || host == "localhost") {
		return nil
	}
	return fmt.Errorf("must be HTTPS (HTTP is accepted only for loopback tests)")
}

// fetchTheme is the installer's http.out door (spec § Middleware): the client
// it builds or is handed is wrapped, so every theme fetch leaves one record.
func fetchTheme(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	if client == nil {
		client = obs.WrapClient(&http.Client{Timeout: 30 * time.Second})
	}
	request, err := http.NewRequestWithContext(obs.Presence(ctx), http.MethodGet, raw, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create GET %s: %w", raw, err)
	}
	response, err := obs.WrapClient(client).Do(request)
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

func sortedThemeNames(profiles map[string]themeProfile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
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
