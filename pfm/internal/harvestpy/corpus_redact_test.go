package harvestpy

import "regexp"

// fixtureAuthorEmail is the invented address the committed oracles carry in
// place of a personal mailbox a source document prints: a paper's author line
// is public, but the repository ships no personal address (leak-check refuses
// one). The byte-exact gate applies the same substitution to live output.
const fixtureAuthorEmail = "author.one@example.invalid"

var personalMailbox = regexp.MustCompile(`[A-Za-z0-9._%+-]+@gmail\.com`)

func redactFixtureEmails(markdown string) string {
	return personalMailbox.ReplaceAllString(markdown, fixtureAuthorEmail)
}
