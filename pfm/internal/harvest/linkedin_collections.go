package harvest

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The LinkedIn collection pages: a newsletter (/newsletters/<slug>), a
// top-content article or topic (/top-content/…), a Learning course
// (/learning/<course>), an embedded post (/embed/feed/update/<urn>) and the
// guest job-posting fragment (/jobs-guest/jobs/api/jobPosting/<id>). A
// newsletter, an embed and a guest posting state no JSON-LD, so their markup
// is read; a top-content page lists its posts in a CollectionPage's hasPart,
// and a course states its instructor, videos and syllabus in a Course. A
// collection names no publication date of its own — a newsletter's dates are
// its editions', a top-content page's its posts' — so none renders one. A
// course's videos need a Learning subscription, so a course is paywalled
// where every other page carries the login wall. A page without the markup
// its address promises (the /authwall page) falls through to the generic path.

// linkedInLearningPages are the /learning/<segment> pages that are no course:
// the catalog, search, paths, topics, subscription and instructor pages.
var linkedInLearningPages = map[string]bool{
	"browse": true, "search": true, "paths": true, "topics": true, "subscription": true, "instructors": true,
}

// linkedInISODuration is a JSON-LD duration, "PT1H5M30S".
var linkedInISODuration = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)(?:\.\d+)?S)?)?$`)

// linkedInCollectionKind reads the kind of a collection page's address:
// linkedInNone for every other address.
func linkedInCollectionKind(page *url.URL) linkedInPageKind {
	segment := func(prefix string) string {
		rest, ok := strings.CutPrefix(page.Path, prefix)
		if !ok {
			return ""
		}
		return strings.Trim(rest, "/")
	}
	switch {
	case segment("/embed/feed/update/") != "":
		return linkedInEmbed
	case segment("/jobs-guest/jobs/api/jobPosting/") != "":
		return linkedInGuestJob
	case segment("/top-content/") != "":
		return linkedInTopContent
	}
	if slug := segment("/newsletters/"); slug != "" && !strings.Contains(slug, "/") {
		return linkedInNewsletter
	}
	if slug := segment("/learning/"); slug != "" && !strings.Contains(slug, "/") && !linkedInLearningPages[slug] {
		return linkedInCourse
	}
	return linkedInNone
}

// linkedInNewsletterPage renders a newsletter: its name, tagline, schedule
// and subscribers as shown, its author by name (the headline on a line of its
// own) and its editions. False when the page holds no newsletter card.
func linkedInNewsletterPage(doc *html.Node, address string) (string, bool) {
	name := linkedInClassText(doc, "top-card-layout__title")
	author := firstClass(doc, "newsletter__author-container")
	editions := firstClass(doc, "newsletter__editions-container")
	if name == "" || (author == nil && editions == nil) {
		return "", false
	}
	var out strings.Builder
	out.WriteString("# " + name + "\n\n")
	if tagline := linkedInClassText(doc, "top-card-layout__headline"); tagline != "" {
		out.WriteString(tagline + "\n\n")
	}
	var shown []string
	for _, class := range []string{"top-card-layout__first-subline", "top-card-layout__second-subline"} {
		if node := firstClass(doc, class); node != nil {
			shown = append(shown, linkedInPieces(node)...)
		}
	}
	if len(shown) > 0 {
		out.WriteString(strings.Join(shown, " · ") + "  \n")
	}
	if author != nil {
		if label := linkedInClassText(author, "base-main-card__title"); label != "" {
			if link := firstElement(author, "a"); link != nil && nodeAttr(link, "href") != "" {
				label = "[" + label + "](" + linkedInCleanURL(nodeAttr(link, "href")) + ")"
			}
			out.WriteString("**Author:** " + label + "  \n")
		}
		if headline := linkedInClassText(author, "base-main-card__subtitle"); headline != "" {
			out.WriteString("**Author headline:** " + headline + "  \n")
		}
	}
	out.WriteString("**Newsletter:** " + address + "\n\n")
	if editions != nil {
		var lines []string
		for _, card := range classElements(editions, "share-update-card") {
			title, link := linkedInClassText(card, "share-article__title"), ""
			if anchor := firstClass(card, "share-article__title-link"); anchor != nil {
				link = linkedInCleanURL(nodeAttr(anchor, "href"))
			}
			date := ""
			if header := firstClass(card, "share-update-card-header"); header != nil {
				if stamp := firstElement(header, "h6"); stamp != nil {
					date = strings.TrimPrefix(nodeText(stamp), "Published on ")
				}
			}
			if line := linkedInJoin(" · ", title, date, link); line != "" {
				lines = append(lines, "- "+line)
			}
		}
		out.WriteString("## Editions\n\n")
		if len(lines) == 0 {
			out.WriteString("*The page lists no editions.*\n")
		} else {
			out.WriteString(strings.Join(lines, "\n") + "\n")
		}
	}
	return out.String(), true
}

// ldCollection is a top-content page's CollectionPage and the posts it lists.
type ldCollection struct {
	Name  ldText            `json:"name"`
	Posts ldList[ldPosting] `json:"hasPart"`
}

// linkedInTopContentPage renders a top-content article or topic: its title,
// the summary when the page has one, then each post it lists — author, day,
// reactions, an excerpt and the post's link. A collection states no date of
// its own.
func linkedInTopContentPage(doc *html.Node, address string, entities []ldEntity, renderer markdownRenderer) (
	string, bool, error,
) {
	var collection ldCollection
	if found, err := ldDecode(entities, "CollectionPage", &collection); !found {
		return "", false, err
	}
	title := linkedInClassText(doc, "core-section-container__main-title")
	if title == "" {
		title = string(collection.Name)
	}
	var out strings.Builder
	out.WriteString("# " + title + "\n\n**Page:** " + address + "\n\n")
	if summary := firstClass(doc, "ai-summary"); summary != nil {
		if blocks := renderer.blocks(summary); len(blocks) > 0 {
			out.WriteString("## Summary\n\n" + strings.Join(blocks, "\n\n") + "\n\n")
		}
	}
	out.WriteString("## Posts\n\n")
	if len(collection.Posts) == 0 {
		out.WriteString("*The page lists no posts.*\n")
	}
	for _, post := range collection.Posts {
		author := linkedInAuthor(post.Author)
		for _, named := range post.Author {
			if named.Name != "" && named.URL != "" {
				author = "[" + string(named.Name) + "](" + linkedInCleanURL(string(named.URL)) + ")"
				break
			}
		}
		reactions := ""
		if count, ok := ldCount(post.Stats, "LikeAction"); ok {
			reactions = strconv.Itoa(count) + " reactions"
		}
		body := string(post.ArticleBody)
		if body == "" {
			body = string(post.Text)
		}
		out.WriteString("- " + linkedInJoin(" · ", author, linkedInDay(string(post.DatePublished)), reactions))
		if excerpt := linkedInTrim(strings.Join(strings.Fields(body), " "), linkedInExcerptRunes); excerpt != "" {
			out.WriteString("  \n  " + excerpt)
		}
		if post.URL != "" {
			out.WriteString("  \n  " + linkedInCleanURL(string(post.URL)))
		}
		out.WriteString("\n")
	}
	return out.String(), true, nil
}

// linkedInEmbedPage renders an embedded post: its actor, the date as shown,
// its counts and its text. False when the page holds no post card.
func linkedInEmbedPage(doc *html.Node, address string) (string, bool) {
	lockup := linkedInTestID(doc, "entity-lockup")
	commentary := linkedInTestID(doc, "commentary")
	if lockup == nil && commentary == nil {
		return "", false
	}
	actor, posted := "", ""
	if lockup != nil {
		for _, link := range elementsByTag(lockup, atom.A) {
			if actor = nodeText(link); actor != "" {
				break
			}
		}
		for _, stamp := range elementsByTag(lockup, atom.Time) {
			if posted = nodeText(stamp); posted != "" {
				break
			}
		}
	}
	if actor == "" {
		actor = unknownAuthor
	}
	body := ""
	if commentary != nil {
		body = rawText(commentary)
	}
	link := address
	if reactions := firstWithAttr(doc, "data-test-id", "social-actions__reactions", nil); reactions != nil &&
		nodeAttr(reactions, "href") != "" {
		link = linkedInCleanURL(nodeAttr(reactions, "href"))
	}
	reactions, comments := "", ""
	if node := firstWithAttr(doc, "data-test-id", "social-actions__reaction-count", nil); node != nil {
		reactions = nodeText(node)
	}
	if node := firstWithAttr(doc, "data-test-id", "social-actions__comments", nil); node != nil {
		if fields := strings.Fields(nodeText(node)); len(fields) > 0 {
			comments = fields[0]
		}
	}
	var out strings.Builder
	out.WriteString("# " + linkedInHeading(body, "") + "\n\n")
	out.WriteString(linkedInMeta("Author", actor, "Posted", posted) + "  \n")
	out.WriteString("**Post:** " + link)
	if counts := linkedInMeta("Reactions", reactions, "Comments", comments); counts != "" {
		out.WriteString("  \n" + counts)
	}
	out.WriteString("\n\n")
	if paragraphs := linkedInParagraphs(body); len(paragraphs) > 0 {
		out.WriteString(strings.Join(paragraphs, "\n\n") + "\n")
	}
	return out.String(), true
}

// linkedInGuestJobPage renders the guest job-posting fragment as a job view:
// its title, company, location, posted time and applicants as shown, the
// criteria and the description. False when the fragment holds no job title.
func linkedInGuestJobPage(doc *html.Node, address string, renderer markdownRenderer) (string, bool) {
	title := linkedInClassText(doc, "top-card-layout__title")
	if title == "" {
		return "", false
	}
	location := ""
	for _, node := range classElements(doc, "topcard__flavor--bullet") {
		if !hasClass(node, "topcard__flavor--metadata") {
			location = nodeText(node)
			break
		}
	}
	link := address
	if anchor := firstClass(doc, "topcard__link"); anchor != nil && nodeAttr(anchor, "href") != "" {
		link = linkedInCleanURL(nodeAttr(anchor, "href"))
	}
	var out strings.Builder
	out.WriteString("# " + title + "\n\n")
	for _, line := range []string{
		linkedInMeta("Company", linkedInClassText(doc, "topcard__org-name-link"), "Location", location),
		linkedInMeta("Posted", linkedInClassText(doc, "posted-time-ago__text"),
			"Applicants", linkedInClassText(doc, "num-applicants__caption")),
		linkedInMeta("Salary", linkedInClassText(doc, "compensation__salary")),
	} {
		if line != "" {
			out.WriteString(line + "  \n")
		}
	}
	out.WriteString("**Job:** " + link + "\n\n")
	if criteria := linkedInCriteria(doc); len(criteria) > 0 {
		out.WriteString("## Criteria\n\n" + strings.Join(criteria, "\n") + "\n\n")
	}
	out.WriteString("## Description\n\n")
	var blocks []string
	if markup := firstClass(doc, "show-more-less-html__markup"); markup != nil {
		for _, block := range renderer.blocks(markup) {
			if block = strings.TrimSpace(linkedInBreakRuns.ReplaceAllString(block, "\n\n")); block != "" {
				blocks = append(blocks, block)
			}
		}
	}
	if len(blocks) == 0 {
		out.WriteString("*The page states no description.*\n")
	} else {
		out.WriteString(strings.Join(blocks, "\n\n") + "\n")
	}
	return out.String(), true
}

// linkedInCriteria is a job's criteria list as the page shows it, one
// "- **Name:** value" line per item.
func linkedInCriteria(doc *html.Node) []string {
	var criteria []string
	for _, item := range classElements(doc, "description__job-criteria-item") {
		name := linkedInClassText(item, "description__job-criteria-subheader")
		value := linkedInClassText(item, "description__job-criteria-text")
		if name != "" && value != "" {
			criteria = append(criteria, "- **"+name+":** "+value)
		}
	}
	return criteria
}

type ldVideo struct {
	Name     ldText `json:"name"`
	Duration ldText `json:"duration"`
}

type ldCourseInstance struct {
	Instructor ldList[ldThing] `json:"instructor"`
}

type ldRating struct {
	Value ldText `json:"ratingValue"`
	Count ldText `json:"ratingCount"`
	Best  ldText `json:"bestRating"`
}

// ldCourse is a Learning course: its instructors, videos (hasPart, an array
// per chapter), syllabus, skills (about), enrollment and rating.
type ldCourse struct {
	Name         ldText                   `json:"name"`
	Description  ldText                   `json:"description"`
	TimeRequired ldText                   `json:"timeRequired"`
	Enrollment   ldText                   `json:"totalHistoricalEnrollment"`
	Instances    ldList[ldCourseInstance] `json:"hasCourseInstance"`
	Author       ldList[ldThing]          `json:"author"`
	About        ldList[ldThing]          `json:"about"`
	Videos       ldList[ldList[ldVideo]]  `json:"hasPart"`
	Syllabus     ldList[ldThing]          `json:"syllabusSections"`
	Rating       *ldRating                `json:"aggregateRating"`
}

// extractLinkedInCourse renders a Learning course, paywalled: its videos need
// a subscription. False for a page with no course markup — no Course JSON-LD
// stating videos or a syllabus, and no table of contents.
func extractLinkedInCourse(
	doc *html.Node,
	page *url.URL,
	address string,
	entities []ldEntity,
	readErr error,
	renderer markdownRenderer,
) (siteExtraction, bool) {
	var course ldCourse
	found, err := ldDecode(entities, "Course", &course)
	if err = errors.Join(readErr, err); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a LinkedIn course's JSON-LD could not be read whole",
			"target", logSource(page.String()), obs.FieldErr, err.Error())
	}
	toc := firstClass(doc, "table-of-contents__list")
	if (!found || len(course.Videos)+len(course.Syllabus) == 0) && toc == nil {
		if err != nil {
			return siteExtraction{unrendered: linkedInUnread}, false
		}
		return siteExtraction{}, false
	}
	return siteExtraction{markdown: linkedInCoursePage(doc, address, course, toc, renderer), partial: paywallReason}, true
}

// linkedInCoursePage renders a course's title, instructor, duration, level,
// release date and learners as shown, its details, skills, contents chapter
// by chapter and the learner review summary; the JSON-LD stands in where the
// page shows none.
func linkedInCoursePage(doc *html.Node, address string, course ldCourse, toc *html.Node,
	renderer markdownRenderer,
) string {
	title := linkedInClassText(doc, "top-card-layout__title")
	if title == "" {
		title = string(course.Name)
	}
	var instructors []string
	for _, instance := range course.Instances {
		for _, person := range instance.Instructor {
			instructors = append(instructors, string(person.Name))
		}
	}
	if len(instructors) == 0 {
		for _, person := range course.Author {
			instructors = append(instructors, string(person.Name))
		}
	}
	var labeled, unlabeled []string
	for _, item := range classElements(doc, "top-card__headline-row-item") {
		text := nodeText(item)
		if label, value, ok := strings.Cut(text, ": "); ok {
			labeled = append(labeled, label, value)
			continue
		}
		if with, ok := strings.CutPrefix(text, "With "); ok {
			if len(instructors) == 0 {
				instructors = append(instructors, with)
			}
			continue
		}
		unlabeled = append(unlabeled, text)
	}
	if len(labeled) == 0 && course.TimeRequired != "" {
		labeled = append(labeled, "Duration", linkedInDuration(string(course.TimeRequired)))
	}
	pairs := append([]string{"Instructor", linkedInJoin(", ", instructors...)}, labeled...)
	pairs = append(pairs, "Learners", linkedInThousands(string(course.Enrollment)))
	var out strings.Builder
	out.WriteString("# " + title + "\n\n")
	if meta := linkedInMeta(pairs...); meta != "" {
		out.WriteString(meta + "  \n")
	}
	if extra := linkedInJoin(" · ", unlabeled...); extra != "" {
		out.WriteString(extra + "  \n")
	}
	out.WriteString("**Course:** " + address + "\n\n")

	var details []string
	if section := firstClass(doc, "course-details__description"); section != nil {
		if markup := firstClass(section, "show-more-less-html__markup"); markup != nil {
			details = renderer.blocks(markup)
		}
	}
	if len(details) == 0 && course.Description != "" {
		details = linkedInParagraphs(string(course.Description))
	}
	if len(details) > 0 {
		out.WriteString("## Course details\n\n" + strings.Join(details, "\n\n") + "\n\n")
	}

	var skills []string
	for _, item := range classElements(doc, "course-skills__skill-list-item") {
		if skill := nodeText(item); skill != "" {
			skills = append(skills, "- "+skill)
		}
	}
	if len(skills) == 0 {
		for _, topic := range course.About {
			if topic.Name != "" {
				skills = append(skills, "- "+string(topic.Name))
			}
		}
	}
	if len(skills) > 0 {
		out.WriteString("## Skills\n\n" + strings.Join(skills, "\n") + "\n\n")
	}

	if chapters := linkedInCourseContents(toc, course.Videos); len(chapters) > 0 {
		out.WriteString("## Contents\n\n" + strings.Join(chapters, "\n\n") + "\n\n")
	}

	summary := linkedInJoin(" ", linkedInClassText(doc, "ratings-summary__rating-average"),
		linkedInClassText(doc, "ratings-summary__rating-max"))
	summary = linkedInJoin(" · ", summary, linkedInClassText(doc, "ratings-summary__ratings-total"))
	if summary == "" && course.Rating != nil && course.Rating.Value != "" {
		summary = string(course.Rating.Value)
		if course.Rating.Best != "" {
			summary += " out of " + string(course.Rating.Best)
		}
		if course.Rating.Count != "" {
			summary += " · " + linkedInThousands(string(course.Rating.Count)) + " ratings"
		}
	}
	if summary != "" {
		out.WriteString("## Learner reviews\n\n" + summary + "\n")
	}
	return out.String()
}

// linkedInCourseContents renders a course's chapters, each a "### chapter"
// heading over its videos and durations: from the page's table of contents,
// or from the JSON-LD's videos, whose names end " - <chapter>".
func linkedInCourseContents(toc *html.Node, videos ldList[ldList[ldVideo]]) []string {
	var chapters []string
	if toc != nil {
		for _, section := range classElements(toc, "toc-section") {
			name := ""
			if button := firstClass(section, "show-more-less__button"); button != nil {
				name = nodeText(button)
			}
			var lines []string
			for _, item := range classElements(section, "toc-item") {
				line := linkedInJoin(" · ", linkedInClassText(item, "table-of-contents__item-title"),
					linkedInClassText(item, "table-of-contents__item-duration"))
				if firstClass(item, "table-of-contents__item-status--locked") != nil {
					line += " · locked"
				}
				lines = append(lines, "- "+line)
			}
			chapters = append(chapters, linkedInChapter(name, lines))
		}
		return chapters
	}
	name := ""
	var lines []string
	for _, group := range videos {
		for _, video := range group {
			title, chapter, _ := strings.Cut(string(video.Name), " - ")
			if chapter != name && len(lines) > 0 {
				chapters = append(chapters, linkedInChapter(name, lines))
				lines = nil
			}
			name = chapter
			lines = append(lines, "- "+linkedInJoin(" · ", title, linkedInDuration(string(video.Duration))))
		}
	}
	if len(lines) > 0 {
		chapters = append(chapters, linkedInChapter(name, lines))
	}
	return chapters
}

// linkedInChapter renders one chapter of a course's contents.
func linkedInChapter(name string, lines []string) string {
	if name == "" {
		name = "Untitled chapter"
	}
	return "### " + name + "\n\n" + strings.Join(lines, "\n")
}

// linkedInDuration renders a JSON-LD duration ("PT3M24S") as the page words
// it ("3m 24s"); the value as stated when it is no duration.
func linkedInDuration(value string) string {
	match := linkedInISODuration.FindStringSubmatch(value)
	if match == nil {
		return value
	}
	var parts []string
	for index, unit := range []string{"d", "h", "m", "s"} {
		if amount := strings.TrimLeft(match[index+1], "0"); amount != "" {
			parts = append(parts, amount+unit)
		}
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}

// linkedInThousands renders a whole count with thousands separators
// ("5,210"); the value as stated when it is no whole count.
func linkedInThousands(value string) string {
	count, err := strconv.Atoi(value)
	if err != nil || count < 0 {
		return value
	}
	digits := strconv.Itoa(count)
	var out strings.Builder
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	return out.String()
}

// linkedInPieces is each text of node as the page parts it — every text
// node, whitespace collapsed — so a "Newsletter · Published weekly" line
// keeps its separation.
func linkedInPieces(node *html.Node) []string {
	var pieces []string
	for _, text := range textNodes(node) {
		if text = strings.Join(strings.Fields(text), " "); text != "" {
			pieces = append(pieces, text)
		}
	}
	return pieces
}

// linkedInTestID is a feed card's element whose data-test-id names part: the
// embed card's ("main-feed-activity-embed-card__<part>") or the feed card's.
func linkedInTestID(doc *html.Node, part string) *html.Node {
	for _, card := range []string{"main-feed-activity-embed-card__", "main-feed-activity-card__"} {
		if node := firstWithAttr(doc, "data-test-id", card+part, nil); node != nil {
			return node
		}
	}
	return nil
}

// linkedInCleanURL is a LinkedIn link without its tracking query and
// fragment; the link as stated when it does not parse.
func linkedInCleanURL(raw string) string {
	link, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		obs.Logger(context.Background()).Debug("harvest: a LinkedIn link did not parse; kept as stated",
			"link", logSource(raw), obs.FieldErr, err.Error())
		return strings.TrimSpace(raw)
	}
	link.RawQuery, link.Fragment = "", ""
	return link.String()
}
