package harvest

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// A page that states its count of reviews, comments or answers only in its
// visible text ("103 reviews" over the review list) and serves a first view
// of them is stored as if that view were the list. The structured data is
// the reliable statement (stated_gaps.go); a visible count is read only
// where it is tied to the list the page shows: the whole text of one element,
// one number for the noun across the page (a listing's cards each state
// their own, and a site's global counters are no label of a list), and a
// measurable count of the list's items — elements whose id names the item
// (empReview_97441640, comment-1vt6x), ids repeating one prefix (c_05alz2)
// or schema.org items. With no item to
// count, a visible label names nothing: the number is not tied to a list.

// threadLabelPattern is the whole text of a count label: a plain number (with
// thousands separators) and a thread noun, English or one of the languages a
// site serves by the fetch's location, optionally in parentheses.
var threadLabelPattern = regexp.MustCompile(`(?i)^\(?\s*(\d{1,3}(?:[.,\x{a0} ]\d{3})+|\d+)\s+` +
	`(reviews|bewertungen|avis|reseñas|recensioni|recensies|beoordelingen|` +
	`comments|reacties|kommentare|commentaires|comentarios|commenti|` +
	`answers|antwoorden|antworten|réponses|respuestas|risposte)\s*\)?$`)

// The thread nouns a count names, as statedGap names them.
const (
	threadReviews  = "reviews"
	threadComments = "comments"
	threadAnswers  = "answers"
)

// threadLabelWords are the words of a label naming each thread noun.
var threadLabelWords = map[string][]string{
	threadReviews:  {threadReviews, "bewertungen", "avis", "reseñas", "recensioni", "recensies", "beoordelingen"},
	threadComments: {threadComments, "reacties", "kommentare", "commentaires", "comentarios", "commenti"},
	threadAnswers:  {threadAnswers, "antwoorden", "antworten", "réponses", "respuestas", "risposte"},
}

// threadLabelNoun is the thread noun of a label's word.
func threadLabelNoun(word string) string {
	for noun, words := range threadLabelWords {
		if containsFold(words, word) {
			return noun
		}
	}
	return ""
}

// threadItemIDPattern is an element id naming one item of a thread noun:
// the noun, then a token after a separator (comment-2k3j, comment-abcd) or a
// token carrying a digit (empReview97441640). A reply or a post is an item of
// a comment thread.
var threadItemIDPattern = regexp.MustCompile(
	`(?i)(review|comment|answer|reply|post)s?(?:[-_:][a-z0-9]+|[a-z0-9]*\d[a-z0-9]*)$`)

// threadItemNoun is the thread noun an item id's stem counts toward: a
// comment, a reply and a post are all items of a comment thread.
func threadItemNoun(stem string) string {
	switch strings.ToLower(stem) {
	case "review":
		return threadReviews
	case "answer":
		return threadAnswers
	default:
		return threadComments
	}
}

// idPrefixPattern splits an id into its non-digit prefix and an item token
// carrying a digit (c_05alz2 → c_, empReview97441640 → empReview).
var idPrefixPattern = regexp.MustCompile(`(?i)^(.*[-_:]|[a-z]*[a-z])([a-z0-9]*\d[a-z0-9]*)$`)

// threadLabelMaxChars bounds a count label: a longer text is prose.
const threadLabelMaxChars = 40

// threadLabels is what a page's visible text states of its thread nouns and
// how many items of each its markup carries by id, and how many ids share
// each non-digit prefix. A noun whose labels state different numbers (a
// listing's cards) has no stated count.
type threadLabels struct {
	stated map[string]int
	items  map[string]map[string]bool
	groups map[string]map[string]bool
}

// largestGroup is the size of the largest set of ids sharing one prefix, when
// at least 2 share it: a repeated list's items, counted as one group, never
// a sum of unrelated ones.
func (labels threadLabels) largestGroup() int {
	largest := 0
	for _, ids := range labels.groups {
		if len(ids) >= 2 {
			largest = max(largest, len(ids))
		}
	}
	return largest
}

// readThreadLabels reads doc's count labels and item ids.
func readThreadLabels(doc *html.Node) threadLabels {
	labels := threadLabels{
		stated: map[string]int{}, items: map[string]map[string]bool{}, groups: map[string]map[string]bool{},
	}
	conflicting := map[string]bool{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		switch {
		case node.Type == html.ElementNode && (node.DataAtom == atom.Script || node.DataAtom == atom.Style):
			return
		case node.Type == html.ElementNode:
			id := nodeAttr(node, "id")
			if match := threadItemIDPattern.FindStringSubmatch(id); match != nil {
				addID(labels.items, threadItemNoun(match[1]), id)
			}
			if match := idPrefixPattern.FindStringSubmatch(id); match != nil {
				addID(labels.groups, match[1], id)
			}
		case node.Type == html.TextNode:
			text := strings.TrimSpace(node.Data)
			match := threadLabelPattern.FindStringSubmatch(text)
			if len(text) > threadLabelMaxChars || match == nil {
				break
			}
			noun := threadLabelNoun(match[2])
			number, err := strconv.Atoi(strings.Map(keepDigit, match[1]))
			if err != nil { // the pattern admits digits only; an overflow is no count
				break
			}
			if seen, ok := labels.stated[noun]; ok && seen != number {
				conflicting[noun] = true
			}
			labels.stated[noun] = number
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	for noun := range conflicting {
		delete(labels.stated, noun)
	}
	return labels
}

// addID records id under key.
func addID(sets map[string]map[string]bool, key, id string) {
	if sets[key] == nil {
		sets[key] = map[string]bool{}
	}
	sets[key][id] = true
}

func keepDigit(r rune) rune {
	if r >= '0' && r <= '9' {
		return r
	}
	return -1
}

// statedOrLabelled reconciles a noun's count: the structured data's stated
// count against its items when it states one; otherwise the page's count
// label against the items its markup carries, named only when those items
// are measurable and clearly fewer (under half) than the label states. Ids
// measure the items when they carry the noun's stem (one is enough) or when
// at least 2 share one prefix (c_05alz2, c_0102x); the larger count stands,
// so one chrome id naming the noun never outweighs the list beside it.
func statedOrLabelled(stated, carried int, noun string, labels threadLabels) string {
	if stated > 0 {
		return statedGap(stated, carried, noun)
	}
	labelled, ok := labels.stated[noun]
	loaded := max(carried, len(labels.items[noun]), labels.largestGroup())
	if !ok || loaded == 0 || loaded*2 >= labelled {
		return ""
	}
	return statedGap(labelled, loaded, noun)
}
