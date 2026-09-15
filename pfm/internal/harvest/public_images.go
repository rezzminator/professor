package harvest

// This file is the protocol boundary for results that leave the harvester
// process.  The transport and cache deliberately retain their provenance for
// diagnostics; this layer turns that private receipt into a separate,
// provenance-free artifact before an adapter can return it.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (h *Harvester) rewritePublicImages(source, body, basePath string) (string, error) {
	matches := markdownImageRE.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return body, nil
	}
	replacements := make(map[string]string)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		raw := body[match[2]:match[3]]
		if raw == "" || strings.HasPrefix(strings.ToLower(raw), "data:") ||
			strings.HasPrefix(strings.ToLower(raw), "http://") ||
			strings.HasPrefix(strings.ToLower(raw), "https://") {
			continue
		}
		path, local := publicImagePath(raw, basePath)
		if !local {
			continue
		}
		canonical, err := canonicalPublicPath(path)
		if err != nil {
			return "", errors.New("embedded image cannot be resolved")
		}
		root, err := h.cacheRoot()
		if err != nil {
			return "", err
		}
		lexicalPath, lexicalErr := filepath.Abs(filepath.Clean(path))
		if lexicalErr != nil || isPrivateMetadataPath(lexicalPath, root) {
			return "", errors.New("embedded image is inside private metadata")
		}
		if isPathInside(lexicalPath, root) {
			if symlinked, symlinkErr := symlinkBelow(lexicalPath, root); symlinkErr != nil || symlinked {
				return "", errors.New("embedded image is not a safe namespace")
			}
		}
		cacheRoot, err := canonicalPublicPath(root)
		if err != nil {
			return "", err
		}
		publicRoot, err := h.publicRoot()
		if err != nil {
			return "", err
		}
		if isPathInside(canonical, publicRoot) {
			continue
		}
		if isPathInside(canonical, cacheRoot) {
			// Private cache links must be copied. A link that cannot be
			// copied is a failed export, never a leaked private path.
		} else if reason := DenyLocalPath(canonical, h.options.LocalRoots); reason != "" {
			return "", errors.New("embedded local image is not permitted")
		}
		data, err := readBoundedFile(canonical, maxImageBytes)
		if err != nil {
			return "", errors.New("embedded image cannot be read")
		}
		kind := classifyKind(canonical, "", data)
		if !isImageKind(kind) {
			kind = SniffMagic(data)
		}
		if !isImageKind(kind) {
			return "", errors.New("embedded link is not an image")
		}
		ext, ok := publicBinaryExtension(kind, canonical, data)
		if !ok {
			return "", errors.New("embedded image type is unsupported")
		}
		publicPath, err := h.publicArtifactPath(source, "image", canonical, ext)
		if err != nil {
			return "", err
		}
		if err := h.writePublicFile(publicPath, data); err != nil {
			return "", err
		}
		replacements[raw] = publicPath
	}
	if len(replacements) == 0 {
		return body, nil
	}
	rewritten := markdownImageRE.ReplaceAllStringFunc(body, func(fragment string) string {
		parts := markdownImageRE.FindStringSubmatch(fragment)
		if len(parts) < 2 {
			return fragment
		}
		if replacement := replacements[parts[1]]; replacement != "" {
			return strings.Replace(fragment, parts[1], replacement, 1)
		}
		return fragment
	})
	for original, replacement := range replacements {
		rewritten = strings.ReplaceAll(rewritten, original, replacement)
	}
	return rewritten, nil
}

func publicImagePath(raw, basePath string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(trimmed), "file://") {
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
