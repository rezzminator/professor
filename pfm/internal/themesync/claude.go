package themesync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// claudeFile is Claude Code's custom theme file: {name, base, overrides},
// base dark|light, overrides a map of theme token to colour.
type claudeFile struct {
	Name      string            `json:"name"`
	Base      string            `json:"base"`
	Overrides map[string]string `json:"overrides"`
}

// claudeTokenKeys is the 47-token set of Claude Code's installed tokyo-night
// theme, the producer's own list; the live base file's keys win when present.
var claudeTokenKeys = []string{
	"claude", "claudeShimmer", tokenText, "inverseText", "inactive", "inactiveShimmer", "subtle", "suggestion",
	"permission", "permissionShimmer", "remember", "success", "error", "warning", "warningShimmer", "merged",
	tokenPromptBorder, tokenShimmer, "planMode", "autoAccept", "bashBorder", "ide", "fastMode",
	"fastModeShimmer", "diffAdded", "diffRemoved", "diffAddedDimmed", "diffRemovedDimmed", "diffAddedWord",
	"diffRemovedWord", "userMessageBackground", "userMessageBackgroundHover", "selectionBg",
	"bashMessageBackgroundColor", "memoryBackgroundColor", "rate_limit_fill", "rate_limit_empty",
	"briefLabelYou", "briefLabelClaude", "blue_FOR_SUBAGENTS_ONLY", "purple_FOR_SUBAGENTS_ONLY",
	"green_FOR_SUBAGENTS_ONLY", "cyan_FOR_SUBAGENTS_ONLY", "red_FOR_SUBAGENTS_ONLY", "orange_FOR_SUBAGENTS_ONLY",
	"yellow_FOR_SUBAGENTS_ONLY", "pink_FOR_SUBAGENTS_ONLY",
}

const (
	tokenText         = "text"
	tokenPromptBorder = "promptBorder"
	tokenShimmer      = "promptBorderShimmer"
	claudeBaseTheme   = "tokyo-night.json"
	claudeOwnedGlob   = "professor-*.json"
	claudeThemePrefix = "custom:professor-"
	shimmerWeight     = 0.35
)

// claudeOverlayKeys are the account overlay colours a professor-* theme keeps
// from its own pre-set file; every other token is computed.
var claudeOverlayKeys = map[string]bool{tokenPromptBorder: true, tokenShimmer: true}

// claudeRoles maps every known Claude token to its colour in this variant:
// brand and prompt colours from the accent and ANSI hues, diff and message
// backgrounds blended toward the background, shimmer toward the foreground.
func (variant Variant) claudeRoles() map[string]string {
	ansi, state := variant.ANSI, variant.State
	shimmer := func(colour string) string { return mixHex(colour, variant.Foreground, shimmerWeight) }
	tint := func(colour string, weight float64) string { return mixHex(variant.Background, colour, weight) }
	orange := mixHex(ansi[1], ansi[3], 0.5)
	pink := mixHex(ansi[5], ansi[1], 0.5)
	return map[string]string{
		"claude":                     variant.Accent,
		"claudeShimmer":              shimmer(variant.Accent),
		tokenText:                    variant.Foreground,
		"inverseText":                variant.Background,
		"inactive":                   variant.Muted,
		"inactiveShimmer":            shimmer(variant.Muted),
		"subtle":                     variant.Overlay,
		"suggestion":                 ansi[6],
		"permission":                 ansi[5],
		"permissionShimmer":          shimmer(ansi[5]),
		"remember":                   ansi[6],
		"success":                    state.Done,
		"error":                      state.Blocked,
		"warning":                    state.Working,
		"warningShimmer":             shimmer(state.Working),
		"merged":                     ansi[5],
		tokenPromptBorder:            variant.Accent,
		tokenShimmer:                 shimmer(variant.Accent),
		"planMode":                   ansi[6],
		"autoAccept":                 state.Done,
		"bashBorder":                 orange,
		"ide":                        ansi[4],
		"fastMode":                   ansi[5],
		"fastModeShimmer":            shimmer(ansi[5]),
		"diffAdded":                  tint(state.Done, 0.22),
		"diffRemoved":                tint(state.Blocked, 0.22),
		"diffAddedDimmed":            tint(state.Done, 0.12),
		"diffRemovedDimmed":          tint(state.Blocked, 0.12),
		"diffAddedWord":              tint(state.Done, 0.45),
		"diffRemovedWord":            tint(state.Blocked, 0.45),
		"userMessageBackground":      variant.Surface,
		"userMessageBackgroundHover": mixHex(variant.Surface, variant.Foreground, 0.06),
		"selectionBg":                variant.SelectionBackground,
		"bashMessageBackgroundColor": tint(orange, 0.15),
		"memoryBackgroundColor":      tint(state.Done, 0.15),
		"rate_limit_fill":            variant.Accent,
		"rate_limit_empty":           tint(variant.Muted, 0.35),
		"briefLabelYou":              ansi[6],
		"briefLabelClaude":           variant.Accent,
		"blue_FOR_SUBAGENTS_ONLY":    ansi[4],
		"purple_FOR_SUBAGENTS_ONLY":  ansi[5],
		"green_FOR_SUBAGENTS_ONLY":   ansi[2],
		"cyan_FOR_SUBAGENTS_ONLY":    ansi[6],
		"red_FOR_SUBAGENTS_ONLY":     ansi[1],
		"orange_FOR_SUBAGENTS_ONLY":  orange,
		"yellow_FOR_SUBAGENTS_ONLY":  ansi[3],
		"pink_FOR_SUBAGENTS_ONLY":    pink,
	}
}

// claudeRender renders one professor-* theme: the original's name suffixed
// with the palette title, the appearance's base, every requested token from
// the variant (a token the roles do not know keeps its installed-base value),
// and the original's overlay keys.
func (palette Palette) claudeRender(
	appearance Appearance, original claudeFile, tokens []string, base map[string]string,
) claudeFile {
	variant := palette.Variant(appearance)
	roles := variant.claudeRoles()
	rendered := claudeFile{
		Name:      strings.TrimSpace(original.Name + " · " + palette.Title),
		Base:      string(Dark),
		Overrides: make(map[string]string, len(tokens)),
	}
	if appearance == Light {
		rendered.Base = string(Light)
	}
	for _, token := range tokens {
		switch value, known := roles[token]; {
		case claudeOverlayKeys[token] && original.Overrides[token] != "":
			rendered.Overrides[token] = original.Overrides[token]
		case known:
			rendered.Overrides[token] = value
		default:
			rendered.Overrides[token] = base[token]
		}
	}
	return rendered
}

// claudeThemesDir is the store every ~/.cc/N/themes symlinks to.
func (s *session) claudeThemesDir() string { return filepath.Join(s.env.Home, ".claude", "themes") }

// claudeTargets lists the professor-* theme files that exist, sorted.
func (s *session) claudeTargets() ([]string, error) {
	targets, err := filepath.Glob(filepath.Join(s.claudeThemesDir(), claudeOwnedGlob))
	if err != nil {
		return nil, fmt.Errorf("themesync: list %s: %w", s.claudeThemesDir(), err)
	}
	sort.Strings(targets)
	return targets, nil
}

// claudeTokens is the token set: the keys of the installed base theme when it
// exists, else the embedded list; the base's own values come back for tokens
// the roles do not cover.
func (s *session) claudeTokens() ([]string, map[string]string, error) {
	path := filepath.Join(s.claudeThemesDir(), claudeBaseTheme)
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return claudeTokenKeys, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("themesync: read %s: %w", path, err)
	}
	var base claudeFile
	if err := json.Unmarshal(content, &base); err != nil {
		return nil, nil, fmt.Errorf("themesync: parse %s: %w", path, err)
	}
	tokens := make([]string, 0, len(base.Overrides))
	for token := range base.Overrides {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens, base.Overrides, nil
}

// claudeEncode is the bytes a rendered theme is written as.
func claudeEncode(theme claudeFile) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(theme); err != nil {
		return nil, fmt.Errorf("themesync: encode claude theme: %w", err)
	}
	return buffer.Bytes(), nil
}

// claudeDesired renders the theme a target should hold now, from the
// pre-set snapshot of that file.
func (s *session) claudeDesired(target string, tokens []string, base map[string]string) ([]byte, error) {
	snapshot, err := os.ReadFile(s.state.Claude[target])
	if err != nil {
		return nil, fmt.Errorf("themesync: read snapshot of %s: %w", target, err)
	}
	var original claudeFile
	if err := json.Unmarshal(snapshot, &original); err != nil {
		return nil, fmt.Errorf("themesync: parse snapshot of %s: %w", target, err)
	}
	return claudeEncode(s.palette.claudeRender(s.state.Appearance, original, tokens, base))
}

func (s *session) applyClaude(_ context.Context) SurfaceResult {
	targets, err := s.claudeTargets()
	if err != nil {
		return s.failed(surfaceClaude, s.claudeThemesDir(), err)
	}
	if len(targets) == 0 {
		return s.absent(surfaceClaude, s.claudeThemesDir(), "no "+claudeOwnedGlob+" in "+s.claudeThemesDir())
	}
	tokens, base, err := s.claudeTokens()
	if err != nil {
		return s.failed(surfaceClaude, s.claudeThemesDir(), err)
	}
	if s.state.Claude == nil {
		s.state.Claude = map[string]string{}
	}
	var failures []string
	for _, target := range targets {
		if err := s.claudeApplyOne(target, tokens, base); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return s.failed(surfaceClaude, s.claudeThemesDir(), errors.New(strings.Join(failures, "; ")))
	}
	return s.result(surfaceClaude, VerdictApplied, s.claudeThemesDir(), "")
}

func (s *session) claudeApplyOne(target string, tokens []string, base map[string]string) error {
	current, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("themesync: read %s: %w", target, err)
	}
	if s.state.Claude[target] == "" {
		backup := s.store.backupPath(surfaceClaude, filepath.Base(target))
		if err := atomicfile.Write(backup, current, 0o600); err != nil {
			return fmt.Errorf("themesync: back up %s: %w", target, err)
		}
		s.state.Claude[target] = backup
		if err := s.persist(); err != nil {
			return err
		}
	}
	desired, err := s.claudeDesired(target, tokens, base)
	if err != nil {
		return err
	}
	if bytes.Equal(current, desired) {
		return nil
	}
	return s.replaceFile(target, desired)
}

// claudeAccountThemes names the accounts whose settings select something other
// than a professor theme, and whether any account selects one.
func (s *session) claudeAccountThemes() (others []string, selects bool, err error) {
	paths, err := filepath.Glob(filepath.Join(s.env.Home, ".cc", "*", "settings.json"))
	if err != nil {
		return nil, false, fmt.Errorf("themesync: list account settings: %w", err)
	}
	paths = append(paths, filepath.Join(s.env.Home, ".claude", "settings.json"))
	sort.Strings(paths)
	for _, path := range paths {
		content, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		var settings struct {
			Theme string `json:"theme"`
		}
		if readErr == nil {
			readErr = json.Unmarshal(content, &settings)
		}
		if readErr != nil {
			return nil, false, fmt.Errorf("themesync: read %s: %w", path, readErr)
		}
		if strings.HasPrefix(settings.Theme, claudeThemePrefix) {
			selects = true
			continue
		}
		label := strings.TrimPrefix(filepath.Dir(path), s.env.Home+string(filepath.Separator))
		theme := settings.Theme
		if theme == "" {
			theme = "(unset)"
		}
		others = append(others, label+" theme "+theme)
	}
	return others, selects, nil
}

func (s *session) readClaude(_ context.Context) SurfaceResult {
	dir := s.claudeThemesDir()
	if len(s.state.Claude) == 0 {
		return s.absent(surfaceClaude, dir, "no "+claudeOwnedGlob+" held in "+dir)
	}
	tokens, base, err := s.claudeTokens()
	if err != nil {
		return s.failed(surfaceClaude, dir, err)
	}
	var drift []string
	for target := range s.state.Claude {
		desired, err := s.claudeDesired(target, tokens, base)
		if err != nil {
			return s.failed(surfaceClaude, dir, err)
		}
		current, err := os.ReadFile(target)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return s.failed(surfaceClaude, target, fmt.Errorf("themesync: read %s: %w", target, err))
		}
		if err != nil || !bytes.Equal(current, desired) {
			drift = append(drift, filepath.Base(target)+" differs from the render")
		}
	}
	others, selects, err := s.claudeAccountThemes()
	if err != nil {
		return s.failed(surfaceClaude, dir, err)
	}
	sort.Strings(drift)
	if len(others) > 0 {
		drift = append(drift, "accounts not following: "+strings.Join(others, ", "))
	}
	if len(drift) > 0 && (len(others) == 0 || !selects || len(drift) > 1) {
		return s.in(surfaceClaude, VerdictDrift, dir, strings.Join(drift, "; "), 0)
	}
	return s.in(surfaceClaude, VerdictInSync, dir, strings.Join(drift, "; "), s.state.Generation)
}

func (s *session) restoreClaude(_ context.Context) SurfaceResult {
	dir := s.claudeThemesDir()
	if len(s.state.Claude) == 0 {
		return s.absent(surfaceClaude, dir, "nothing applied")
	}
	var failures []string
	restored := 0
	for target, backup := range s.state.Claude {
		original, err := os.ReadFile(backup)
		if err == nil {
			err = s.replaceFile(target, original)
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", filepath.Base(target), err))
			continue
		}
		delete(s.state.Claude, target)
		restored++
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return s.failed(surfaceClaude, dir, errors.New(strings.Join(failures, "; ")))
	}
	return s.result(surfaceClaude, VerdictRestored, dir, fmt.Sprintf("%d themes", restored))
}
