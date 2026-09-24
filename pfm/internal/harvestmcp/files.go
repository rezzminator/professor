package harvestmcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	// filesPath is the gateway route a signed download link names. It sits
	// outside the bearer check: the signature is its access control.
	filesPath = "/files/"
	// downloadLinkTTL is how long a signed download link stays fetchable.
	downloadLinkTTL = 10 * time.Minute
)

// downloadIDPattern is the only shape a /files id may take — a sha256 the
// download store is keyed by — so no path, dot or separator ever reaches it.
var downloadIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// downloadLinks signs the remote gateway's download URLs with a key that
// lives only in this process: never written, never logged, so a restart
// invalidates every link it issued.
type downloadLinks struct {
	base  string
	key   []byte
	clock clock.Clock
}

func newDownloadLinks(publicURL string, now clock.Clock) (*downloadLinks, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate the download link key: %w", err)
	}
	return &downloadLinks{base: publicURL, key: key, clock: now}, nil
}

func (links *downloadLinks) signature(id string, exp int64) []byte {
	mac := hmac.New(sha256.New, links.key)
	mac.Write([]byte(id + "." + strconv.FormatInt(exp, 10)))
	return mac.Sum(nil)
}

// link returns the signed URL for id and the moment it stops working.
func (links *downloadLinks) link(id string) (string, time.Time) {
	expires := links.clock.Now().Add(downloadLinkTTL).UTC().Truncate(time.Second)
	exp := expires.Unix()
	return fmt.Sprintf("%s%s%s?exp=%d&sig=%s", links.base, filesPath, id, exp,
		hex.EncodeToString(links.signature(id, exp))), expires
}

// check verifies a link's signature (constant time), then its expiry. It
// answers 0 for a live link, else the status and its named cause.
func (links *downloadLinks) check(id, exp, sig string) (int, string) {
	expUnix, expErr := strconv.ParseInt(exp, 10, 64)
	given, sigErr := hex.DecodeString(sig)
	if expErr != nil || sigErr != nil || len(given) == 0 || !hmac.Equal(given, links.signature(id, expUnix)) {
		return http.StatusForbidden, "the link's signature is missing or invalid; call download again for a fresh link"
	}
	if !links.clock.Now().Before(time.Unix(expUnix, 0)) {
		return http.StatusGone, "the link expired; call download again for a fresh link"
	}
	return 0, ""
}

// serveFile answers GET|HEAD /files/{sha256}?exp=&sig=: one file the
// download store holds, streamed, never a listing and never another path.
func (r *RemoteServer) serveFile(w http.ResponseWriter, req *http.Request) {
	id := strings.TrimPrefix(req.URL.Path, filesPath)
	recorder := &fileStatusWriter{ResponseWriter: w}
	r.writeFile(recorder, req, id)
	logged := "invalid"
	if downloadIDPattern.MatchString(id) {
		logged = id[:12]
	}
	obs.Logger(obs.Component(req.Context(), "mcp")).Info("harvester.files.request",
		"id", logged, "status", recorder.status, "bytes", recorder.bytes)
}

func (r *RemoteServer) writeFile(w http.ResponseWriter, req *http.Request, id string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "only GET and HEAD are served here", http.StatusMethodNotAllowed)
		return
	}
	if !downloadIDPattern.MatchString(id) {
		http.Error(w, "no such file: a download id is a 64-character lowercase sha256", http.StatusNotFound)
		return
	}
	query := req.URL.Query()
	if status, reason := r.service.links.check(id, query.Get("exp"), query.Get("sig")); status != 0 {
		http.Error(w, reason, status)
		return
	}
	file, held := r.service.downloads.get(id)
	if !held {
		http.Error(w, "no such file: this server holds no download with that id", http.StatusNotFound)
		return
	}
	opened, err := os.Open(file.path)
	if err != nil {
		obs.Logger(obs.Component(req.Context(), "mcp")).Warn("harvester.files.open", obs.FieldErr, err.Error())
		http.Error(w, "the stored file could not be read; call download again", http.StatusInternalServerError)
		return
	}
	defer func() {
		if closeErr := opened.Close(); closeErr != nil {
			obs.Logger(obs.Component(req.Context(), "mcp")).
				Warn("harvester.files.close", obs.FieldErr, closeErr.Error())
		}
	}()
	info, err := opened.Stat()
	if err != nil {
		obs.Logger(obs.Component(req.Context(), "mcp")).Warn("harvester.files.stat", obs.FieldErr, err.Error())
		http.Error(w, "the stored file could not be read; call download again", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", file.mime)
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeFileName(file.name, id)+`"`)
	http.ServeContent(w, req, "", info.ModTime(), opened)
}

// safeFileName keeps a Content-Disposition filename to letters, digits and
// ._- so no quote, separator or control byte reaches the header.
func safeFileName(name, id string) string {
	cleaned := strings.Trim(strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			return c
		}
		return '_'
	}, name), ".")
	if cleaned == "" {
		return id
	}
	if len(cleaned) > 200 {
		cleaned = cleaned[:200]
	}
	return cleaned
}

// fileStatusWriter remembers the status and body bytes /files wrote, for
// its request log line.
type fileStatusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (writer *fileStatusWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *fileStatusWriter) Write(chunk []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	written, err := writer.ResponseWriter.Write(chunk)
	writer.bytes += int64(written)
	return written, err
}
