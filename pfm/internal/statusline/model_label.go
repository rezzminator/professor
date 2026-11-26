package statusline

import (
	"strings"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// modelVersion is the model's id as the price table keys it (a gateway asset
// path, Bedrock prefix and context-window tag dropped), without the vendor
// prefix: claude-opus-5-5[1m] → opus-5-5, gw:catalog/claude-opus-5 → opus-5.
func modelVersion(model string) string {
	if id, ok := pricing.NormalizeID(model); ok {
		model = id
	}
	model = strings.TrimPrefix(strings.TrimSpace(model), "claude-")
	if cut := strings.IndexByte(model, '['); cut >= 0 {
		model = model[:cut]
	}
	return model
}

// modelFamily is the model's family alone: claude-opus-5-5[1m] → opus. A gpt-
// id names its tier last and its version first, so it renders tier-version:
// gpt-6-sol → sol-6, gpt-5.6-terra → terra-5.6; one with no tier renders
// whole, gpt-6 → gpt-6. The gateway's priority-tier suffix is no tier and
// drops like the window tag: gpt-6-sol-fast → sol-6, gpt-5.5-fast → gpt-5.5.
func modelFamily(model string) string {
	model = modelVersion(model)
	if strings.HasPrefix(model, "gpt-") {
		model = strings.TrimSuffix(model, "-fast")
		rest := strings.TrimPrefix(model, "gpt-")
		if cut := strings.LastIndexByte(rest, '-'); cut > 0 && cut < len(rest)-1 {
			return rest[cut+1:] + "-" + rest[:cut]
		}
		return model
	}
	if cut := strings.IndexByte(model, '-'); cut > 0 {
		model = model[:cut]
	}
	return model
}

// modelLabel is the one name an agent row shows beside the model's glyph, the
// main line's spelling: claude-opus-5-5[1m] → Opus 5.5,
// claude-haiku-4-5-20251001 → Haiku 4.5 (a release date drops), the alias
// opus → Opus; each version part is one or two digits. A display name (one
// with a blank, "Opus 5.5") stays as given; a gpt- model keeps modelFamily's
// tier-version form (gpt-6-sol → sol-6); an id of any other shape is
// modelVersion's, its vendor prefix dropped; no model is "".
func modelLabel(model string) string {
	if model = strings.TrimSpace(model); strings.ContainsRune(model, ' ') {
		return model
	}
	version := modelVersion(model)
	if strings.HasPrefix(version, "gpt-") {
		return modelFamily(model)
	}
	parts := strings.Split(strings.ToLower(version), "-")
	if last := len(parts) - 1; last > 0 && len(parts[last]) == 8 && digitsOnly(parts[last]) {
		parts = parts[:last]
	}
	family := parts[0]
	if family == "" || strings.Trim(family, "abcdefghijklmnopqrstuvwxyz") != "" {
		return version
	}
	for _, part := range parts[1:] {
		if len(part) > 2 || !digitsOnly(part) {
			return version
		}
	}
	label := strings.ToUpper(family[:1]) + family[1:]
	if len(parts) > 1 {
		label += " " + strings.Join(parts[1:], ".")
	}
	return label
}

// digitsOnly reports whether text is one or more ASCII digits.
func digitsOnly(text string) bool {
	return text != "" && strings.Trim(text, "0123456789") == ""
}
