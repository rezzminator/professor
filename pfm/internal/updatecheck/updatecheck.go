// Package updatecheck maintains the silent, next-invocation Professor release
// notice consumed by the interactive fleet picker.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	lockStaleAfter = 2 * time.Minute
	checkFreshFor  = 6 * time.Hour

	// ProfessorRepo is the "<owner>/<repo>" GitHub slug every hardcoded
	// Professor URL is derived from. A repository rename is this one line.
	ProfessorRepo = "rezzminator/professor"
)

// Notice is one successful release lookup. Current is rewritten to the
// invoking binary's version when Read returns it; the cached value is retained
// only as useful diagnostic provenance.
type Notice struct {
	Current    string    `json:"current"`
	Latest     string    `json:"latest"`
	ReleaseURL string    `json:"release_url"`
	CheckedAt  time.Time `json:"checked_at"`
}

// failureClass is a short, stable, machine-readable reason a check failed —
// never prose, so a marker's class can be switched on without parsing an
// error string whose wording is free to change between versions.
type failureClass string

const (
	failureLock    failureClass = "lock"
	failureCache   failureClass = "cache"
	failureNetwork failureClass = "network"
)

// FailureMarker is the durable "last check failed" record written beside the
// cache whenever a check does not end in a fresh success. Read alone cannot
// tell a permanently failing checker from a machine that genuinely has no
// update — both answer found=false — so a caller that wants to warn about a
// checker that has been failing needs this file too. Cleared the next time a
// check actually succeeds (including a "still fresh" short-circuit).
type FailureMarker struct {
	At     time.Time    `json:"at"`
	Class  failureClass `json:"class"`
	Reason string       `json:"reason"`
}

type semanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease bool
}

// Read returns a notice only when the last successful lookup found a release
// newer than the binary asking. Missing cache state is an ordinary first run;
// malformed state is named to callers, which may deliberately keep the picker
// silent and let the detached checker repair it.
func Read(path, current string) (Notice, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Notice{}, false, nil
	}
	if err != nil {
		return Notice{}, false, fmt.Errorf("read update cache: %w", err)
	}
	var notice Notice
	if err := json.Unmarshal(raw, &notice); err != nil {
		return Notice{}, false, fmt.Errorf("decode update cache: %w", err)
	}
	installed, installedOK := parseNoticeVersion(current)
	latest, latestOK := parseReleaseVersion(notice.Latest)
	if !installedOK || !latestOK || !newer(latest, installed) {
		return Notice{}, false, nil
	}
	notice.Current = normalizeVersion(current)
	return notice, true, nil
}

// Check performs one bounded latest-release lookup and atomically replaces the
// cache only after a complete, valid response. A failed lookup leaves the last
// successful notice intact, so temporary network failures cannot make an
// already-known update disappear.
func CheckForUpdate(ctx context.Context, path, current, latestURL string, client *http.Client) error {
	return CheckForUpdateWithClock(ctx, path, current, latestURL, client, clock.Real)
}

// CheckForUpdateWithClock is CheckForUpdate with the repository clock injected
// for callers that must make lock expiry and cache freshness deterministic.
//
// Every path that does not end in a confirmed-fresh cache writes a durable
// FailureMarker beside path first; every path that does clears it. A checker
// that has been failing for days must never look identical, through Read
// alone, to a machine that genuinely has no update — see ReadFailure.
func CheckForUpdateWithClock(
	ctx context.Context,
	path, current, latestURL string,
	client *http.Client,
	clk clock.Clock,
) error {
	if clk == nil {
		clk = clock.Real
	}
	if _, ok := parseNoticeVersion(current); !ok {
		return fmt.Errorf("current version %q is not vMAJOR.MINOR.PATCH[-prerelease]", current)
	}
	release, err := acquire(path+".lock", clk.Now())
	if err != nil {
		return recordFailure(path, clk.Now().UTC(), failureLock, err)
	}
	if release == nil {
		// Another invocation already holds the lock: THIS call performed no
		// check of its own, so the durable marker is left exactly as it was
		// — neither written nor cleared on its behalf.
		return nil
	}
	defer release()

	now := clk.Now().UTC()
	class, checkErr := performCheck(ctx, path, current, latestURL, client, now)
	if checkErr != nil {
		return recordFailure(path, now, class, checkErr)
	}
	if err := clearFailure(path); err != nil {
		return err
	}
	return nil
}

// performCheck is CheckForUpdateWithClock's body once the lock is held: the
// freshness short-circuit, the network lookup, and the cache write. Its
// failureClass return is "" on success — recordFailure is never called with
// a nil error, so the class is never read in that case.
func performCheck(
	ctx context.Context,
	path, current, latestURL string,
	client *http.Client,
	now time.Time,
) (failureClass, error) {
	recent, err := checkedRecently(path, current, now)
	if err != nil {
		return failureCache, err
	}
	if recent {
		return "", nil
	}
	if client == nil {
		client = obs.WrapClient(&http.Client{Timeout: 12 * time.Second})
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, latestURL, http.NoBody)
	if err != nil {
		return failureNetwork, fmt.Errorf("build latest-release request: %w", err)
	}
	request.Header.Set("User-Agent", "pfm-update-check/"+normalizeVersion(current))
	noFollow := *obs.WrapClient(client)
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resolved, location, err := follow(&noFollow, request)
	if err != nil {
		return failureNetwork, err
	}
	if hop := renameHopURL(request.URL, resolved); hop != "" {
		hopRequest, err := http.NewRequestWithContext(ctx, http.MethodHead, hop, http.NoBody)
		if err != nil {
			return failureNetwork, fmt.Errorf("build renamed Professor release request: %w", err)
		}
		hopRequest.Header.Set("User-Agent", request.Header.Get("User-Agent"))
		resolved, location, err = follow(&noFollow, hopRequest)
		if err != nil {
			return failureNetwork, err
		}
		if second := renameHopURL(hopRequest.URL, resolved); second != "" {
			return failureNetwork, fmt.Errorf("renamed Professor release redirect %q renamed again to %q", hop, second)
		}
	}
	latest := pathVersion(resolved)
	if _, ok := parseReleaseVersion(latest); !ok {
		return failureNetwork, fmt.Errorf(
			"latest Professor release redirect %q has no vMAJOR.MINOR.PATCH tag",
			location,
		)
	}
	notice := Notice{
		Current:    normalizeVersion(current),
		Latest:     latest,
		ReleaseURL: resolved.String(),
		CheckedAt:  now,
	}
	if err := writeNotice(path, notice); err != nil {
		return failureCache, fmt.Errorf("write update cache: %w", err)
	}
	return "", nil
}

// recordFailure writes the durable failure marker beside path and returns
// the ORIGINAL cause — a marker write that itself fails is joined in, never
// allowed to swallow the check failure it exists to report.
func recordFailure(path string, at time.Time, class failureClass, cause error) error {
	if markErr := writeFailure(path, FailureMarker{At: at, Class: class, Reason: cause.Error()}); markErr != nil {
		return errors.Join(cause, fmt.Errorf("record update check failure: %w", markErr))
	}
	return cause
}

// failurePath is where the durable failure marker lives, beside the cache
// itself and its own ".lock" file.
func failurePath(path string) string { return path + ".failure" }

func writeFailure(path string, marker FailureMarker) error {
	encoded, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode update check failure marker: %w", err)
	}
	return atomicfile.Write(failurePath(path), append(encoded, '\n'), 0o600)
}

// clearFailure removes the failure marker on a confirmed success. A marker
// that was never there is not an error — the ordinary state after the very
// first successful check.
func clearFailure(path string) error {
	if err := os.Remove(failurePath(path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear update check failure marker: %w", err)
	}
	return nil
}

// ReadFailure reports the durable "last check failed" marker beside path, if
// one exists. Absent is an ordinary state (no failure since the last
// success, or the checker has never run) — but a marker that exists and
// cannot be read or decoded is reported as an error, never folded into
// absence, the same rule Read applies to the cache itself.
func ReadFailure(path string) (FailureMarker, bool, error) {
	raw, err := os.ReadFile(failurePath(path))
	if errors.Is(err, fs.ErrNotExist) {
		return FailureMarker{}, false, nil
	}
	if err != nil {
		return FailureMarker{}, false, fmt.Errorf("read update check failure marker: %w", err)
	}
	var marker FailureMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return FailureMarker{}, false, fmt.Errorf("decode update check failure marker: %w", err)
	}
	return marker, true, nil
}

// checkedRecently recognizes only a complete, successful notice. Malformed
// cache state is stale rather than fatal here so Check can repair it with a
// fresh lookup; Read still reports the malformed state to its callers.
func checkedRecently(path, current string, now time.Time) (bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read update cache freshness: %w", err)
	}
	var notice Notice
	if err := json.Unmarshal(raw, &notice); err != nil {
		return false, nil
	}
	_, latestOK := parseReleaseVersion(notice.Latest)
	if notice.CheckedAt.IsZero() ||
		notice.Current != normalizeVersion(current) ||
		!latestOK ||
		strings.TrimSpace(notice.ReleaseURL) == "" {
		return false, nil
	}
	age := now.Sub(notice.CheckedAt)
	return age >= 0 && age <= checkFreshFor, nil
}

func acquire(path string, now time.Time) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create update cache directory: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("close update lock: %w", closeErr)
			}
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create update lock: %w", err)
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("inspect update lock: %w", statErr)
		}
		if now.Sub(info.ModTime()) <= lockStaleAfter {
			return nil, nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("remove stale update lock: %w", err)
		}
	}
	return nil, nil
}

func writeNotice(path string, notice Notice) error {
	encoded, err := json.MarshalIndent(notice, "", "  ")
	if err != nil {
		return fmt.Errorf("encode update notice: %w", err)
	}
	return atomicfile.Write(path, append(encoded, '\n'), 0o600)
}

// follow issues one HEAD request and returns its redirect target, both parsed
// and as the raw Location header (kept for error messages). A non-3xx status
// or a missing/unparsable Location is an error, never a silent "no update".
func follow(
	client *http.Client,
	request *http.Request,
) (resolved *url.URL, location string, returnErr error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("request latest Professor release: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close latest release response: %w", err))
		}
	}()
	if response.StatusCode < 300 || response.StatusCode >= 400 {
		return nil, "", fmt.Errorf("latest Professor release returned %s", response.Status)
	}
	location = strings.TrimSpace(response.Header.Get("Location"))
	if location == "" {
		return nil, "", errors.New("latest Professor release redirect omitted Location")
	}
	resolved, err = request.URL.Parse(location)
	if err != nil {
		return nil, "", fmt.Errorf("parse latest Professor release redirect: %w", err)
	}
	return resolved, location, nil
}

// renameHopURL recognizes exactly the GitHub repository-rename redirect
// shape: same scheme+host as the request just made, same repo, a different
// owner, landing on the sibling "releases/latest" (not yet a tag). It
// returns "" for anything else — a cross-host Location, a different repo, or
// a Location that already names a tag — leaving that response to the
// existing tag rule in Check.
func renameHopURL(original, resolved *url.URL) string {
	originalOwner, originalRepo, ok := releasesLatestOwnerRepo(original)
	if !ok {
		return ""
	}
	if resolved.Scheme != original.Scheme || resolved.Host != original.Host {
		return ""
	}
	resolvedOwner, resolvedRepo, ok := releasesLatestOwnerRepo(resolved)
	if !ok {
		return ""
	}
	if resolvedRepo != originalRepo || resolvedOwner == originalOwner {
		return ""
	}
	return resolved.String()
}

// releasesLatestOwnerRepo reports the owner/repo of a /<owner>/<repo>/releases/latest
// path, and false for anything not shaped exactly like one.
func releasesLatestOwnerRepo(location *url.URL) (owner, repo string, ok bool) {
	parts := strings.Split(strings.Trim(location.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "releases" || parts[3] != "latest" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func pathVersion(location *url.URL) string {
	parts := strings.Split(strings.Trim(location.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return normalizeVersion(parts[len(parts)-1])
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	if value != "" && value[0] != 'v' {
		value = "v" + value
	}
	return value
}

func parseNoticeVersion(value string) (semanticVersion, bool) {
	value = strings.TrimPrefix(normalizeVersion(value), "v")
	var prerelease bool
	if separator := strings.IndexAny(value, "-+"); separator >= 0 {
		if separator == 0 || separator == len(value)-1 {
			return semanticVersion{}, false
		}
		// A "-" suffix is a pre-release (SemVer §9); a "+" suffix alone is
		// build metadata (§10) and carries no ordering weight of its own, so
		// only a "-" that appears before any "+" counts.
		prerelease = value[separator] == '-'
		value = value[:separator]
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return semanticVersion{}, false
	}
	numbers := make([]int, 3)
	for index, part := range parts {
		if part == "" {
			return semanticVersion{}, false
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || strconv.Itoa(number) != part {
			return semanticVersion{}, false
		}
		numbers[index] = number
	}
	return semanticVersion{major: numbers[0], minor: numbers[1], patch: numbers[2], prerelease: prerelease}, true
}

func parseReleaseVersion(value string) (semanticVersion, bool) {
	normalized := strings.TrimPrefix(normalizeVersion(value), "v")
	if strings.ContainsAny(normalized, "-+") {
		return semanticVersion{}, false
	}
	return parseNoticeVersion(normalized)
}

func newer(candidate, current semanticVersion) bool {
	if candidate.major != current.major {
		return candidate.major > current.major
	}
	if candidate.minor != current.minor {
		return candidate.minor > current.minor
	}
	if candidate.patch != current.patch {
		return candidate.patch > current.patch
	}
	// Same core version: a release beats its own pre-release (SemVer §11).
	// Two pre-releases of one core are never newer than each other here —
	// candidate is always a published tag (parseReleaseVersion rejects any
	// suffix), so no identifier ordering is needed.
	return !candidate.prerelease && current.prerelease
}
