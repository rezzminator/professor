package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckPersistsLatestReleaseForTheNextInvocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead {
			t.Fatalf("request method = %s, want HEAD", request.Method)
		}
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.61.2")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", server.URL, server.Client()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	notice, found, err := Read(cache, "v0.61.1")
	if err != nil || !found || notice.Latest != "v0.61.2" || notice.Current != "v0.61.1" {
		t.Fatalf("Read(old) notice=%#v found=%t err=%v", notice, found, err)
	}
	if _, found, err := Read(cache, "v0.61.2"); err != nil || found {
		t.Fatalf("Read(current) found=%t err=%v, want no update row", found, err)
	}
}

func TestLocalHotfixVersionStillDiscoversNewRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.61.5")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	const current = "v0.61.3-local.2"
	if err := CheckForUpdate(context.Background(), cache, current, server.URL, server.Client()); err != nil {
		t.Fatalf("Check(local hotfix) error = %v", err)
	}
	notice, found, err := Read(cache, current)
	if err != nil || !found || notice.Latest != "v0.61.5" || notice.Current != current {
		t.Fatalf("Read(local hotfix) notice=%#v found=%t err=%v", notice, found, err)
	}
}

func TestReadOffersReleaseToItsOwnPrerelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.78.0")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	const current = "0.78.0-alpha"
	if err := CheckForUpdate(context.Background(), cache, current, server.URL, server.Client()); err != nil {
		t.Fatalf("Check(prerelease) error = %v", err)
	}
	notice, found, err := Read(cache, current)
	if err != nil || !found || notice.Latest != "v0.78.0" {
		t.Fatalf(
			"Read(prerelease current vs its own release) notice=%#v found=%t err=%v, want offered",
			notice,
			found,
			err,
		)
	}
}

func TestReadDoesNotOfferAnOlderReleaseToAPrerelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.77.0")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	const current = "0.78.0-alpha"
	if err := CheckForUpdate(context.Background(), cache, current, server.URL, server.Client()); err != nil {
		t.Fatalf("Check(prerelease vs older release) error = %v", err)
	}
	if notice, found, err := Read(cache, current); err != nil || found {
		t.Fatalf("Read(prerelease vs older release) notice=%#v found=%t err=%v, want no update row", notice, found, err)
	}
}

func TestReadDoesNotOfferAReleaseToItsOwnBuildMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.78.0")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	const current = "0.78.0+build.5"
	if err := CheckForUpdate(context.Background(), cache, current, server.URL, server.Client()); err != nil {
		t.Fatalf("Check(build metadata current) error = %v", err)
	}
	if notice, found, err := Read(cache, current); err != nil || found {
		t.Fatalf(
			"Read(build metadata current vs same-core release) notice=%#v found=%t err=%v, want no update row (build metadata is not a pre-release)",
			notice,
			found,
			err,
		)
	}
}

func TestCheckFollowsOneGitHubOwnerRenameHopToTheTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/mreza0100/professor/releases/latest":
			writer.Header().Set("Location", "http://"+request.Host+"/rezzminator/professor/releases/latest")
			writer.WriteHeader(http.StatusMovedPermanently)
		case "/rezzminator/professor/releases/latest":
			writer.Header().Set("Location", "/rezzminator/professor/releases/tag/v0.76.0")
			writer.WriteHeader(http.StatusFound)
		default:
			t.Fatalf("unexpected request path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	latestURL := server.URL + "/mreza0100/professor/releases/latest"
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", latestURL, server.Client()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	notice, found, err := Read(cache, "v0.61.1")
	if err != nil || !found || notice.Latest != "v0.76.0" {
		t.Fatalf("Read(rename hop) notice=%#v found=%t err=%v", notice, found, err)
	}
	if !strings.HasSuffix(notice.ReleaseURL, "/rezzminator/professor/releases/tag/v0.76.0") {
		t.Fatalf("ReleaseURL = %q, want suffix /rezzminator/professor/releases/tag/v0.76.0", notice.ReleaseURL)
	}
}

func TestCheckRejectsASecondRenameHop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/mreza0100/professor/releases/latest":
			writer.Header().Set("Location", "/rezzminator/professor/releases/latest")
			writer.WriteHeader(http.StatusMovedPermanently)
		case "/rezzminator/professor/releases/latest":
			writer.Header().Set("Location", "/third/professor/releases/latest")
			writer.WriteHeader(http.StatusMovedPermanently)
		default:
			t.Fatalf("unexpected request path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	latestURL := server.URL + "/mreza0100/professor/releases/latest"
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", latestURL, server.Client()); err == nil {
		t.Fatal("Check() with two rename hops returned nil error, want an error")
	}
}

func TestCheckRejectsACrossHostRenameHop(t *testing.T) {
	otherHostHit := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		otherHostHit = true
	}))
	defer other.Close()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/mreza0100/professor/releases/latest":
			writer.Header().Set("Location", "https://evil.example/rezzminator/professor/releases/latest")
			writer.WriteHeader(http.StatusMovedPermanently)
		default:
			t.Fatalf("unexpected request path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	latestURL := server.URL + "/mreza0100/professor/releases/latest"
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", latestURL, server.Client()); err == nil {
		t.Fatal("Check() with cross-host redirect returned nil error, want an error")
	}
	if otherHostHit {
		t.Fatal("Check() made a request to an unrelated host")
	}
}

func TestCheckRejectsARenameHopToADifferentRepo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/mreza0100/professor/releases/latest":
			writer.Header().Set("Location", "/rezzminator/other/releases/latest")
			writer.WriteHeader(http.StatusMovedPermanently)
		default:
			t.Fatalf("unexpected request path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	latestURL := server.URL + "/mreza0100/professor/releases/latest"
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", latestURL, server.Client()); err == nil {
		t.Fatal("Check() with different-repo redirect returned nil error, want an error")
	}
}

func TestFailedRefreshPreservesLastSuccessfulNotice(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.61.2")
		writer.WriteHeader(http.StatusFound)
	}))
	cache := filepath.Join(t.TempDir(), "update.json")
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", good.URL, good.Client()); err != nil {
		t.Fatal(err)
	}
	notice, found, err := Read(cache, "v0.61.1")
	if err != nil || !found {
		t.Fatalf("read primed cache: notice=%#v found=%t err=%v", notice, found, err)
	}
	notice.CheckedAt = time.Now().Add(-checkFreshFor - time.Minute)
	if err := writeNotice(cache, notice); err != nil {
		t.Fatalf("age primed cache: %v", err)
	}
	good.Close()

	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", failing.URL, failing.Client()); err == nil {
		t.Fatal("failed refresh returned nil")
	}
	if notice, found, err := Read(cache, "v0.61.1"); err != nil || !found || notice.Latest != "v0.61.2" {
		t.Fatalf("last success was lost: notice=%#v found=%t err=%v", notice, found, err)
	}
}

// TestFailedCheckWritesADurableFailureMarker (Lane-2 §5): a check that
// cannot reach the network must leave a durable trace beside the cache — Read
// alone answers found=false identically for "no update" and "the checker has
// been failing for a week", and a picker that trusts Read alone reads a
// permanently broken checker as "up to date" forever.
func TestFailedCheckWritesADurableFailureMarker(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()

	cache := filepath.Join(t.TempDir(), "update.json")
	before := time.Now()
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", failing.URL, failing.Client()); err == nil {
		t.Fatal("CheckForUpdate() against a failing server returned nil error")
	}
	marker, found, err := ReadFailure(cache)
	if err != nil {
		t.Fatalf("ReadFailure() error = %v", err)
	}
	if !found {
		t.Fatal("ReadFailure() found nothing after a failed check")
	}
	if marker.Class != failureNetwork {
		t.Fatalf("marker.Class = %q, want %q", marker.Class, failureNetwork)
	}
	if marker.Reason == "" {
		t.Fatal("marker.Reason is empty, want the cause named")
	}
	if marker.At.Before(before.Add(-time.Second)) {
		t.Fatalf("marker.At = %s, want it stamped around %s", marker.At, before)
	}
}

// TestSuccessfulCheckClearsAPriorFailureMarker: the marker exists only to
// name an ONGOING failure, so a check that recovers must remove it — a
// picker warning that never clears is as dishonest as one that never fires.
func TestSuccessfulCheckClearsAPriorFailureMarker(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	cache := filepath.Join(t.TempDir(), "update.json")
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", failing.URL, failing.Client()); err == nil {
		t.Fatal("CheckForUpdate() against a failing server returned nil error")
	}
	failing.Close()
	if _, found, err := ReadFailure(cache); err != nil || !found {
		t.Fatalf("ReadFailure() before recovery: found=%t err=%v, want a marker", found, err)
	}

	good := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.61.2")
		writer.WriteHeader(http.StatusFound)
	}))
	defer good.Close()
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", good.URL, good.Client()); err != nil {
		t.Fatalf("CheckForUpdate() after recovery: %v", err)
	}
	if _, found, err := ReadFailure(cache); err != nil || found {
		t.Fatalf("ReadFailure() after recovery: found=%t err=%v, want the marker cleared", found, err)
	}
}

func TestRecentSuccessfulCheckSuppressesRedundantNetworkLookup(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits++
		writer.Header().Set("Location", "/example/project/releases/tag/v0.61.5")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	cache := filepath.Join(t.TempDir(), "update.json")
	if err := CheckForUpdate(context.Background(), cache, "v0.61.4", server.URL, server.Client()); err != nil {
		t.Fatal(err)
	}
	if err := CheckForUpdate(context.Background(), cache, "v0.61.4", server.URL, server.Client()); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("two immediate checks made %d network requests, want one", hits)
	}
	notice, _, err := Read(cache, "v0.61.4")
	if err != nil || time.Since(notice.CheckedAt) > time.Minute {
		t.Fatalf("cached successful check is not recent: notice=%#v err=%v", notice, err)
	}
}
