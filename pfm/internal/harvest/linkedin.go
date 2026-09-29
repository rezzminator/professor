package harvest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The LinkedIn page extractor. A signed-out LinkedIn page states its main
// entity in schema.org JSON-LD. This file holds the router and the post
// (/posts/<slug>), feed update (/feed/update/<urn>), job view
// (/jobs/view/<slug-id>) and member profile (/in/<vanity>[/<section>]) kinds;
// linkedin_org.go the company family, products, ranking hubs and job-card
// lists; linkedin_collections.go newsletters, top content, Learning courses,
// embeds and the guest job posting. For the kinds here the entity is a
// SocialMediaPosting with its author, counts, text and the comments shown
// signed-out; a JobPosting; a @graph holding the member's Person beside their
// Article (Pulse) and DiscussionForumPosting (recent posts) entries. The
// markup around it is navigation, a sign-in gate and a login form, so the
// extractor renders the JSON-LD and reads the markup only where it holds more:
// a job's criteria list, salary and applicant count, and a profile's headline,
// About, Experience and Education sections. A post's author is the posting's
// own, never a comment's; a profile states no publication date, so it renders
// none. A page without the entity its address promises — the /authwall "Join
// LinkedIn" page, the login page, a 999 answer — is not the shape it knows and
// falls through to the generic path, which names the wall. Every page rendered
// is what LinkedIn shows a signed-out reader, so each carries the login wall's
// partial; a post's comments are reconciled against the count it states.

const linkedInHost = "linkedin.com"

// linkedInHeadingRunes caps a post's heading, its first line.
const linkedInHeadingRunes = 100

// linkedInUntitled heads a post that states neither text nor headline.
const linkedInUntitled = "[untitled post]"

// linkedInUnread is what a page whose JSON-LD could not be read leaves
// unrendered.
const linkedInUnread = "linkedin page: its JSON-LD could not be read, so its entity was not rendered"

// linkedInExcerptRunes caps a recent post's excerpt on a profile.
const linkedInExcerptRunes = 200

var (
	// linkedInBlankLines parts a post's plain text into paragraphs.
	linkedInBlankLines = regexp.MustCompile(`\n[ \t]*\n`)
	// linkedInBreakRuns is a run of hard breaks (<br><br>) in a job
	// description, which parts paragraphs.
	linkedInBreakRuns = regexp.MustCompile(`(?: {2}\n){2,}`)
)

// linkedInPageKind is the entity a LinkedIn address promises.
type linkedInPageKind int

const (
	linkedInNone linkedInPageKind = iota
	linkedInNewsletter
	linkedInTopContent
	linkedInCourse
	linkedInEmbed
	linkedInGuestJob
	linkedInPost
	linkedInJob
	linkedInProfile
	linkedInCompany
	linkedInProduct
	linkedInHub
	linkedInJobList
)

// linkedInKind reads the kind of page an address is: the collection kinds
// (linkedInCollectionKind), /posts/… and /feed/update/… a post, /jobs/view/…
// a job, /in/<vanity>[/<section>] a profile, then the company family, product,
// hub and job-list kinds (linkedInFamilyKind).
func linkedInKind(page *url.URL) linkedInPageKind {
	if kind := linkedInCollectionKind(page); kind != linkedInNone {
		return kind
	}
	for _, prefix := range []string{"/posts/", "/feed/update/"} {
		if rest, ok := strings.CutPrefix(page.Path, prefix); ok && strings.Trim(rest, "/") != "" {
			return linkedInPost
		}
	}
	if rest, ok := strings.CutPrefix(page.Path, "/jobs/view/"); ok && strings.Trim(rest, "/") != "" {
		return linkedInJob
	}
	if rest, ok := strings.CutPrefix(page.Path, "/in/"); ok {
		// A sub-page (/in/<vanity>/recent-activity/…) is served signed-out
		// as the profile itself; its Person decides.
		if vanity, _, _ := strings.Cut(rest, "/"); vanity != "" {
			return linkedInProfile
		}
	}
	if kind := linkedInFamilyKind(page.Path); kind != linkedInNone {
		return kind
	}
	return linkedInNone
}

// isLinkedInPage reports a page address the extractor renders.
func isLinkedInPage(page *url.URL) bool { return linkedInKind(page) != linkedInNone }

// ldText is a JSON-LD value read as text: a string, a number (a year, a
// count), a boolean, an object's name, or null ("").
type ldText string

func (text *ldText) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("read a JSON-LD value: %w", err)
	}
	switch typed := value.(type) {
	case nil:
		*text = ""
	case string:
		*text = ldText(strings.TrimSpace(typed))
	case float64:
		*text = ldText(strconv.FormatFloat(typed, 'f', -1, 64))
	case bool:
		*text = ldText(strconv.FormatBool(typed))
	case map[string]any:
		name, ok := typed["name"].(string)
		if !ok {
			return fmt.Errorf("a JSON-LD object holds no name to read as text: %.80s", data)
		}
		*text = ldText(strings.TrimSpace(name))
	default:
		return fmt.Errorf("a JSON-LD value is %T, not text: %.80s", value, data)
	}
	return nil
}

// ldList is a JSON-LD property holding one value or an array of them.
type ldList[T any] []T

func (list *ldList[T]) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	switch {
	case bytes.Equal(trimmed, []byte("null")):
		*list = nil
		return nil
	case bytes.HasPrefix(trimmed, []byte("[")):
		var many []T
		if err := json.Unmarshal(trimmed, &many); err != nil {
			return fmt.Errorf("read a JSON-LD array: %w", err)
		}
		*list = many
		return nil
	}
	var one T
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return fmt.Errorf("read a JSON-LD value: %w", err)
	}
	*list = ldList[T]{one}
	return nil
}

// ldCredential is a job's education requirement: its credentialCategory, or
// the requirement as plain text.
type ldCredential string

func (credential *ldCredential) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*credential = ldCredential(strings.TrimSpace(text))
		return nil
	}
	var object struct {
		Category ldText `json:"credentialCategory"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("read a JSON-LD education requirement: %w", err)
	}
	*credential = ldCredential(object.Category)
	return nil
}

type ldCounter struct {
	InteractionType ldText `json:"interactionType"`
	Count           ldText `json:"userInteractionCount"`
}

type ldRole struct {
	StartDate ldText `json:"startDate"`
	EndDate   ldText `json:"endDate"`
}

// ldThing is a named entity: an author, an organization, a school.
type ldThing struct {
	Name   ldText  `json:"name"`
	URL    ldText  `json:"url"`
	Member *ldRole `json:"member"`
}

// ldPosting is a SocialMediaPosting, one of its comments, a member's Article
// or DiscussionForumPosting.
type ldPosting struct {
	URL           ldText            `json:"url"`
	Headline      ldText            `json:"headline"`
	ArticleBody   ldText            `json:"articleBody"`
	Text          ldText            `json:"text"`
	DatePublished ldText            `json:"datePublished"`
	Author        ldList[ldThing]   `json:"author"`
	Comments      ldList[ldPosting] `json:"comment"`
	CommentCount  ldText            `json:"commentCount"`
	Stats         ldList[ldCounter] `json:"interactionStatistic"`
}

type ldAddress struct {
	Locality ldText `json:"addressLocality"`
	Region   ldText `json:"addressRegion"`
	Country  ldText `json:"addressCountry"`
}

type ldPlace struct {
	Address ldAddress `json:"address"`
}

// ldSalary is a JobPosting's baseSalary: a MonetaryAmount whose value is a
// number or a QuantitativeValue.
type ldSalary struct {
	Currency ldText          `json:"currency"`
	Value    json.RawMessage `json:"value"`
}

type ldJob struct {
	Title              ldText               `json:"title"`
	Description        ldText               `json:"description"`
	DatePosted         ldText               `json:"datePosted"`
	ValidThrough       ldText               `json:"validThrough"`
	EmploymentType     ldList[ldText]       `json:"employmentType"`
	HiringOrganization ldThing              `json:"hiringOrganization"`
	JobLocation        ldList[ldPlace]      `json:"jobLocation"`
	Skills             ldList[ldText]       `json:"skills"`
	Industry           ldList[ldText]       `json:"industry"`
	Education          ldList[ldCredential] `json:"educationRequirements"`
	BaseSalary         *ldSalary            `json:"baseSalary"`
}

type ldPerson struct {
	Name        ldText            `json:"name"`
	URL         ldText            `json:"url"`
	JobTitle    ldList[ldText]    `json:"jobTitle"`
	Description ldText            `json:"description"`
	Address     ldAddress         `json:"address"`
	WorksFor    ldList[ldThing]   `json:"worksFor"`
	AlumniOf    ldList[ldThing]   `json:"alumniOf"`
	Stats       ldList[ldCounter] `json:"interactionStatistic"`
}

// ldEntity is one JSON-LD entity of a page, undecoded, with its @type.
type ldEntity struct {
	kind string
	raw  json.RawMessage
}

// linkedInEntities reads every JSON-LD block of doc into its entities, a
// @graph's members flattened; the error names each block that could not be
// read, the entities of the rest still returned.
func linkedInEntities(doc *html.Node) ([]ldEntity, error) {
	var entities []ldEntity
	var failures []error
	for _, script := range elementsByTag(doc, atom.Script) {
		if !strings.EqualFold(strings.TrimSpace(nodeAttr(script, "type")), "application/ld+json") {
			continue
		}
		var err error
		if entities, err = appendLDEntities(entities, []byte(rawText(script))); err != nil {
			failures = append(failures, err)
		}
	}
	return entities, errors.Join(failures...)
}

func appendLDEntities(into []ldEntity, data []byte) ([]ldEntity, error) {
	trimmed := bytes.TrimSpace(data)
	var members []json.RawMessage
	if bytes.HasPrefix(trimmed, []byte("[")) {
		if err := json.Unmarshal(trimmed, &members); err != nil {
			return into, fmt.Errorf("read a JSON-LD array: %w", err)
		}
	} else {
		var node struct {
			Type  json.RawMessage   `json:"@type"`
			Graph []json.RawMessage `json:"@graph"`
		}
		if err := json.Unmarshal(trimmed, &node); err != nil {
			return into, fmt.Errorf("read a JSON-LD block: %w", err)
		}
		if node.Graph == nil {
			return append(into, ldEntity{kind: ldTypeName(node.Type), raw: trimmed}), nil
		}
		members = node.Graph
	}
	for _, member := range members {
		var err error
		if into, err = appendLDEntities(into, member); err != nil {
			return into, err
		}
	}
	return into, nil
}

// ldTypeName is an entity's @type: the string, or an array's first; "" when
// it has none.
func ldTypeName(raw json.RawMessage) string {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return name
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil && len(names) > 0 {
		return names[0]
	}
	return ""
}

// ldDecode decodes the first entity of kind into into; false when the page
// holds none, or it does not decode (the error).
func ldDecode(entities []ldEntity, kind string, into any) (bool, error) {
	for _, entity := range entities {
		if entity.kind != kind {
			continue
		}
		if err := json.Unmarshal(entity.raw, into); err != nil {
			return false, fmt.Errorf("decode the page's %s: %w", kind, err)
		}
		return true, nil
	}
	return false, nil
}

// ldPostings decodes every entity of kind; one that does not decode is
// named in the error and left out.
func ldPostings(entities []ldEntity, kind string) ([]ldPosting, error) {
	var postings []ldPosting
	var failures []error
	for _, entity := range entities {
		if entity.kind != kind {
			continue
		}
		var posting ldPosting
		if err := json.Unmarshal(entity.raw, &posting); err != nil {
			failures = append(failures, fmt.Errorf("decode a %s: %w", kind, err))
			continue
		}
		postings = append(postings, posting)
	}
	return postings, errors.Join(failures...)
}

// extractLinkedInPage renders a LinkedIn page from the entity its address
// promises (linkedInKind); false for a page that holds
// none (a wall), which falls through to the generic path. A JSON-LD block
// that could not be read is logged; when it leaves the page without its
// entity, the fall-through names it.
func extractLinkedInPage(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	entities, readErr := linkedInEntities(doc)
	renderer := markdownRenderer{base: page}
	address := *page
	address.RawQuery, address.Fragment = "", ""
	var (
		markdown, partial string
		found             bool
		err               error
	)
	switch linkedInKind(page) {
	case linkedInNewsletter:
		markdown, found = linkedInNewsletterPage(doc, address.String())
	case linkedInTopContent:
		markdown, found, err = linkedInTopContentPage(doc, address.String(), entities, renderer)
	case linkedInCourse:
		return extractLinkedInCourse(doc, page, address.String(), entities, readErr, renderer)
	case linkedInEmbed:
		markdown, found = linkedInEmbedPage(doc, address.String())
	case linkedInGuestJob:
		markdown, found = linkedInGuestJobPage(doc, address.String(), renderer)
	case linkedInPost:
		markdown, partial, found, err = linkedInPostPage(address.String(), entities)
	case linkedInJob:
		markdown, found, err = linkedInJobPage(doc, address.String(), entities, renderer)
	case linkedInProfile:
		markdown, found, err = linkedInProfilePage(doc, address.String(), entities, renderer)
	case linkedInCompany:
		markdown, found, err = linkedInOrgPage(doc, page, address.String(), entities)
	case linkedInProduct:
		markdown, found = linkedInProductPage(doc, address.String())
	case linkedInHub:
		markdown, found, err = linkedInHubPage(doc, address.String(), entities)
	case linkedInJobList:
		markdown, found = linkedInJobListPage(doc, page, address.String())
	}
	if err = errors.Join(readErr, err); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a LinkedIn page's JSON-LD could not be read whole",
			"target", logSource(page.String()), obs.FieldErr, err.Error())
	}
	if !found {
		if err != nil {
			return siteExtraction{unrendered: linkedInUnread}, false
		}
		return siteExtraction{}, false
	}
	return siteExtraction{markdown: markdown, partial: joinReasons(loginWallReason, partial)}, true
}

// linkedInPostPage renders a SocialMediaPosting: its author (the posting's
// own, never a comment's), date, counts, text and the comments the page
// serves, reconciled against the count it states.
func linkedInPostPage(address string, entities []ldEntity) (markdown, partial string, found bool, err error) {
	var post ldPosting
	if found, err = ldDecode(entities, "SocialMediaPosting", &post); !found {
		return "", "", false, err
	}
	loaded := len(post.Comments)
	stated, statedKnown := ldCount(post.Stats, "CommentAction")
	if count, convErr := strconv.Atoi(string(post.CommentCount)); convErr == nil {
		stated, statedKnown = count, true
	}
	var gaps []string
	statedText := "count not read (the post states no comment count)"
	surplus := ""
	if statedKnown {
		statedText = strconv.Itoa(stated)
		switch rest := stated - loaded; {
		case rest > 0:
			gaps = append(gaps, fmt.Sprintf("%d stated comment(s) not in the page (LinkedIn serves a signed-out "+
				"reader only its first comments)", rest))
		case rest < 0:
			surplus = fmt.Sprintf(" · %d more loaded than stated", -rest)
		}
	} else {
		gaps = append(gaps, "the stated comment count was not read (the post states none)")
	}
	countLine := fmt.Sprintf("**Comments:** %s stated · %d loaded", statedText, loaded) + surplus
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
		progress := fmt.Sprintf("%d comments loaded, the stated count not read", loaded)
		if statedKnown {
			progress = fmt.Sprintf("%d of %d comments loaded", loaded, stated)
		}
		partial = "linkedin post: " + progress + " — " + strings.Join(gaps, "; ")
	}

	body := string(post.ArticleBody)
	if body == "" {
		body = string(post.Text)
	}
	var out strings.Builder
	out.WriteString("# " + linkedInHeading(body, string(post.Headline)) + "\n\n")
	out.WriteString("**Author:** " + linkedInAuthor(post.Author) +
		" · **Published:** " + linkedInDate(string(post.DatePublished), unknownPosted) + "  \n")
	out.WriteString("**Post:** " + address + "  \n")
	if reactions, ok := ldCount(post.Stats, "LikeAction"); ok {
		out.WriteString(fmt.Sprintf("**Reactions:** %d · ", reactions))
	}
	out.WriteString(countLine + "\n\n")
	if paragraphs := linkedInParagraphs(body); len(paragraphs) > 0 {
		out.WriteString(strings.Join(paragraphs, "\n\n") + "\n\n")
	}
	out.WriteString("---\n\n## Comments\n\n")
	if loaded == 0 {
		out.WriteString("*No comments are in this page.*\n")
	}
	for _, comment := range post.Comments {
		out.WriteString("- **" + linkedInAuthor(comment.Author) + "** · " +
			linkedInDate(string(comment.DatePublished), unknownPosted) + "\n")
		if paragraphs := linkedInParagraphs(string(comment.Text)); len(paragraphs) > 0 {
			out.WriteString(prefixLines(strings.Join(paragraphs, "\n\n"), "  ", "") + "\n")
		}
	}
	return out.String(), partial, true, nil
}

// linkedInJobPage renders a JobPosting with the criteria, salary and
// applicant count the page's markup shows beside it.
func linkedInJobPage(
	doc *html.Node,
	address string,
	entities []ldEntity,
	renderer markdownRenderer,
) (string, bool, error) {
	var job ldJob
	if found, err := ldDecode(entities, "JobPosting", &job); !found {
		return "", false, err
	}
	title := string(job.Title)
	if title == "" {
		title = pageTitle(doc)
	}
	var locations []string
	for _, place := range job.JobLocation {
		if location := linkedInJoin(", ", string(place.Address.Locality), string(place.Address.Region),
			string(place.Address.Country)); location != "" {
			locations = append(locations, location)
		}
	}
	var employment []string
	for _, kind := range job.EmploymentType {
		if kind != "" {
			employment = append(employment, linkedInEmployment(string(kind)))
		}
	}
	salary := ""
	if node := firstClass(doc, "compensation__salary"); node != nil {
		salary = nodeText(node)
	}
	if salary == "" {
		salary = linkedInSalary(job.BaseSalary)
	}
	applicants := ""
	if node := firstClass(doc, "num-applicants__caption"); node != nil {
		applicants = nodeText(node)
	}

	var out strings.Builder
	out.WriteString("# " + title + "\n\n")
	for _, line := range [][]string{
		{"Company", string(job.HiringOrganization.Name), "Location", strings.Join(locations, " / ")},
		{
			"Posted", linkedInDay(string(job.DatePosted)), "Valid through", linkedInDay(string(job.ValidThrough)),
			"Employment type", strings.Join(employment, ", "),
		},
		{"Salary", salary, "Applicants", applicants},
	} {
		if meta := linkedInMeta(line...); meta != "" {
			out.WriteString(meta + "  \n")
		}
	}
	out.WriteString("**Job:** " + address + "\n\n")
	criteria := linkedInCriteria(doc)
	if len(criteria) == 0 {
		if industry := linkedInJoin(", ", linkedInTexts(job.Industry)...); industry != "" {
			criteria = append(criteria, "- **Industries:** "+industry)
		}
	}
	var education []string
	for _, credential := range job.Education {
		if credential != "" {
			education = append(education, string(credential))
		}
	}
	if len(education) > 0 {
		criteria = append(criteria, "- **Education:** "+strings.Join(education, ", "))
	}
	if skills := linkedInJoin(", ", linkedInTexts(job.Skills)...); skills != "" {
		criteria = append(criteria, "- **Skills:** "+skills)
	}
	if len(criteria) > 0 {
		out.WriteString("## Criteria\n\n" + strings.Join(criteria, "\n") + "\n\n")
	}
	out.WriteString("## Description\n\n")
	blocks, err := linkedInDescription(string(job.Description), renderer)
	if len(blocks) == 0 {
		if markup := firstClass(doc, "show-more-less-html__markup"); markup != nil {
			blocks = renderer.blocks(markup)
		}
	}
	switch {
	case len(blocks) > 0:
		out.WriteString(strings.Join(blocks, "\n\n") + "\n")
	case err != nil:
		out.WriteString("*The description could not be read: " + err.Error() + "*\n")
	default:
		out.WriteString("*The page states no description.*\n")
	}
	return out.String(), true, err
}

// linkedInDescription renders a job description's HTML as Markdown blocks;
// a run of hard breaks parts paragraphs.
func linkedInDescription(description string, renderer markdownRenderer) ([]string, error) {
	if strings.TrimSpace(description) == "" {
		return nil, nil
	}
	container := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(description), container)
	if err != nil {
		return nil, fmt.Errorf("parse the job description's HTML: %w", err)
	}
	for _, node := range nodes {
		container.AppendChild(node)
	}
	blocks := renderer.blocks(container)
	for index, block := range blocks {
		blocks[index] = strings.TrimSpace(linkedInBreakRuns.ReplaceAllString(block, "\n\n"))
	}
	return blocks, nil
}

// linkedInProfilePage renders a member's Person: headline, location,
// followers, About, Experience and Education from the page's sections (the
// JSON-LD's years where the page shows none), then their articles and recent
// posts. A profile states no publication date.
func linkedInProfilePage(
	doc *html.Node,
	address string,
	entities []ldEntity,
	renderer markdownRenderer,
) (string, bool, error) {
	var person ldPerson
	if found, err := ldDecode(entities, "Person", &person); !found {
		return "", false, err
	}
	articles, articlesErr := ldPostings(entities, "Article")
	activity, activityErr := ldPostings(entities, "DiscussionForumPosting")

	headline := ""
	if node := firstClass(doc, "top-card-layout__headline"); node != nil {
		headline = nodeText(node)
	}
	if headline == "" {
		headline = linkedInJoin(" · ", linkedInTexts(person.JobTitle)...)
	}
	followers := ""
	if count, ok := ldCount(person.Stats, "Follow"); ok {
		followers = linkedInThousands(strconv.Itoa(count))
	}
	// A sub-page (/recent-activity/, /details/…) and the redirect LinkedIn
	// answers some of them with both carry the member's own address in the
	// Person entity; the page read is the profile, so the line names it.
	if canonical, err := url.Parse(string(person.URL)); err == nil && canonical.Scheme == "https" && isLinkedInPage(canonical) {
		address = canonical.String()
	}
	var out strings.Builder
	out.WriteString("# " + string(person.Name) + "\n\n")
	if meta := linkedInMeta("Headline", headline, "Location", linkedInJoin(", ",
		string(person.Address.Locality), string(person.Address.Region)), "Followers", followers); meta != "" {
		out.WriteString(meta + "  \n")
	}
	out.WriteString("**Profile:** " + address + "\n\n")

	var about []string
	if section := firstWithAttr(doc, "data-section", "summary", nil); section != nil {
		content := firstClass(section, "core-section-container__content")
		if content == nil {
			content = section
		}
		about = renderer.blocks(content)
	}
	if len(about) == 0 && person.Description != "" {
		about = linkedInParagraphs(string(person.Description))
	}
	if len(about) > 0 {
		out.WriteString("## About\n\n" + strings.Join(about, "\n\n") + "\n\n")
	}

	var roles []string
	for _, item := range classElements(doc, "experience-item") {
		role := linkedInJoin(" · ", linkedInClassText(item, "experience-item__title"),
			linkedInClassText(item, "experience-item__subtitle"), linkedInDateRange(firstClass(item, "date-range")))
		if role != "" {
			roles = append(roles, "- "+role)
		}
	}
	if len(roles) == 0 {
		for _, organization := range person.WorksFor {
			if role := linkedInJoin(" · ", string(organization.Name), linkedInYears(organization.Member)); role != "" {
				roles = append(roles, "- "+role)
			}
		}
	}
	if len(roles) > 0 {
		out.WriteString("## Experience\n\n" + strings.Join(roles, "\n") + "\n\n")
	}

	var schools []string
	for _, item := range classElements(doc, "education__list-item") {
		degree := ""
		if node := firstElement(item, "h4"); node != nil && strings.Trim(nodeText(node), "- ") != "" {
			degree = nodeText(node)
		}
		school := ""
		if node := firstElement(item, "h3"); node != nil {
			school = nodeText(node)
		}
		if line := linkedInJoin(" · ", school, degree, linkedInDateRange(firstClass(item, "date-range"))); line != "" {
			schools = append(schools, "- "+line)
		}
	}
	if len(schools) == 0 {
		for _, school := range person.AlumniOf {
			if line := linkedInJoin(" · ", string(school.Name), linkedInYears(school.Member)); line != "" {
				schools = append(schools, "- "+line)
			}
		}
	}
	if len(schools) > 0 {
		out.WriteString("## Education\n\n" + strings.Join(schools, "\n") + "\n\n")
	}

	if len(articles) > 0 {
		out.WriteString("## Articles\n\n")
		for _, article := range articles {
			label := string(article.Headline)
			if article.URL != "" {
				label = "[" + label + "](" + string(article.URL) + ")"
			}
			out.WriteString("- " + linkedInJoin(" · ", label, linkedInDay(string(article.DatePublished))) + "\n")
		}
		out.WriteString("\n")
	}
	if len(activity) > 0 {
		out.WriteString("## Recent activity\n\n")
		for _, post := range activity {
			excerpt := linkedInTrim(strings.Join(strings.Fields(string(post.Text)), " "), linkedInExcerptRunes)
			out.WriteString("- " + linkedInJoin(" · ", linkedInDay(string(post.DatePublished)), excerpt,
				string(post.URL)) + "\n")
		}
	}
	return out.String(), true, errors.Join(articlesErr, activityErr)
}

// ldCount reads the count of the first interaction statistic whose type
// names action ("LikeAction", "CommentAction", "Follow").
func ldCount(stats []ldCounter, action string) (int, bool) {
	for _, stat := range stats {
		if !strings.Contains(string(stat.InteractionType), action) {
			continue
		}
		if count, err := strconv.Atoi(string(stat.Count)); err == nil {
			return count, true
		}
	}
	return 0, false
}

// linkedInAuthor is the first author's name, unknownAuthor when none.
func linkedInAuthor(authors []ldThing) string {
	for _, author := range authors {
		if author.Name != "" {
			return string(author.Name)
		}
	}
	return unknownAuthor
}

// linkedInHeading is a post's heading: the first line of its text (its
// headline when the text has none), trimmed to linkedInHeadingRunes.
func linkedInHeading(body, headline string) string {
	for _, line := range strings.Split(body+"\n"+headline, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			return linkedInTrim(line, linkedInHeadingRunes)
		}
	}
	return linkedInUntitled
}

// linkedInTrim cuts text to limit runes at a word boundary, marked with "…".
func linkedInTrim(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	cut := string(runes[:limit])
	if space := strings.LastIndex(cut, " "); space > len(cut)/2 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

// linkedInParagraphs renders a post's plain text as Markdown paragraphs: a
// blank line parts paragraphs, a single newline stays a hard break.
func linkedInParagraphs(text string) []string {
	var out []string
	for _, paragraph := range linkedInBlankLines.Split(strings.ReplaceAll(text, "\r\n", "\n"), -1) {
		var lines []string
		for _, line := range strings.Split(paragraph, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		if len(lines) > 0 {
			out = append(out, strings.Join(lines, "  \n"))
		}
	}
	return out
}

// linkedInDate renders a JSON-LD instant as "2006-01-02 15:04 UTC"; the value
// as stated when it is no instant, missing when it is empty.
func linkedInDate(value, missing string) string {
	if value == "" {
		return missing
	}
	if instant, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return instant.UTC().Format("2006-01-02 15:04 UTC")
	}
	return value
}

// linkedInDay renders a JSON-LD date or instant as its day, "2006-01-02".
func linkedInDay(value string) string {
	if instant, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return instant.UTC().Format(time.DateOnly)
	}
	return value
}

// linkedInEmployment renders a schema.org employment type ("FULL_TIME") as
// the page words it ("Full-time").
func linkedInEmployment(value string) string {
	words := strings.ToLower(strings.ReplaceAll(value, "_", "-"))
	if words == "" {
		return ""
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// linkedInSalary renders a JobPosting's baseSalary; "" when it states none.
func linkedInSalary(salary *ldSalary) string {
	if salary == nil || len(salary.Value) == 0 {
		return ""
	}
	var amount ldText
	if err := json.Unmarshal(salary.Value, &amount); err == nil && amount != "" {
		return linkedInJoin(" ", string(salary.Currency), string(amount))
	}
	var quantity struct {
		Value    ldText `json:"value"`
		MinValue ldText `json:"minValue"`
		MaxValue ldText `json:"maxValue"`
		UnitText ldText `json:"unitText"`
	}
	if err := json.Unmarshal(salary.Value, &quantity); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a LinkedIn job's salary could not be read",
			obs.FieldErr, err.Error())
		return ""
	}
	amount = quantity.Value
	if quantity.MinValue != "" || quantity.MaxValue != "" {
		amount = ldText(linkedInJoin(" – ", string(quantity.MinValue), string(quantity.MaxValue)))
	}
	if amount == "" {
		return ""
	}
	text := linkedInJoin(" ", string(salary.Currency), string(amount))
	if quantity.UnitText != "" {
		text += " per " + strings.ToLower(string(quantity.UnitText))
	}
	return text
}

// linkedInDateRange renders a profile section's date range: its two <time>
// years joined, or one with "Present" when the range states it.
func linkedInDateRange(node *html.Node) string {
	if node == nil {
		return ""
	}
	var times []string
	for _, element := range elementsByTag(node, atom.Time) {
		if text := nodeText(element); text != "" {
			times = append(times, text)
		}
	}
	switch {
	case len(times) >= 2:
		return times[0] + " – " + times[1]
	case len(times) == 1 && strings.Contains(nodeText(node), "Present"):
		return times[0] + " – Present"
	case len(times) == 1:
		return times[0]
	}
	return ""
}

// linkedInYears renders a JSON-LD OrganizationRole's years.
func linkedInYears(role *ldRole) string {
	switch {
	case role == nil:
		return ""
	case role.StartDate != "" && role.EndDate != "":
		return string(role.StartDate) + " – " + string(role.EndDate)
	case role.StartDate != "":
		return "since " + string(role.StartDate)
	case role.EndDate != "":
		return "until " + string(role.EndDate)
	}
	return ""
}

// linkedInMeta renders label/value pairs as one "**Label:** value · …" line,
// leaving out the empty values.
func linkedInMeta(pairs ...string) string {
	var parts []string
	for index := 0; index+1 < len(pairs); index += 2 {
		if pairs[index+1] != "" {
			parts = append(parts, "**"+pairs[index]+":** "+pairs[index+1])
		}
	}
	return strings.Join(parts, " · ")
}

// linkedInJoin joins the non-empty parts with separator.
func linkedInJoin(separator string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, separator)
}

// linkedInTexts is a JSON-LD list's values as strings, a comma-separated
// value split into its items.
func linkedInTexts(values []ldText) []string {
	var out []string
	for _, value := range values {
		for _, item := range strings.Split(string(value), ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

// linkedInClassText is the text of node's first element of class; "" when none.
func linkedInClassText(node *html.Node, class string) string {
	if found := firstClass(node, class); found != nil {
		return nodeText(found)
	}
	return ""
}

// classElements returns every element under node (node included) whose class
// list names class, in DOM order.
func classElements(node *html.Node, class string) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && hasClass(current, class) {
			found = append(found, current)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return found
}

// linkedInWallTrk is a link's trk parameter: LinkedIn tags every link with
// the page template it sits on ("seo-authwall-base_footer-about").
var linkedInWallTrk = regexp.MustCompile(`[?&]trk=([A-Za-z0-9_-]+)`)

// linkedInSignUpWall reports whether markdown, a reader's copy of source, is
// LinkedIn's sign-up wall (the "authwall" template a signed-out visitor is
// bounced to) rather than the page: most of its tagged links name that
// template. The wall's own prose is never read — a page quoting "Join
// LinkedIn" is still the page.
func linkedInSignUpWall(source, markdown string) bool {
	page, err := url.Parse(source)
	if err != nil {
		obs.Logger(context.Background()).
			Warn("harvest: the address could not be parsed for its host", obs.FieldErr, err.Error())
		return false
	}
	if host := strings.ToLower(strings.TrimSuffix(page.Hostname(), ".")); host != linkedInHost &&
		!strings.HasSuffix(host, "."+linkedInHost) {
		return false
	}
	tagged, walled := 0, 0
	for _, match := range linkedInWallTrk.FindAllStringSubmatch(markdown, -1) {
		tagged++
		if strings.Contains(match[1], "authwall") {
			walled++
		}
	}
	return walled > 0 && walled*2 > tagged
}
