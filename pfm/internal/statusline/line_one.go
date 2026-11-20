package statusline

import (
	"slices"

	"github.com/charmbracelet/x/ansi"
)

// identityFacts is what line 1 draws, in its order: the account's badge, the
// model and its effort, the directory, the worktree, the git branch, the
// agent, the vim mode, the session's name and the fleet.
type identityFacts struct {
	badge     string
	data      input
	effort    autoEffortState
	directory string
	worktree  string
	git       gitState
	agent     string
	vim       string
	session   string
	fleet     string
}

// identityPart names a part of line 1 a narrow terminal can give up.
type identityPart int

const (
	identityBadge identityPart = iota
	identityDirectory
	identityWorktree
	identityGit
	identityAgent
	identityVim
	identitySession
	identityFleet
)

// identityDrops is the order line 1 gives its parts up in, least missed
// first. The model's glyph and name and the effort's bar are never given up.
var identityDrops = [...]identityPart{
	identitySession, identityFleet, identityAgent, identityVim,
	identityWorktree, identityGit, identityDirectory, identityBadge,
}

// identityRank is how much of line 1 a terminal's width leaves: cut whether
// the names are cut to identityCells, words whether the effort's level word
// stands beside its bar, joint the separator, drops how many of identityDrops
// are given up.
type identityRank struct {
	cut   bool
	words bool
	joint string
	drops int
}

// identityCells is the cells a cut name keeps, its … included.
var identityCells = map[identityPart]int{
	identityDirectory: 16, identityWorktree: 16, identityGit: 16, identityAgent: 12, identitySession: 24,
}

// identityRanks is line 1's ranks, fullest first, in the order every line
// shrinks in: shorter spellings (long names cut with …, the effort's bar
// without its word), then the blanks around each separator, then one part
// after another, least missed first.
var identityRanks = func() []identityRank {
	ranks := []identityRank{
		{words: true, joint: sep},
		{cut: true, words: true, joint: sep},
		{cut: true, joint: sep},
	}
	for drops := range len(identityDrops) + 1 {
		ranks = append(ranks, identityRank{cut: true, joint: sepTight, drops: drops})
	}
	return ranks
}()

// identityLine is line 1 at the fullest rank the terminal's width holds,
// every part whole when the width is unknown, the last rank when none fits.
func identityLine(runtime Runtime, facts identityFacts) string {
	line := ""
	for _, rank := range identityRanks {
		line = facts.draw(rank)
		if runtime.Columns <= 0 || ansi.StringWidth(line) <= runtime.Columns-footerPadding {
			break
		}
	}
	return line
}

// draw is line 1 at one rank.
func (facts identityFacts) draw(rank identityRank) string {
	shown := func(part identityPart) bool {
		return !slices.Contains(identityDrops[:rank.drops], part)
	}
	name := func(part identityPart, text string) string {
		if !rank.cut {
			return text
		}
		return cutName(text, identityCells[part])
	}
	line := ""
	if shown(identityBadge) {
		line = facts.badge
	}
	line += modelSegment(facts.data, facts.effort, rank.words)
	add := func(part identityPart, text string) {
		if text != "" && shown(part) {
			line += rank.joint + text
		}
	}
	add(identityDirectory, blue+name(identityDirectory, facts.directory)+reset)
	if facts.worktree != "" {
		add(identityWorktree, magenta+"🌳 "+name(identityWorktree, facts.worktree)+reset)
	}
	gitCells := 0
	if rank.cut {
		gitCells = identityCells[identityGit]
	}
	add(identityGit, facts.git.text(gitCells))
	if facts.agent != "" {
		add(identityAgent, magenta+"⚡"+name(identityAgent, facts.agent)+reset)
	}
	if facts.vim != "" {
		color := green
		if facts.vim == "NORMAL" {
			color = cyan
		}
		add(identityVim, color+facts.vim+reset)
	}
	if facts.session != "" {
		add(identitySession, cLabel+"🔖 "+name(identitySession, facts.session)+reset)
	}
	add(identityFleet, facts.fleet)
	return line
}

// cutName is text in at most cells cells, cut with … when longer; whole when
// cells is 0.
func cutName(text string, cells int) string {
	if cells <= 0 || ansi.StringWidth(text) <= cells {
		return text
	}
	return ansi.Truncate(text, cells, "…")
}
