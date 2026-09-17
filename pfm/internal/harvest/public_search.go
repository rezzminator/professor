package harvest

// This file is the protocol boundary for results that leave the harvester
// process.  The transport and cache deliberately retain their provenance for
// diagnostics; this layer turns that private receipt into a separate,
// provenance-free artifact before an adapter can return it.

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SearchCachePublic searches private cache entries and exports every match
// before returning it. Public exports are skipped so they cannot consume the
// caller's result limit or become a second search index.
func (h *Harvester) SearchCachePublic(pattern string, maxResults int, ignoreCase bool) ([]CacheSearchResult, error) {
	if maxResults <= 0 {
		maxResults = 50
	}
	entries, err := h.searchPrivateCache(pattern, publicSearchLimit, ignoreCase)
	if err != nil {
		return nil, err
	}
	publicRoot, err := h.publicRoot()
	if err != nil {
		return nil, err
	}
	out := make([]CacheSearchResult, 0, maxResults)
	for _, entry := range entries {
		if isPathInside(entry.Path, publicRoot) {
			continue
		}
		if len(out) >= maxResults {
			break
		}
		identity := strings.TrimSpace(entry.URL)
		if identity == "" || identity == entry.Path {
			return nil, errors.New("cache match has no public document identity")
		}
		exported := h.PublicResult(identity, Result{Source: identity, Path: entry.Path}, false)
		if exported.Error != "" || exported.Path == "" {
			return nil, errors.New("cache match could not be exported safely")
		}
		publicBody, err := h.readPublicBody(exported.Path)
		if err != nil {
			return nil, errors.New("cache match could not be read safely")
		}
		rx, err := publicSearchRegexp(pattern, ignoreCase)
		if err != nil {
			return nil, err
		}
		matches := rx.FindAllString(publicBody, -1)
		if len(matches) == 0 {
			continue
		}
		sample := ""
		for _, line := range strings.Split(publicBody, "\n") {
			if rx.MatchString(line) {
				sample = strings.TrimSpace(line)
				if len([]rune(sample)) > 200 {
					sample = string([]rune(sample)[:200])
				}
				break
			}
		}
		display, err := h.publicDisplayHandle(identity, exported.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, CacheSearchResult{URL: display, Path: exported.Path, Matches: len(matches), Sample: sample})
	}
	return out, nil
}

func (h *Harvester) searchPrivateCache(pattern string, _ int, ignoreCase bool) ([]CacheSearchResult, error) {
	if h == nil || h.cache == nil {
		return nil, errors.New("harvester cache is unavailable")
	}
	rx, err := publicSearchRegexp(pattern, ignoreCase)
	if err != nil {
		return nil, err
	}
	// Walk the resolved cache root so a configured cache directory symlink is
	// valid, while symlinks introduced below that root still fail closed.
	root, rootErr := canonicalPublicPath(h.cache.root)
	if rootErr != nil {
		return nil, fmt.Errorf("search cache: resolve cache directory: %w", rootErr)
	}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return []CacheSearchResult{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("search cache: %w", err)
	}
	publicRoot := filepath.Join(root, publicDirName)
	privateRoot := filepath.Join(root, ".private")
	out := make([]CacheSearchResult, 0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			log.Printf("harvest: private cache search found unsafe symlink %q", path)
			return errors.New("private cache search found an unsafe symlink")
		}
		if entry.IsDir() {
			if path != root && isPathInside(path, publicRoot) {
				return fs.SkipDir
			}
			if path != root && isPathInside(path, privateRoot) {
				return fs.SkipDir
			}
			return nil
		}
		if isPathInside(path, publicRoot) || isPathInside(path, privateRoot) {
			return nil
		}
		if filepath.Ext(path) != extensionMD {
			return nil
		}
		raw, readErr := readBoundedFile(path, h.publicLimit())
		if readErr != nil {
			log.Printf("harvest: private cache search cannot read %q: %v", path, readErr)
			return errors.New("private cache search could not read an artifact")
		}
		meta, body := parseCacheFrontmatter(string(raw))
		if meta["source"] == frontmatterSourceHarvester {
			body = stripGeneratedSourceMetadata(body)
		}
		hits := rx.FindAllString(body, -1)
		if len(hits) == 0 {
			return nil
		}
		sample := ""
		for _, line := range strings.Split(body, "\n") {
			if rx.MatchString(line) {
				sample = strings.TrimSpace(line)
				if len([]rune(sample)) > 200 {
					sample = string([]rune(sample)[:200])
				}
				break
			}
		}
		display := meta["url"]
		if display == "" {
			display = path
		}
		out = append(out, CacheSearchResult{URL: display, Path: path, Matches: len(hits), Sample: sample})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search cache: %w", err)
	}
	return out, nil
}

func publicSearchRegexp(pattern string, ignoreCase bool) (*regexp.Regexp, error) {
	flags := pattern
	if ignoreCase {
		flags = "(?i)" + pattern
	}
	rx, err := regexp.Compile(flags)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}
	return rx, nil
}

func (h *Harvester) readPublicBody(path string) (string, error) {
	raw, err := h.readPublicArtifact(path)
	if err != nil {
		return "", err
	}
	if meta, body := parseCacheFrontmatter(string(raw)); meta["source"] == frontmatterSourceHarvester {
		return body, nil
	}
	return string(raw), nil
}
