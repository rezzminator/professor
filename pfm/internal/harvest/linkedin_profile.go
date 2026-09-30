package harvest

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The LinkedIn member profile (/in/<vanity>[/<section>]). Its entity is a
// @graph holding the member's Person beside their Article (Pulse) and
// DiscussionForumPosting (recent posts) entries; the markup holds more — the
// headline and the About, Experience and Education sections — so the
// extractor reads those from it, the JSON-LD standing in where the page
// shows none. A profile states no publication date, so it renders none. A
// signed-out reader asking for a section (/details/skills/) is served the
// profile itself; the partial names the section asked for as not read.

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
	var out strings.Builder
	out.WriteString("# " + string(person.Name) + "\n\n")
	if meta := linkedInMeta("Headline", headline, "Location", linkedInJoin(", ",
		string(person.Address.Locality), string(person.Address.Region)), "Followers", followers); meta != "" {
		out.WriteString(meta + "  \n")
	}
	out.WriteString("**Profile:** " + linkedInProfileAddress(address, string(person.URL)) + "\n\n")

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
		for index := range articles {
			article := &articles[index]
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
		for index := range activity {
			post := &activity[index]
			out.WriteString("- " + linkedInJoin(" · ", linkedInDay(string(post.DatePublished)),
				linkedInExcerpt(post), string(post.URL)) + "\n")
		}
	}
	return out.String(), true, errors.Join(articlesErr, activityErr)
}

// linkedInProfileAddress is the address a profile line names: the Person's
// own url when it is an https linkedin.com profile, since a sub-page
// (/recent-activity/, /details/…) and the redirect LinkedIn answers some of
// them with both carry it and the page read is the profile; else address.
func linkedInProfileAddress(address, stated string) string {
	if stated == "" {
		return address
	}
	canonical, err := url.Parse(stated)
	if err != nil {
		obs.Logger(context.Background()).
			Debug("harvest: a LinkedIn Person's url did not parse; the address read stands",
				"target", logSource(address), "link", logSource(stated), obs.FieldErr, err.Error())
		return address
	}
	if canonical.Scheme != schemeHTTPS || !isLinkedInHost(canonical.Hostname()) ||
		linkedInKind(canonical) != linkedInProfile {
		return address
	}
	return canonical.String()
}

// linkedInSectionReason names, for a profile address asking for a section
// other than the root or the recent activity (/details/skills/), that the
// section is not what was read: a signed-out reader is served the profile.
func linkedInSectionReason(page *url.URL) string {
	rest, _ := strings.CutPrefix(page.Path, "/in/")
	_, section, _ := strings.Cut(rest, "/")
	section = strings.Trim(section, "/")
	if section == "" || section == "recent-activity" || strings.HasPrefix(section, "recent-activity/") {
		return ""
	}
	return "linkedin profile: the address asked for the profile's " + section +
		" page; the signed-out profile was read"
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
