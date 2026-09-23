package harvest

// This file is the protocol boundary for results that leave the harvester
// process.  The transport and cache deliberately retain their provenance for
// diagnostics; this layer turns that private receipt into a separate,
// provenance-free artifact before an adapter can return it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// markdownImageWholeRE is one whole markdown image — alt text, link and an
// optional title — the span an image that cannot be published is replaced by.
var markdownImageWholeRE = regexp.MustCompile(`!\[([^\]]*)\]\(\s*([^\s)]+)(?:\s+"[^"]*")?\s*\)`)

// withPublicImages is body with its local images relocated into public/
// (rewritePublicImages). An image that cannot be published is dropped, its alt
// text kept, and the drop is named in out.Partial and in body's partial
// marker; only a failure of the public store itself is an error.
func (h *Harvester) withPublicImages(source, body, basePath string, out *Result) (string, error) {
	rewritten, dropped, err := h.rewritePublicImages(source, body, basePath)
	if err != nil || dropped == 0 {
		return rewritten, err
	}
	out.Partial = joinReasons(out.Partial, fmt.Sprintf("%d image(s) could not be published", dropped))
	return withPartial(partialBody(rewritten), out.Partial), nil
}

// rewritePublicImages copies each local image body embeds into public/ and
// rewrites its link. It returns how many images it dropped: a link that
// cannot be copied is never kept (a private path must not leak) and never
// fails the page; an error is the public store failing.
func (h *Harvester) rewritePublicImages(source, body, basePath string) (string, int, error) {
	matches := markdownImageRE.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return body, 0, nil
	}
	root, err := h.resolvedCacheRoot()
	if err != nil {
		return "", 0, err
	}
	cacheRoot, err := canonicalPublicPath(root)
	if err != nil {
		return "", 0, err
	}
	publicRoot, err := h.publicRoot()
	if err != nil {
		return "", 0, err
	}
	replacements := make(map[string]string)
	refused := make(map[string]bool)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		raw := body[match[2]:match[3]]
		if _, done := replacements[raw]; done || refused[raw] {
			continue
		}
		path, local := publicImagePath(raw, basePath)
		if !local {
			continue
		}
		canonical, reason := h.publishableImage(path, root, cacheRoot, publicRoot)
		if reason == "" && filepath.Dir(canonical) == publicRoot {
			replacements[raw] = publicRelativeLink(canonical)
			continue
		}
		var data []byte
		var ext string
		if reason == "" {
			data, ext, reason = readPublicImage(canonical)
		}
		if reason != "" {
			obs.Logger(context.Background()).Warn("harvest: an embedded image is dropped from the public result",
				"target", logSource(source), "reason", reason)
			refused[raw] = true
			continue
		}
		publicPath, err := h.publicArtifactPath(source, kindImage, canonical, ext)
		if err != nil {
			return "", 0, err
		}
		if err := h.writePublicFile(publicPath, data); err != nil {
			return "", 0, err
		}
		replacements[raw] = publicRelativeLink(publicPath)
	}
	if len(replacements) == 0 && len(refused) == 0 {
		return body, 0, nil
	}
	rewritten := markdownImageWholeRE.ReplaceAllStringFunc(body, func(fragment string) string {
		parts := markdownImageWholeRE.FindStringSubmatch(fragment)
		if len(parts) < 3 {
			return fragment
		}
		if refused[parts[2]] {
			return parts[1]
		}
		if replacement := replacements[parts[2]]; replacement != "" {
			return strings.Replace(fragment, parts[2], replacement, 1)
		}
		return fragment
	})
	for original, replacement := range replacements {
		rewritten = strings.ReplaceAll(rewritten, original, replacement)
	}
	for original := range refused {
		rewritten = strings.ReplaceAll(rewritten, original, "")
	}
	return rewritten, len(refused), nil
}

// publicRelativeLink is the link to a file in public/ from a document in
// public/: every public artifact lives flat in that one directory
// (publicArtifactPath), so the published page names no server path and the
// link resolves from wherever the page is opened.
func publicRelativeLink(publicPath string) string {
	return "./" + filepath.Base(publicPath)
}

// publishableImage is path resolved for publication, or the reason it may not
// be published: private metadata, an unsafe namespace, or a local file outside
// the cache that the server's permitted roots refuse.
func (h *Harvester) publishableImage(path, root, cacheRoot, publicRoot string) (string, string) {
	canonical, err := canonicalPublicPath(path)
	if err != nil {
		return "", "embedded image cannot be resolved"
	}
	lexicalPath, lexicalErr := filepath.Abs(filepath.Clean(path))
	if lexicalErr != nil || isPrivateMetadataPath(lexicalPath, root) {
		return "", "embedded image is inside private metadata"
	}
	if isPathInside(lexicalPath, root) {
		if symlinked, symlinkErr := symlinkBelow(lexicalPath, root); symlinkErr != nil || symlinked {
			return "", "embedded image is not a safe namespace"
		}
	}
	if isPathInside(canonical, publicRoot) || isPathInside(canonical, cacheRoot) {
		return canonical, ""
	}
	if reason := DenyLocalPath(canonical, h.options.LocalRoots); reason != "" {
		return "", "embedded local image is not permitted"
	}
	return canonical, ""
}

// readPublicImage is the image at canonical with its public extension, or the
// reason it is not a publishable image.
func readPublicImage(canonical string) ([]byte, string, string) {
	data, err := readBoundedFile(canonical, maxImageBytes)
	if err != nil {
		return nil, "", "embedded image cannot be read"
	}
	kind := classifyKind(canonical, "", data)
	if !isImageKind(kind) {
		kind = SniffMagic(data)
	}
	if !isImageKind(kind) {
		return nil, "", "embedded link is not an image"
	}
	ext, ok := publicBinaryExtension(kind, canonical, data)
	if !ok {
		return nil, "", "embedded image type is unsupported"
	}
	return data, ext, ""
}

// publicImagePath is the local file raw names, and whether it names one. A
// remote link — http(s), data:, or protocol-relative (//host/…, which a page
// may carry past the localizer's image cap) — names none.
func publicImagePath(raw, basePath string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	if trimmed == "" || strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") || strings.HasPrefix(trimmed, "//") {
		return "", false
	}
	if strings.HasPrefix(lower, "file://") {
		path, err := fileURLPath(trimmed)
		return path, err == nil
	}
	location := strings.Split(strings.Split(trimmed, "?")[0], "#")[0]
	if filepath.IsAbs(location) {
		return location, true
	}
	if basePath != "" && (strings.HasPrefix(location, ".") || strings.Contains(location, string(os.PathSeparator))) {
		return filepath.Join(filepath.Dir(basePath), location), true
	}
	return "", false
}
