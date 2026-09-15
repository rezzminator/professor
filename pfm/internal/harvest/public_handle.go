package harvest

// This file is the protocol boundary for results that leave the harvester
// process.  The transport and cache deliberately retain their provenance for
// diagnostics; this layer turns that private receipt into a separate,
// provenance-free artifact before an adapter can return it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// PublicCandidates strips provider labels and replaces every location URL
// with a persistent opaque handle. Exact scholarly identifiers remain useful
// identity handles and are never inferred from arbitrary PDF URLs.
func (h *Harvester) PublicCandidates(candidates []Candidate) ([]Candidate, error) {
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		publicCandidate := candidate
		publicCandidate.Source = ""
		publicCandidate.Priority = 0
		if strings.TrimSpace(candidate.URL) != "" {
			if publicIdentityHandle(candidate.URL) {
				publicCandidate.URL = strings.TrimSpace(candidate.URL)
			} else {
				handle, err := h.PublicHandle(candidate.URL)
				if err != nil {
					return nil, err
				}
				publicCandidate.URL = handle
			}
		}
		out = append(out, publicCandidate)
	}
	return out, nil
}

func publicIdentityHandle(source string) bool {
	s := strings.TrimSpace(source)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	if publicBareDOI.MatchString(s) {
		return true
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "doi:") {
		return publicBareDOI.MatchString(strings.TrimSpace(s[4:]))
	}
	if publicPMID.MatchString(low) || publicPMCID.MatchString(low) {
		return true
	}
	if publicISBN.MatchString(s) {
		value := s
		if strings.HasPrefix(low, "isbn:") {
			value = strings.TrimSpace(s[5:])
		}
		return NormalizeISBN(value) != ""
	}
	parsed, err := url.Parse(s)
	if err == nil && (parsed.Scheme == schemeHTTP || parsed.Scheme == schemeHTTPS) && parsed.User == nil &&
		parsed.Port() == "" &&
		(strings.EqualFold(parsed.Hostname(), "doi.org") || strings.EqualFold(parsed.Hostname(), "dx.doi.org")) &&
		parsed.RawQuery == "" &&
		parsed.Fragment == "" &&
		parsed.Opaque == "" {
		return publicBareDOI.MatchString(strings.TrimPrefix(parsed.Path, "/"))
	}
	return false
}

// PublicHandle validates and stores a public HTTP(S) target behind a stable
// handle. The target is intentionally kept only in the private mapping.
func (h *Harvester) PublicHandle(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("public handle requires a public HTTP(S) URL")
	}
	if err := assertFetchable(source, false); err != nil {
		log.Printf("harvest: public handle rejected %q: %v", source, err)
		return "", errors.New("public handle requires a public HTTP(S) URL")
	}
	root, err := h.cacheRoot()
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(source))
	handle := publicHandlePrefix + hex.EncodeToString(key[:])
	dir := filepath.Join(root, ".private", "handles")
	if err := h.ensurePrivateHandleDir(root, dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, hex.EncodeToString(key[:])+".json")
	mapping, err := json.Marshal(publicHandleRecord{Target: source})
	if err != nil {
		log.Printf("harvest: public handle mapping encode failed for %q: %v", source, err)
		return "", errors.New("could not create public source handle")
	}
	if err := h.writeAtomic(path, mapping, 0o600); err != nil {
		log.Printf("harvest: public handle mapping write failed for %q: %v", source, err)
		return "", errors.New("could not create public source handle")
	}
	return handle, nil
}

// ResolvePublicSource decodes a handle and confines local requests. Public
// exports are readable; private cache/provenance paths are never readable.
func (h *Harvester) ResolvePublicSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, publicHandlePrefix) {
		if !publicHandleRE.MatchString(source) {
			return "", errors.New("invalid public source handle")
		}
		target, err := h.readPublicHandle(source)
		if err != nil {
			return "", err
		}
		if err := assertFetchable(target, false); err != nil {
			log.Printf("harvest: stored public handle target rejected %q: %v", target, err)
			return "", errors.New("stored public source is no longer fetchable")
		}
		return target, nil
	}
	// Match FetchWithOptions' precedence: an existing/explicit local file wins
	// over identifier syntax (an ISBN-named file is still a file), while a bare
	// DOI such as 10.1234/example must never be mistaken for a missing path.
	if !isDefiniteLocalSource(source) && ClassifyIdentifier(source) != IdentifierNone {
		return source, nil
	}
	if !isLocalSource(source) {
		return source, nil
	}
	path := source
	if strings.HasPrefix(strings.ToLower(path), "file://") {
		decoded, err := fileURLPath(path)
		if err != nil {
			return "", errors.New("invalid local source")
		}
		path = decoded
	}
	canonical, err := canonicalPublicPath(path)
	if err != nil {
		return "", errors.New("local source cannot be resolved")
	}
	root, err := h.cacheRoot()
	if err != nil {
		return "", err
	}
	lexicalPath, lexicalErr := filepath.Abs(filepath.Clean(path))
	if lexicalErr != nil {
		return "", errors.New("local source cannot be resolved")
	}
	lexicalCacheRoot, lexicalErr := filepath.Abs(filepath.Clean(root))
	if lexicalErr != nil {
		return "", errors.New("cache directory cannot be resolved")
	}
	lexicalPublicRoot := filepath.Join(lexicalCacheRoot, publicDirName)
	if isPathInside(lexicalPath, lexicalCacheRoot) && !isPathInside(lexicalPath, lexicalPublicRoot) {
		return "", errors.New("internal retrieval metadata is not available")
	}
	if isPathInside(lexicalPath, lexicalPublicRoot) {
		if symlinked, symlinkErr := symlinkBelow(lexicalPath, lexicalPublicRoot); symlinkErr != nil || symlinked {
			return "", errors.New("public export path is not a safe namespace")
		}
	}
	publicRoot, err := h.publicRoot()
	if err != nil {
		return "", errors.New("public export directory cannot be resolved")
	}
	cacheRoot, err := canonicalPublicPath(root)
	if err != nil {
		return "", errors.New("cache directory cannot be resolved")
	}
	if insideAny(canonical, []string{cacheRoot}) {
		if !insideAny(canonical, []string{publicRoot}) {
			return "", errors.New("internal retrieval metadata is not available")
		}
		if info, statErr := os.Stat(canonical); statErr != nil || !info.Mode().IsRegular() {
			return "", errors.New("public export does not exist")
		}
		return canonical, nil
	}
	if _, statErr := os.Stat(canonical); statErr != nil {
		return "", errors.New("local source does not exist")
	}
	if reason := DenyLocalPath(canonical, h.options.LocalRoots); reason != "" {
		return "", errors.New("local source is not permitted")
	}
	return canonical, nil
}

func (h *Harvester) readPublicHandle(source string) (string, error) {
	root, err := h.cacheRoot()
	if err != nil {
		return "", err
	}
	key := strings.TrimPrefix(source, publicHandlePrefix)
	dir := filepath.Join(root, ".private", "handles")
	if err := ensureNamespaceDir(filepath.Dir(dir), false); err != nil {
		return "", errors.New("public source handle is invalid")
	}
	if err := ensureNamespaceDir(dir, false); err != nil {
		return "", errors.New("public source handle is invalid")
	}
	canonicalDir, dirErr := canonicalPublicPath(dir)
	path := filepath.Join(dir, key+".json")
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("public source handle is invalid")
	}
	canonicalPath, pathErr := canonicalPublicPath(path)
	if dirErr != nil || pathErr != nil || !isPathInside(canonicalPath, canonicalDir) {
		return "", errors.New("public source handle is invalid")
	}
	data, err := readBoundedFile(path, 16*1024)
	if err != nil {
		log.Printf("harvest: public handle mapping read failed for %q: %v", source, err)
		return "", errors.New("public source handle is unavailable")
	}
	var record publicHandleRecord
	if err := json.Unmarshal(data, &record); err != nil || strings.TrimSpace(record.Target) == "" {
		return "", errors.New("public source handle is invalid")
	}
	target := strings.TrimSpace(record.Target)
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) || u.Host == "" || u.User != nil {
		return "", errors.New("public source handle is invalid")
	}
	return target, nil
}

func (h *Harvester) publicDisplayHandle(identity, exportedPath string) (string, error) {
	if publicIdentityHandle(identity) {
		return identity, nil
	}
	if isLocalSource(identity) || strings.HasPrefix(strings.ToLower(identity), "file://") {
		return exportedPath, nil
	}
	if err := assertFetchable(identity, false); err != nil {
		log.Printf("harvest: cache identity is not a public URL %q: %v", identity, err)
		return "", errors.New("cache match has no public identity")
	}
	return h.PublicHandle(identity)
}
