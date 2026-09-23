package harvest

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func fileURLPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "file") {
		return "", fmt.Errorf("invalid file URL")
	}
	if u.Host != "" && !strings.EqualFold(u.Host, localhostName) {
		return "", fmt.Errorf("file URL host %q is not local", u.Host)
	}
	path, err := url.PathUnescape(u.EscapedPath())
	if err != nil || path == "" {
		return "", fmt.Errorf("invalid file URL path")
	}
	return path, nil
}

var (
	systemRoots = []string{"/proc", "/sys", "/dev", "/etc"}
	denyDirs    = map[string]bool{
		".ssh":            true,
		".gnupg":          true,
		".aws":            true,
		".password-store": true,
		".docker":         true,
		".config":         true,
		".kube":           true,
	}
	denyNames = map[string]bool{
		"id_rsa":           true,
		"id_ed25519":       true,
		"id_dsa":           true,
		"id_ecdsa":         true,
		"credentials":      true,
		".netrc":           true,
		".pgpass":          true,
		".htpasswd":        true,
		"shadow":           true,
		"master.key":       true,
		"passwd":           true,
		".git-credentials": true,
		".bash_history":    true,
	}
	denySuffixes = []string{
		".pem",
		".key",
		".p12",
		".pfx",
		".keystore",
		".jks",
		".asc",
		".gpg",
		".kdbx",
		".ppk",
		".env",
	}
)

// DenyLocalPath returns a human-readable refusal reason, or an empty string
// when the canonical path is safe. roots is a confinement list; an empty list
// means unconfined (a local caller owns the machine). A NON-empty list whose
// roots all fail to resolve refuses everything — confinement fails closed,
// never open.
func DenyLocalPath(path string, roots []string) string {
	rp, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "refusing to read an unresolvable path"
	}
	rp, err = filepath.Abs(rp)
	if err != nil {
		return "refusing to read an unresolvable path"
	}
	rp = filepath.Clean(rp)
	resolvedRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		if rr, e := filepath.EvalSymlinks(root); e == nil {
			if a, e := filepath.Abs(rr); e == nil {
				resolvedRoots = append(resolvedRoots, filepath.Clean(a))
			}
		} else {
			log.Printf("harvest: ignoring unresolvable local root %q: %v", root, e)
		}
	}
	if len(roots) > 0 && len(resolvedRoots) == 0 {
		return "refusing to read: none of this server's permitted directories resolves"
	}
	if len(resolvedRoots) > 0 && !insideAny(rp, resolvedRoots) {
		return "refusing to read outside this server's permitted directory"
	}
	for _, root := range systemRoots {
		if rp == root || strings.HasPrefix(rp, root+string(os.PathSeparator)) {
			return "refusing to read inside a system directory (" + root + ")"
		}
	}
	for _, part := range strings.Split(filepath.ToSlash(rp), "/") {
		if denyDirs[strings.ToLower(part)] {
			return "refusing to read inside a sensitive directory (" + part + ")"
		}
	}
	low := strings.ToLower(filepath.Base(rp))
	if denyNames[low] {
		return "refusing to read a sensitive file (" + low + ")"
	}
	for _, suffix := range denySuffixes {
		if strings.HasSuffix(low, suffix) {
			return "refusing to read a credential/secret file (" + low + ")"
		}
	}
	if low == ".env" || strings.HasPrefix(low, ".env.") {
		return "refusing to read an environment/secret file (" + low + ")"
	}
	for _, marker := range []string{"_rsa", "_ed25519", "_dsa", "_ecdsa"} {
		if strings.Contains(low, marker) {
			return "refusing to read a private key (" + low + ")"
		}
	}
	return ""
}

func insideAny(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func (h *Harvester) fetchLocal(ctx context.Context, source string, options FetchOptions) Result {
	path := source
	if strings.HasPrefix(strings.ToLower(path), "file://") {
		decoded, err := fileURLPath(path)
		if err != nil {
			return Result{Source: source, Error: err.Error()}
		}
		path = decoded
	}
	if reason := DenyLocalPath(path, h.options.LocalRoots); reason != "" {
		return Result{Source: source, Error: reason}
	}
	body, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Result{Source: source, Error: fmt.Sprintf("read local file %s: %v", path, err)}
	}
	if len(body) == 0 {
		return Result{Source: source, Error: localEmptyFileText, ErrorKind: errorKindEmpty}
	}
	kind := classifyFetchedKind(path, "", body)
	if unsupported, ok := unsupportedLocalFormat(path, body); ok {
		unsupported.Source = source
		return unsupported
	}
	if kindFromName(path) == kindPDF && !strings.HasPrefix(string(body), "%PDF-") {
		return Result{
			Source: source,
			Kind:   kindPDF,
			Error: fmt.Sprintf(
				"%s has a .pdf extension but is not a PDF file — check its actual contents before fetching it again.",
				source,
			),
		}
	}
	if !options.Refresh {
		if cached, meta, cachePath, ok := h.cache.load(source, kind); ok {
			return h.resultFromCache(source, kind, cached, meta, cachePath)
		}
	}
	converted, err := h.convertFetchedContent(ctx, kind, source, body)
	if err != nil {
		return Result{Source: source, Kind: kind, Error: err.Error()}
	}
	if !usableContent(converted, kind) {
		return Result{Source: source, Kind: kind, Error: "conversion produced no usable content"}
	}
	return h.storeResult(source, kind, localLabel, converted, int64(len(body)), 0, []string{localLabel}, options)
}
