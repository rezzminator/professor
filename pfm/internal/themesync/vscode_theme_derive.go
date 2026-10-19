package themesync

import (
	"fmt"
	"math"
)

// The contrast floors a derived VS Code theme holds against the editor
// background, the current line's highlight and the selection: body text,
// every syntax colour, and the comments, which sit back on purpose.
const (
	vscodeTextContrast    = 7.0
	vscodeTokenContrast   = 4.5
	vscodeCommentContrast = 3.5
)

// The two inks every fallback ends at.
const (
	inkBlack = "#000000"
	inkWhite = "#ffffff"
)

// vscodeRoles is a palette read as a VS Code theme reads it: the layered
// backgrounds from the deepest (tab strip, inputs) to the hovered surface,
// the text tiers, the accent and the hues. Every value is #rrggbb.
type vscodeRoles struct {
	light                                         bool
	deep, bg, line, sidebar, surface, hover       string
	selection, border, shadow                     string
	fg, soft, muted, faint, subtle                string
	accent, accentHi, onAccent, focus, info       string
	red, orange, yellow, green, cyan, blue        string
	purple, pink                                  string
	diffAdded, diffRemoved, diffAddWord, diffWord string
}

// vscodeSyntax is the colour of every syntax role, each lifted to its
// contrast floor against the editor background, the current line and the
// selection.
type vscodeSyntax struct {
	keyword, storage, operator, punctuation, plain string
	str, escape, regex, number, constant, language string
	function, builtin, typ, tag, attribute         string
	parameter, property, decorator, namespace      string
	self, comment, invalid, heading, link, code    string
}

// mixColour is a and b blended in sRGB, weight the share of b.
func mixColour(a, b string, weight float64) string {
	left, errLeft := parseColor(a)
	right, errRight := parseColor(b)
	if errLeft != nil || errRight != nil {
		return a
	}
	channel := func(x, y uint8) uint8 {
		return uint8(math.Round(float64(x)*(1-weight) + float64(y)*weight))
	}
	return rgb{
		channel(left.red, right.red), channel(left.green, right.green), channel(left.blue, right.blue),
	}.String()
}

// withAlpha is a #rrggbb colour at the given opacity, #rrggbbaa.
func withAlpha(colour string, alpha uint8) string { return fmt.Sprintf("%s%02x", colour, alpha) }

// vscodeSelectionAlpha is the opacity editor.selectionBackground draws the
// palette's selection at over the editor background.
const vscodeSelectionAlpha = 0xcc

// overColour is colour drawn at alpha over ground: the #rrggbb VS Code shows.
func overColour(ground, colour string, alpha uint8) string {
	return mixColour(ground, colour, float64(alpha)/0xff)
}

// readVSCodeRoles reads the roles off a resolved palette (customPalette's
// tokens) and its derived variant. A token the palette leaves unset (a
// built-in base carries only derive's tokens) falls back to the nearest role
// the variant holds, so every base renders a whole theme. Body text is lifted
// to its floor on the editor and the current line first, so every tier mixed
// from it inherits the lift; name is the theme an unreachable floor names.
func readVSCodeRoles(name string, tokens map[string]string, variant Variant, side Appearance) (vscodeRoles, error) {
	pick := func(name, fallback string) string {
		if colour, err := parseColor(tokens[name]); err == nil && tokens[name] != "" {
			return colour.String()
		}
		return fallback
	}
	roles := vscodeRoles{light: side == Light, bg: variant.Background}
	roles.surface = variant.Surface
	roles.selection = variant.SelectionBackground
	roles.muted = variant.Muted
	roles.subtle = variant.Overlay
	roles.accent = variant.Accent
	roles.line = mixColour(roles.bg, roles.surface, 0.4)
	fg, err := liftColorAll(
		name, "vscode foreground", variant.Foreground, []string{roles.bg, roles.line}, vscodeTextContrast,
	)
	if err != nil {
		return vscodeRoles{}, err
	}
	roles.fg = fg
	// The deepest layer is where tabs, inputs and code blocks draw body
	// text, so Clawd's eye colour serves as it only when that text reads on
	// it at its floor: a light palette's Clawd is dark ink.
	deep := func(fallback string) string {
		eye, text := pick("clawd_background", ""), roles.fg
		ground, errGround := parseColor(eye)
		ink, errInk := parseColor(text)
		if eye != "" && errGround == nil && errInk == nil && contrastRatio(ink, ground) >= vscodeTextContrast {
			return eye
		}
		return fallback
	}
	ink := inkBlack
	if roles.light {
		roles.deep = deep(mixColour(roles.bg, roles.fg, 0.06))
		roles.sidebar = pick("composerSidebarBackground", mixColour(roles.bg, roles.fg, 0.03))
		roles.shadow = withAlpha(ink, 0x26)
	} else {
		roles.deep = deep(mixColour(roles.bg, ink, 0.3))
		roles.sidebar = pick("composerSidebarBackground", mixColour(roles.bg, roles.surface, 0.35))
		roles.shadow = withAlpha(ink, 0x80)
	}
	roles.hover = pick("userMessageBackgroundHover", mixColour(roles.surface, roles.fg, 0.08))
	roles.border = mixColour(roles.bg, roles.subtle, 0.45)
	roles.soft = mixColour(roles.fg, roles.muted, 0.45)
	roles.faint = mixColour(roles.muted, roles.bg, 0.45)
	roles.accentHi = pick("claudeShimmer", mixColour(roles.accent, roles.fg, 0.4))
	roles.focus = pick("promptBorder", roles.accent)
	hue := func(name string) string { return pick(name+"_FOR_SUBAGENTS_ONLY", variant.ANSI[1]) }
	roles.red = pick("error", hue("red"))
	roles.orange = pick("orange_FOR_SUBAGENTS_ONLY", variant.Orange)
	roles.yellow = pick("warning", hue("yellow"))
	roles.green = pick("success", hue("green"))
	roles.cyan = hue("cyan")
	roles.blue = hue("blue")
	roles.purple = hue("purple")
	roles.pink = pick("pink_FOR_SUBAGENTS_ONLY", mixColour(roles.red, roles.purple, 0.5))
	roles.info = pick("ide", roles.blue)
	roles.diffAdded = pick("diffAdded", mixColour(roles.bg, roles.green, 0.16))
	roles.diffRemoved = pick("diffRemoved", mixColour(roles.bg, roles.red, 0.16))
	roles.diffAddWord = pick("diffAddedWord", mixColour(roles.bg, roles.green, 0.32))
	roles.diffWord = pick("diffRemovedWord", mixColour(roles.bg, roles.red, 0.32))
	roles.onAccent = onColour(roles.accent, roles.deep, roles.bg, roles.fg)
	return roles, nil
}

// onColour is the text colour drawn on a filled background: the first of the
// candidates (then black and white) that reaches the token floor on it, else
// whichever of all reaches furthest.
func onColour(fill string, candidates ...string) string {
	background, err := parseColor(fill)
	if err != nil {
		return inkWhite
	}
	best, bestRatio := inkWhite, 0.0
	for _, candidate := range append(candidates, inkBlack, inkWhite) {
		colour, err := parseColor(candidate)
		if err != nil {
			continue
		}
		ratio := contrastRatio(colour, background)
		if ratio >= vscodeTokenContrast {
			return candidate
		}
		if ratio > bestRatio {
			best, bestRatio = candidate, ratio
		}
	}
	return best
}

// readVSCodeSyntax assigns each syntax role a palette colour and lifts it to
// its floor against the editor background, the current line and the
// selection drawn over the editor (given up first when no lightness clears
// all three). name is the theme an unreachable floor's error names.
func readVSCodeSyntax(name string, tokens map[string]string, roles vscodeRoles) (vscodeSyntax, error) {
	pick := func(token, fallback string) string {
		if colour, err := parseColor(tokens[token]); err == nil && tokens[token] != "" {
			return colour.String()
		}
		return fallback
	}
	syntax := vscodeSyntax{
		keyword:     roles.accent,
		storage:     pick("skill", roles.purple),
		operator:    mixColour(roles.accent, roles.fg, 0.55),
		punctuation: mixColour(roles.fg, roles.muted, 0.6),
		plain:       roles.fg,
		str:         roles.green,
		escape:      pick("remember", roles.cyan),
		regex:       pick("autoAccept", roles.orange),
		number:      pick("fastMode", roles.orange),
		constant:    roles.orange,
		language:    pick("merged", roles.purple),
		function:    roles.info,
		builtin:     pick("permission", roles.cyan),
		typ:         pick("rainbow_yellow", roles.yellow),
		tag:         pick("rainbow_red", roles.red),
		attribute:   pick("rainbow_orange", roles.orange),
		parameter:   roles.accentHi,
		property:    pick("claudeBlueShimmer_FOR_SYSTEM_SPINNER", mixColour(roles.blue, roles.fg, 0.45)),
		decorator:   pick("planMode", roles.purple),
		namespace:   pick("rainbow_indigo", mixColour(roles.blue, roles.purple, 0.5)),
		self:        pick("rainbow_violet", roles.purple),
		comment:     mixColour(roles.muted, roles.bg, 0.3),
		invalid:     roles.red,
		heading:     roles.accent,
		link:        roles.info,
		code:        pick("rainbow_orange", roles.orange),
	}
	against := []string{roles.bg, roles.line, overColour(roles.bg, roles.selection, vscodeSelectionAlpha)}
	for _, role := range []struct {
		name   string
		colour *string
		floor  float64
	}{
		{"keyword", &syntax.keyword, vscodeTokenContrast},
		{"storage", &syntax.storage, vscodeTokenContrast},
		{"operator", &syntax.operator, vscodeTokenContrast},
		{"punctuation", &syntax.punctuation, vscodeTokenContrast},
		{"plain", &syntax.plain, vscodeTextContrast},
		{"string", &syntax.str, vscodeTokenContrast},
		{"escape", &syntax.escape, vscodeTokenContrast},
		{"regex", &syntax.regex, vscodeTokenContrast},
		{"number", &syntax.number, vscodeTokenContrast},
		{"constant", &syntax.constant, vscodeTokenContrast},
		{"language", &syntax.language, vscodeTokenContrast},
		{"function", &syntax.function, vscodeTokenContrast},
		{"builtin", &syntax.builtin, vscodeTokenContrast},
		{"type", &syntax.typ, vscodeTokenContrast},
		{"tag", &syntax.tag, vscodeTokenContrast},
		{"attribute", &syntax.attribute, vscodeTokenContrast},
		{"parameter", &syntax.parameter, vscodeTokenContrast},
		{"property", &syntax.property, vscodeTokenContrast},
		{"decorator", &syntax.decorator, vscodeTokenContrast},
		{"namespace", &syntax.namespace, vscodeTokenContrast},
		{"self", &syntax.self, vscodeTokenContrast},
		{"comment", &syntax.comment, vscodeCommentContrast},
		{"invalid", &syntax.invalid, vscodeTokenContrast},
		{"heading", &syntax.heading, vscodeTokenContrast},
		{"link", &syntax.link, vscodeTokenContrast},
		{"code", &syntax.code, vscodeTokenContrast},
	} {
		lifted, err := liftColorAll(name, "vscode "+role.name, *role.colour, against, role.floor)
		if err != nil {
			return vscodeSyntax{}, err
		}
		*role.colour = lifted
	}
	return syntax, nil
}

// deriveVSCodeTheme is the whole VS Code colour theme a palette implies:
// the workbench in the palette's layered backgrounds, the syntax in its hues,
// and the symbol icons in the same colours as the code they stand for. name
// is the theme an error names.
func deriveVSCodeTheme(name string, tokens map[string]string, variant Variant, side Appearance) (VSCodeTheme, error) {
	roles, err := readVSCodeRoles(name, tokens, variant, side)
	if err != nil {
		return VSCodeTheme{}, err
	}
	syntax, err := readVSCodeSyntax(name, tokens, roles)
	if err != nil {
		return VSCodeTheme{}, err
	}
	colors := vscodeWorkbenchColors(roles)
	for key, value := range vscodeSyntaxColors(roles, syntax) {
		colors[key] = value
	}
	semantic := true
	return VSCodeTheme{
		Colors:               colors,
		TokenColors:          vscodeTokenRules(syntax),
		SemanticHighlighting: &semantic,
		SemanticTokenColors:  vscodeSemanticRules(syntax),
	}, nil
}

// vscodeWorkbenchColors is the workbench in the roles: tabs and inputs sit
// in the deepest layer, the editor and panel on the background, the side bar
// and every floating widget on the sidebar layer, selection and hover on
// the palette's own, and every state in its hue. The contract's keys
// (terminal, title, activity and status bars) are set over these by
// RenderVSCodeTheme, so they are left to it.
func vscodeWorkbenchColors(r vscodeRoles) map[string]string {
	a := withAlpha
	return map[string]string{
		"foreground":                r.fg,
		"descriptionForeground":     r.muted,
		"disabledForeground":        r.faint,
		"errorForeground":           r.red,
		"icon.foreground":           r.soft,
		"focusBorder":               a(r.focus, 0xb3),
		"contrastBorder":            a(r.border, 0x00),
		"widget.shadow":             r.shadow,
		"widget.border":             r.border,
		"selection.background":      a(r.selection, 0xcc),
		"sash.hoverBorder":          r.accent,
		"textLink.foreground":       r.info,
		"textLink.activeForeground": r.accentHi,
		"textPreformat.foreground":  r.accentHi,
		"textPreformat.background":  a(r.surface, 0x80),
		"textBlockQuote.background": r.deep,
		"textBlockQuote.border":     r.accent,
		"textCodeBlock.background":  r.deep,
		"textSeparator.foreground":  r.border,
		"progressBar.background":    r.accent,
		"badge.background":          r.accent,
		"badge.foreground":          r.onAccent,

		"button.background":                 r.accent,
		"button.foreground":                 r.onAccent,
		"button.hoverBackground":            mixColour(r.accent, r.fg, 0.18),
		"button.separator":                  a(r.onAccent, 0x66),
		"button.secondaryBackground":        r.surface,
		"button.secondaryForeground":        r.fg,
		"button.secondaryHoverBackground":   r.hover,
		"checkbox.background":               r.deep,
		"checkbox.border":                   r.border,
		"checkbox.foreground":               r.fg,
		"dropdown.background":               r.sidebar,
		"dropdown.listBackground":           r.sidebar,
		"dropdown.border":                   r.border,
		"dropdown.foreground":               r.fg,
		"input.background":                  r.deep,
		"input.foreground":                  r.fg,
		"input.border":                      r.border,
		"input.placeholderForeground":       r.faint,
		"inputOption.activeBorder":          r.accent,
		"inputOption.activeBackground":      a(r.accent, 0x33),
		"inputOption.activeForeground":      r.fg,
		"inputValidation.errorBackground":   mixColour(r.bg, r.red, 0.2),
		"inputValidation.errorBorder":       r.red,
		"inputValidation.warningBackground": mixColour(r.bg, r.yellow, 0.2),
		"inputValidation.warningBorder":     r.yellow,
		"inputValidation.infoBackground":    mixColour(r.bg, r.info, 0.2),
		"inputValidation.infoBorder":        r.info,
		"keybindingLabel.background":        a(r.subtle, 0x40),
		"keybindingLabel.foreground":        r.fg,
		"keybindingLabel.border":            r.border,
		"keybindingLabel.bottomBorder":      r.border,
		"scrollbar.shadow":                  r.shadow,
		"scrollbarSlider.background":        a(r.subtle, 0x55),
		"scrollbarSlider.hoverBackground":   a(r.subtle, 0x99),
		"scrollbarSlider.activeBackground":  a(r.accent, 0x99),

		"editor.background":                   r.bg,
		"editor.foreground":                   r.fg,
		"editorCursor.foreground":             r.accent,
		"editorCursor.background":             r.bg,
		"editor.lineHighlightBackground":      r.line,
		"editor.lineHighlightBorder":          a(r.line, 0x00),
		"editor.selectionBackground":          a(r.selection, vscodeSelectionAlpha),
		"editor.selectionForeground":          r.fg,
		"editor.inactiveSelectionBackground":  a(r.selection, 0x80),
		"editor.selectionHighlightBackground": a(r.selection, 0x66),
		"editor.wordHighlightBackground": a(
			r.info,
			0x26,
		),
		"editor.wordHighlightStrongBackground":                a(r.accent, 0x30),
		"editor.findMatchBackground":                          a(r.yellow, 0x59),
		"editor.findMatchBorder":                              r.yellow,
		"editor.findMatchHighlightBackground":                 a(r.yellow, 0x26),
		"editor.findRangeHighlightBackground":                 a(r.surface, 0x66),
		"editor.rangeHighlightBackground":                     a(r.surface, 0x55),
		"editor.foldBackground":                               a(r.surface, 0x40),
		"editor.hoverHighlightBackground":                     a(r.info, 0x20),
		"editor.stackFrameHighlightBackground":                a(r.yellow, 0x26),
		"editor.focusedStackFrameHighlightBackground":         a(r.green, 0x26),
		"editorLink.activeForeground":                         r.info,
		"editorLineNumber.foreground":                         r.faint,
		"editorLineNumber.activeForeground":                   r.accentHi,
		"editorIndentGuide.background1":                       a(r.subtle, 0x40),
		"editorIndentGuide.activeBackground1":                 a(r.subtle, 0xb3),
		"editorWhitespace.foreground":                         a(r.subtle, 0x66),
		"editorRuler.foreground":                              a(r.subtle, 0x55),
		"editorCodeLens.foreground":                           r.faint,
		"editorLightBulb.foreground":                          r.yellow,
		"editorLightBulbAutoFix.foreground":                   r.info,
		"editorInlayHint.foreground":                          r.muted,
		"editorInlayHint.background":                          a(r.surface, 0x80),
		"editorGhostText.foreground":                          r.faint,
		"editorUnnecessaryCode.opacity":                       "#000000aa",
		"editorBracketMatch.background":                       a(r.accent, 0x26),
		"editorBracketMatch.border":                           a(r.accent, 0x99),
		"editorBracketHighlight.foreground1":                  r.accent,
		"editorBracketHighlight.foreground2":                  r.info,
		"editorBracketHighlight.foreground3":                  r.yellow,
		"editorBracketHighlight.foreground4":                  r.green,
		"editorBracketHighlight.foreground5":                  r.purple,
		"editorBracketHighlight.foreground6":                  r.orange,
		"editorBracketHighlight.unexpectedBracket.foreground": r.red,
		"editorError.foreground":                              r.red,
		"editorWarning.foreground":                            r.yellow,
		"editorInfo.foreground":                               r.info,
		"editorHint.foreground":                               r.green,
		"problemsErrorIcon.foreground":                        r.red,
		"problemsWarningIcon.foreground":                      r.yellow,
		"problemsInfoIcon.foreground":                         r.info,
		"editorGutter.background":                             r.bg,
		"editorGutter.addedBackground":                        r.green,
		"editorGutter.modifiedBackground":                     r.info,
		"editorGutter.deletedBackground":                      r.red,
		"editorGutter.foldingControlForeground":               r.muted,
		"editorOverviewRuler.border":                          a(r.border, 0x00),
		"editorOverviewRuler.errorForeground":                 r.red,
		"editorOverviewRuler.warningForeground":               r.yellow,
		"editorOverviewRuler.infoForeground":                  r.info,
		"editorOverviewRuler.addedForeground":                 a(r.green, 0x99),
		"editorOverviewRuler.modifiedForeground":              a(r.info, 0x99),
		"editorOverviewRuler.deletedForeground":               a(r.red, 0x99),
		"editorOverviewRuler.findMatchForeground":             a(r.yellow, 0x99),
		"editorOverviewRuler.selectionHighlightForeground":    a(r.selection, 0xcc),
		"editorStickyScroll.background":                       r.bg,
		"editorStickyScrollHover.background":                  r.sidebar,
		"editorStickyScroll.shadow":                           r.shadow,

		"editorWidget.background":                      r.sidebar,
		"editorWidget.foreground":                      r.fg,
		"editorWidget.border":                          r.border,
		"editorSuggestWidget.background":               r.sidebar,
		"editorSuggestWidget.border":                   r.border,
		"editorSuggestWidget.foreground":               r.fg,
		"editorSuggestWidget.selectedBackground":       r.selection,
		"editorSuggestWidget.selectedForeground":       r.fg,
		"editorSuggestWidget.highlightForeground":      r.accent,
		"editorSuggestWidget.focusHighlightForeground": r.accentHi,
		"editorHoverWidget.background":                 r.sidebar,
		"editorHoverWidget.border":                     r.border,
		"editorHoverWidget.foreground":                 r.fg,
		"editorHoverWidget.statusBarBackground":        r.deep,
		"peekView.border":                              r.accent,
		"peekViewEditor.background":                    r.deep,
		"peekViewEditor.matchHighlightBackground":      a(r.yellow, 0x40),
		"peekViewResult.background":                    r.sidebar,
		"peekViewResult.selectionBackground":           r.selection,
		"peekViewResult.selectionForeground":           r.fg,
		"peekViewResult.matchHighlightBackground":      a(r.yellow, 0x40),
		"peekViewResult.fileForeground":                r.fg,
		"peekViewResult.lineForeground":                r.soft,
		"peekViewTitle.background":                     r.sidebar,
		"peekViewTitleLabel.foreground":                r.fg,
		"peekViewTitleDescription.foreground":          r.muted,
		"diffEditor.insertedLineBackground":            a(r.diffAdded, 0xcc),
		"diffEditor.removedLineBackground":             a(r.diffRemoved, 0xcc),
		"diffEditor.insertedTextBackground":            a(r.diffAddWord, 0xaa),
		"diffEditor.removedTextBackground":             a(r.diffWord, 0xaa),
		"diffEditor.diagonalFill":                      a(r.subtle, 0x55),
		"diffEditorGutter.insertedLineBackground":      a(r.diffAdded, 0xcc),
		"diffEditorGutter.removedLineBackground":       a(r.diffRemoved, 0xcc),
		"merge.currentHeaderBackground":                a(r.green, 0x55),
		"merge.currentContentBackground":               a(r.green, 0x22),
		"merge.incomingHeaderBackground":               a(r.info, 0x55),
		"merge.incomingContentBackground":              a(r.info, 0x22),
		"merge.border":                                 r.border,

		"sideBar.background":                          r.sidebar,
		"sideBar.foreground":                          r.soft,
		"sideBar.border":                              r.border,
		"sideBar.dropBackground":                      a(r.accent, 0x22),
		"sideBarTitle.foreground":                     r.fg,
		"sideBarSectionHeader.background":             r.sidebar,
		"sideBarSectionHeader.foreground":             r.fg,
		"sideBarSectionHeader.border":                 r.border,
		"activityBar.border":                          r.border,
		"titleBar.border":                             r.border,
		"statusBar.border":                            r.border,
		"statusBar.noFolderBackground":                r.surface,
		"statusBar.noFolderForeground":                r.fg,
		"statusBarItem.activeBackground":              r.hover,
		"statusBarItem.prominentBackground":           r.accent,
		"statusBarItem.prominentForeground":           r.onAccent,
		"statusBarItem.errorBackground":               r.red,
		"statusBarItem.errorForeground":               onColour(r.red, r.deep, r.fg),
		"editorGroup.border":                          r.border,
		"editorGroup.dropBackground":                  a(r.accent, 0x22),
		"editorGroupHeader.tabsBackground":            r.deep,
		"editorGroupHeader.tabsBorder":                r.deep,
		"editorGroupHeader.noTabsBackground":          r.bg,
		"editorPane.background":                       r.bg,
		"tab.activeBackground":                        r.bg,
		"tab.activeForeground":                        r.fg,
		"tab.activeBorder":                            r.bg,
		"tab.activeBorderTop":                         r.accent,
		"tab.inactiveBackground":                      r.deep,
		"tab.inactiveForeground":                      r.muted,
		"tab.hoverBackground":                         mixColour(r.deep, r.bg, 0.6),
		"tab.hoverForeground":                         r.fg,
		"tab.border":                                  r.deep,
		"tab.unfocusedActiveBackground":               r.bg,
		"tab.unfocusedActiveForeground":               r.soft,
		"tab.unfocusedActiveBorderTop":                a(r.accent, 0x66),
		"tab.unfocusedInactiveForeground":             r.faint,
		"tab.activeModifiedBorder":                    r.accent,
		"tab.inactiveModifiedBorder":                  a(r.accent, 0x80),
		"breadcrumb.background":                       r.bg,
		"breadcrumb.foreground":                       r.muted,
		"breadcrumb.focusForeground":                  r.fg,
		"breadcrumb.activeSelectionForeground":        r.accent,
		"breadcrumbPicker.background":                 r.sidebar,
		"panel.background":                            r.bg,
		"panel.border":                                r.border,
		"panel.dropBorder":                            r.accent,
		"panelTitle.activeForeground":                 r.fg,
		"panelTitle.activeBorder":                     r.accent,
		"panelTitle.inactiveForeground":               r.muted,
		"panelInput.border":                           r.border,
		"panelSection.border":                         r.border,
		"panelSectionHeader.background":               r.sidebar,
		"terminal.border":                             r.border,
		"terminalCommandDecoration.defaultBackground": r.subtle,
		"terminalCommandDecoration.successBackground": r.green,
		"terminalCommandDecoration.errorBackground":   r.red,

		"list.activeSelectionBackground":       r.selection,
		"list.activeSelectionForeground":       r.fg,
		"list.activeSelectionIconForeground":   r.fg,
		"list.inactiveSelectionBackground":     a(r.selection, 0x99),
		"list.inactiveSelectionForeground":     r.fg,
		"list.hoverBackground":                 a(r.surface, 0x99),
		"list.hoverForeground":                 r.fg,
		"list.focusBackground":                 a(r.selection, 0xcc),
		"list.focusForeground":                 r.fg,
		"list.focusOutline":                    a(r.focus, 0x99),
		"list.inactiveFocusOutline":            a(r.focus, 0x4d),
		"list.highlightForeground":             r.accent,
		"list.focusHighlightForeground":        r.accentHi,
		"list.dropBackground":                  a(r.accent, 0x22),
		"list.errorForeground":                 r.red,
		"list.warningForeground":               r.yellow,
		"list.invalidItemForeground":           r.red,
		"list.deemphasizedForeground":          r.faint,
		"listFilterWidget.background":          r.sidebar,
		"listFilterWidget.outline":             r.accent,
		"listFilterWidget.noMatchesOutline":    r.red,
		"tree.indentGuidesStroke":              a(r.subtle, 0x99),
		"tree.inactiveIndentGuidesStroke":      a(r.subtle, 0x4d),
		"quickInput.background":                r.sidebar,
		"quickInput.foreground":                r.fg,
		"quickInputTitle.background":           r.deep,
		"quickInputList.focusBackground":       r.selection,
		"quickInputList.focusForeground":       r.fg,
		"quickInputList.focusIconForeground":   r.fg,
		"pickerGroup.foreground":               r.accent,
		"pickerGroup.border":                   r.border,
		"menu.background":                      r.sidebar,
		"menu.foreground":                      r.fg,
		"menu.selectionBackground":             r.selection,
		"menu.selectionForeground":             r.fg,
		"menu.border":                          r.border,
		"menu.separatorBackground":             r.border,
		"menubar.selectionBackground":          a(r.surface, 0x99),
		"menubar.selectionForeground":          r.fg,
		"notifications.background":             r.sidebar,
		"notifications.foreground":             r.fg,
		"notifications.border":                 r.border,
		"notificationCenterHeader.background":  r.deep,
		"notificationCenterHeader.foreground":  r.fg,
		"notificationLink.foreground":          r.info,
		"notificationToast.border":             r.border,
		"notificationsErrorIcon.foreground":    r.red,
		"notificationsWarningIcon.foreground":  r.yellow,
		"notificationsInfoIcon.foreground":     r.info,
		"debugToolBar.background":              r.sidebar,
		"debugIcon.breakpointForeground":       r.red,
		"debugIcon.startForeground":            r.green,
		"settings.headerForeground":            r.fg,
		"settings.modifiedItemIndicator":       r.accent,
		"settings.focusedRowBackground":        a(r.surface, 0x55),
		"settings.rowHoverBackground":          a(r.surface, 0x33),
		"welcomePage.tileBackground":           r.sidebar,
		"welcomePage.tileHoverBackground":      r.surface,
		"walkThrough.embeddedEditorBackground": r.deep,
		"minimap.selectionHighlight":           r.selection,
		"minimap.findMatchHighlight":           r.yellow,
		"minimap.errorHighlight":               r.red,
		"minimap.warningHighlight":             r.yellow,
		"minimapSlider.background":             a(r.subtle, 0x40),
		"minimapSlider.hoverBackground":        a(r.subtle, 0x66),
		"minimapSlider.activeBackground":       a(r.accent, 0x66),
		"minimapGutter.addedBackground":        r.green,
		"minimapGutter.modifiedBackground":     r.info,
		"minimapGutter.deletedBackground":      r.red,

		"gitDecoration.addedResourceForeground":         r.green,
		"gitDecoration.modifiedResourceForeground":      r.yellow,
		"gitDecoration.deletedResourceForeground":       r.red,
		"gitDecoration.renamedResourceForeground":       r.cyan,
		"gitDecoration.untrackedResourceForeground":     mixColour(r.green, r.fg, 0.3),
		"gitDecoration.ignoredResourceForeground":       r.faint,
		"gitDecoration.conflictingResourceForeground":   r.purple,
		"gitDecoration.submoduleResourceForeground":     r.cyan,
		"gitDecoration.stageModifiedResourceForeground": r.yellow,
		"gitDecoration.stageDeletedResourceForeground":  r.red,
		"charts.foreground":                             r.fg,
		"charts.lines":                                  r.subtle,
		"charts.red":                                    r.red,
		"charts.blue":                                   r.blue,
		"charts.yellow":                                 r.yellow,
		"charts.orange":                                 r.orange,
		"charts.green":                                  r.green,
		"charts.purple":                                 r.purple,
	}
}

// vscodeSyntaxColors are the workbench keys painted in the syntax roles: the
// outline's and the suggest list's symbol icons, each in its code's colour.
func vscodeSyntaxColors(r vscodeRoles, s vscodeSyntax) map[string]string {
	return map[string]string{
		"symbolIcon.classForeground": s.typ, "symbolIcon.interfaceForeground": s.typ,
		"symbolIcon.structForeground": s.typ, "symbolIcon.typeParameterForeground": s.typ,
		"symbolIcon.enumeratorForeground": s.typ, "symbolIcon.enumeratorMemberForeground": s.constant,
		"symbolIcon.constantForeground": s.constant, "symbolIcon.numberForeground": s.number,
		"symbolIcon.booleanForeground": s.language, "symbolIcon.nullForeground": s.language,
		"symbolIcon.functionForeground": s.function, "symbolIcon.methodForeground": s.function,
		"symbolIcon.constructorForeground": s.function, "symbolIcon.eventForeground": s.decorator,
		"symbolIcon.variableForeground": s.plain, "symbolIcon.fieldForeground": s.property,
		"symbolIcon.propertyForeground": s.property, "symbolIcon.keyForeground": s.property,
		"symbolIcon.keywordForeground": s.keyword, "symbolIcon.operatorForeground": s.operator,
		"symbolIcon.stringForeground": s.str, "symbolIcon.textForeground": s.plain,
		"symbolIcon.namespaceForeground": s.namespace, "symbolIcon.moduleForeground": s.namespace,
		"symbolIcon.packageForeground": s.namespace, "symbolIcon.fileForeground": r.soft,
		"symbolIcon.folderForeground": r.soft, "symbolIcon.arrayForeground": s.punctuation,
		"symbolIcon.objectForeground": s.punctuation, "symbolIcon.colorForeground": s.attribute,
		"symbolIcon.referenceForeground": s.link, "symbolIcon.snippetForeground": s.escape,
		"symbolIcon.unitForeground": s.number,
	}
}

// rule is one tokenColors rule; style is its fontStyle, "" for none set.
func rule(name, foreground, style string, scopes ...string) VSCodeTokenColor {
	settings := VSCodeTokenSettings{Foreground: foreground}
	if style != "" {
		if style == "-" {
			style = ""
		}
		settings.FontStyle = &style
	}
	return VSCodeTokenColor{Name: name, Scope: scopes, Settings: settings}
}

// vscodeTokenRules are the TextMate rules, broad scopes first so the narrow
// ones after them win where VS Code finds both equally specific.
func vscodeTokenRules(s vscodeSyntax) []VSCodeTokenColor {
	return []VSCodeTokenColor{
		rule("Comment", s.comment, "italic", "comment", "punctuation.definition.comment",
			"string.comment"),
		rule("Doc comment tag", s.decorator, "italic", "comment keyword.codetag", "storage.type.class.jsdoc",
			"comment.block.documentation entity.name.type"),
		// keyword.type.go is Go's `type` declaration keyword, which the
		// Primitive type rule's keyword.type would otherwise claim.
		rule("Keyword", s.keyword, "", "keyword", "keyword.control", "keyword.other", "storage.type.function",
			"keyword.declaration", "keyword.type.go"),
		rule("Storage", s.storage, "italic", "storage", "storage.type", "storage.modifier",
			"keyword.storage"),
		rule("Operator", s.operator, "-", "keyword.operator", "punctuation.accessor",
			"punctuation.separator.key-value", "keyword.operator.assignment", "storage.type.function.arrow"),
		rule("Punctuation", s.punctuation, "", "punctuation", "meta.brace", "punctuation.separator",
			"punctuation.terminator", "punctuation.section"),
		rule("String", s.str, "", "string", "string.quoted", "string.template",
			"punctuation.definition.string"),
		rule("String escape", s.escape, "", "constant.character.escape", "constant.character",
			"string.regexp constant.character.escape"),
		rule("Regular expression", s.regex, "", "string.regexp", "source.regexp"),
		rule("Template expression", s.keyword, "",
			"punctuation.definition.template-expression", "punctuation.section.embedded"),
		rule("Number", s.number, "", "constant.numeric", "keyword.other.unit"),
		rule("Constant", s.constant, "", "constant", "variable.other.constant", "variable.other.enummember",
			"entity.name.constant", "support.constant"),
		rule("Language constant", s.language, "", "constant.language", "constant.language.boolean",
			"constant.language.null", "constant.language.undefined"),
		rule("Variable", s.plain, "", "variable", "variable.other", "meta.definition.variable.name"),
		rule("Language variable", s.self, "italic", "variable.language", "variable.language.this",
			"variable.language.self", "variable.language.super"),
		rule("Parameter", s.parameter, "italic", "variable.parameter", "meta.parameter",
			"entity.name.variable.parameter"),
		rule("Property", s.property, "", "variable.other.property", "variable.other.object.property",
			"support.type.property-name", "meta.object-literal.key", "entity.name.tag.yaml",
			"support.type.property-name.json", "variable.object.property"),
		rule("Function", s.function, "", "entity.name.function", "meta.function-call entity.name.function",
			"variable.function", "support.function.any-method", "meta.function-call.generic"),
		rule("Built-in function", s.builtin, "", "support.function", "support.function.builtin",
			"entity.name.function.builtin", "keyword.other.special-method"),
		rule("Type", s.typ, "", "entity.name.type", "entity.name.class", "entity.name.struct",
			"entity.name.interface", "entity.name.enum", "entity.other.inherited-class", "support.type",
			"support.class", "storage.type.primitive", "storage.type.built-in", "entity.name.type.parameter"),
		rule("Primitive type", s.typ, "-", "support.type.primitive", "storage.type.primitive",
			"keyword.type"),
		rule("Namespace", s.namespace, "", "entity.name.namespace", "entity.name.module",
			"entity.name.package", "entity.name.type.module", "storage.modifier.import", "support.other.namespace"),
		rule("Decorator", s.decorator, "", "entity.name.function.decorator", "meta.decorator",
			"punctuation.decorator", "meta.attribute", "entity.name.function.macro", "support.function.macro"),
		rule("Tag", s.tag, "", "entity.name.tag", "meta.tag.sgml", "punctuation.definition.tag",
			"support.class.component"),
		rule("Attribute", s.attribute, "italic", "entity.other.attribute-name",
			"entity.other.attribute-name.class.css", "entity.other.attribute-name.id.css"),
		rule("CSS property", s.property, "", "support.type.property-name.css", "meta.property-name.css"),
		rule("CSS value", s.constant, "", "support.constant.property-value.css", "meta.property-value.css",
			"support.constant.color"),
		rule("Invalid", s.invalid, "underline", "invalid", "invalid.illegal"),
		rule("Deprecated", s.invalid, "strikethrough", "invalid.deprecated"),
		rule("Heading", s.heading, "bold", "markup.heading", "entity.name.section",
			"punctuation.definition.heading.markdown"),
		rule("Bold", s.parameter, "bold", "markup.bold"),
		rule("Italic", s.parameter, "italic", "markup.italic"),
		rule("Strikethrough", s.comment, "strikethrough", "markup.strikethrough"),
		rule("Link", s.link, "underline", "markup.underline.link", "string.other.link",
			"constant.other.reference.link"),
		rule("Inline code", s.code, "", "markup.inline.raw", "markup.fenced_code", "markup.raw"),
		rule("Quote", s.comment, "italic", "markup.quote"),
		rule("List marker", s.keyword, "", "punctuation.definition.list.begin.markdown",
			"beginning.punctuation.definition.list"),
		rule("Inserted", s.str, "", "markup.inserted", "punctuation.definition.inserted"),
		rule("Deleted", s.invalid, "", "markup.deleted", "punctuation.definition.deleted"),
		rule("Changed", s.number, "", "markup.changed", "punctuation.definition.changed"),
		rule("Diff header", s.function, "", "meta.diff.header", "meta.diff.range", "meta.diff.index"),
	}
}

// vscodeSemanticRules are the semantic token colours, the same roles the
// TextMate rules paint, so a language server's colours match its grammar's.
func vscodeSemanticRules(s vscodeSyntax) map[string]VSCodeSemanticStyle {
	yes := true
	return map[string]VSCodeSemanticStyle{
		"namespace": {Foreground: s.namespace}, "class": {Foreground: s.typ}, "interface": {Foreground: s.typ},
		"enum": {Foreground: s.typ}, "struct": {Foreground: s.typ}, "type": {Foreground: s.typ},
		"typeParameter": {Foreground: s.typ, Italic: &yes}, "enumMember": {Foreground: s.constant},
		"function": {Foreground: s.function}, "method": {Foreground: s.function},
		"function.defaultLibrary": {Foreground: s.builtin}, "method.defaultLibrary": {Foreground: s.builtin},
		"macro": {Foreground: s.decorator}, "decorator": {Foreground: s.decorator},
		"property": {Foreground: s.property}, "parameter": {Foreground: s.parameter, Italic: &yes},
		"variable": {Foreground: s.plain}, "variable.readonly": {Foreground: s.constant},
		"variable.defaultLibrary": {Foreground: s.builtin}, "property.readonly": {Foreground: s.constant},
		"selfKeyword": {Foreground: s.self, Italic: &yes}, "keyword": {Foreground: s.keyword},
		"string": {Foreground: s.str}, "number": {Foreground: s.number}, "regexp": {Foreground: s.regex},
		"comment": {Foreground: s.comment, Italic: &yes}, "operator": {Foreground: s.operator},
		"*.deprecated": {Strikethrough: &yes},
	}
}
