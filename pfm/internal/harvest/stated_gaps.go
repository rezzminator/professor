package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// loaderLabelPattern is the whole visible label of a control that loads more
// of a page's thread: "Load more", "Show more comments", "View 3 more
// replies", "801 hidden items", "More answers below", "Show 12 more".
var loaderLabelPattern = regexp.MustCompile(
	`(?i)^(?:load more\b.*` +
		`|(?:show|view|see|load|get) (?:\d[\d,]* )?(?:more|all(?: \d[\d,]*)?) ` +
		`(?:comments?|replies|answers?|reviews?|posts?|responses?|messages?)\b.*` +
		`|\d[\d,]* (?:hidden|more) (?:items?|comments?|replies|answers?|reviews?|posts?)` +
		`|more answers below` +
		`|show \d[\d,]* more\b.*)$`,
)

// loaderLabelMaxChars bounds a loader label: a longer text is prose, not a control.
const loaderLabelMaxChars = 60

// threadGap names what a generic page's own markup says of its thread beyond
// what it loaded — a count its structured data states above the items it
// carries (statedThreadGap), and a loader control left unpressed
// (unpressedLoaders) — or "" when its markup shows neither. Site extractors
// reconcile their own counts; this is the generic path's.
func threadGap(ctx context.Context, doc *html.Node) string {
	return joinReasons(statedThreadGap(ctx, doc), unpressedLoaders(doc))
}

// threadCounts is what a page's schema.org JSON-LD states of its thread and
// the Comment, Review and Answer items it carries.
type threadCounts struct {
	statedComments, statedReviews, statedAnswers int
	comments, reviews, answers                   int
}

// statedThreadGap reads the page's schema.org JSON-LD: commentCount or a
// CommentAction interaction count against its Comment items; reviewCount or a
// ReviewAction interaction count against its Review items, on a page that
// shows reviews; answerCount against its Answer items. A count the structured
// data does not state is read from a count label tied to the page's list
// (stated_labels.go). With no item to count, it never guesses how many loaded.
func statedThreadGap(ctx context.Context, doc *html.Node) string {
	var counts threadCounts
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "script" &&
			strings.EqualFold(strings.TrimSpace(nodeAttr(node, "type")), "application/ld+json") {
			var data any
			if err := json.Unmarshal([]byte(nodeText(node)), &data); err != nil {
				obs.Logger(ctx).
					Info("harvest: a JSON-LD block could not be read for its stated counts", obs.FieldErr, err.Error())
				return
			}
			counts.add(data)
			return
		}
		if node.Type == html.ElementNode {
			counts.addMicrodata(node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	labels := readThreadLabels(doc)
	gaps := []string{
		statedOrLabelled(counts.statedComments, counts.comments, threadComments, labels),
		statedOrLabelled(counts.statedAnswers, counts.answers, threadAnswers, labels),
	}
	if counts.reviews > 0 || showsReviews(doc) {
		gaps = append(gaps, statedOrLabelled(counts.statedReviews, counts.reviews, threadReviews, labels))
	}
	return joinReasons(gaps...)
}

// statedGap is the house reconciliation of a stated count against the items
// the page carries; with no item to count (carried 0) it names no number it
// did not read.
func statedGap(stated, carried int, noun string) string {
	switch {
	case stated <= carried:
		return ""
	case carried == 0:
		return fmt.Sprintf("%d %s stated; the stored page holds only what the first view serves", stated, noun)
	default:
		return fmt.Sprintf("%d %s stated · %d loaded — the rest are not on the served page (paged or loaded on demand)",
			stated, noun, carried)
	}
}

// add walks one JSON-LD value, counting its stated totals and its items.
func (counts *threadCounts) add(value any) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			counts.add(item)
		}
	case map[string]any:
		switch {
		case schemaTyped(typed["@type"], "Comment"):
			counts.comments++
		case schemaTyped(typed["@type"], "Review"):
			counts.reviews++
		case schemaTyped(typed["@type"], "Answer"):
			counts.answers++
		}
		counts.statedComments = max(counts.statedComments, statedNumber(typed["commentCount"]))
		counts.statedReviews = max(counts.statedReviews, statedNumber(typed["reviewCount"]))
		counts.statedAnswers = max(counts.statedAnswers, statedNumber(typed["answerCount"]))
		if interaction, ok := typed["userInteractionCount"]; ok {
			switch {
			case schemaTyped(typed["interactionType"], "CommentAction"):
				counts.statedComments = max(counts.statedComments, statedNumber(interaction))
			case schemaTyped(typed["interactionType"], "ReviewAction"):
				counts.statedReviews = max(counts.statedReviews, statedNumber(interaction))
			}
		}
		for _, child := range typed {
			counts.add(child)
		}
	}
}

// addMicrodata counts one element's schema.org microdata: an itemscope typed
// Comment, Review or Answer is an item; an itemprop commentCount, reviewCount or answerCount states
// a total, in its content attribute or its text.
func (counts *threadCounts) addMicrodata(node *html.Node) {
	if itemType := nodeAttr(node, "itemtype"); itemType != "" {
		counts.add(map[string]any{"@type": itemType})
	}
	var prop string
	switch strings.ToLower(nodeAttr(node, "itemprop")) {
	case "commentcount":
		prop = "commentCount"
	case "reviewcount":
		prop = "reviewCount"
	case "answercount":
		prop = "answerCount"
	default:
		return
	}
	value := nodeAttr(node, "content")
	if value == "" {
		value = nodeText(node)
	}
	counts.add(map[string]any{prop: value})
}

// schemaTyped reports whether a JSON-LD type value — a name, a schema.org
// address, a list of them, or an object carrying @type — names want.
func schemaTyped(value any, want string) bool {
	switch typed := value.(type) {
	case string:
		name := typed[strings.LastIndexAny(typed, "/:")+1:]
		return strings.EqualFold(name, want)
	case []any:
		for _, item := range typed {
			if schemaTyped(item, want) {
				return true
			}
		}
	case map[string]any:
		return schemaTyped(typed["@type"], want)
	}
	return false
}

// statedNumber is a JSON-LD count, a number or a numeric string; 0 when none.
func statedNumber(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		number, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(typed), ",", ""))
		if err != nil {
			return 0
		}
		return number
	}
	return 0
}

// showsReviews reports whether the page's body text speaks of reviews: a
// stated review count names a gap only on a page that renders reviews.
func showsReviews(doc *html.Node) bool {
	body := firstElement(doc, "body")
	return body != nil && strings.Contains(strings.ToLower(nodeText(body)), "review")
}

// unpressedLoaders names the loader controls left in the page — a button or
// link whose whole visible label is a load-more phrase; prose never counts.
func unpressedLoaders(doc *html.Node) string {
	var labels []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode &&
			(node.Data == "button" || node.Data == "a" || strings.EqualFold(nodeAttr(node, "role"), "button")) {
			label := nodeText(node)
			matched := strings.TrimRight(label, "…. ")
			if len(label) <= loaderLabelMaxChars && loaderLabelPattern.MatchString(matched) &&
				!containsFold(labels, label) {
				labels = append(labels, label)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	switch len(labels) {
	case 0:
		return ""
	case 1:
		return "the page has a '" + labels[0] + "' control that was not pressed"
	default:
		return "the page has '" + strings.Join(labels, "', '") + "' controls that were not pressed"
	}
}

func containsFold(labels []string, label string) bool {
	for _, seen := range labels {
		if strings.EqualFold(seen, label) {
			return true
		}
	}
	return false
}
