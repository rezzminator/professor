package harvest

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The LinkedIn organization family and its lists. A signed-out company page
// (/company/<x>, its life, jobs, about and posts tabs) or showcase page
// (/showcase/<x>) proves it is the organization by an Organization JSON-LD
// entity or by the org top card (the top card's title under an org pageKey);
// it renders the top card's industry and followers, the about-us fields, the
// description, the locations, the updates (the JSON-LD postings, the markup's
// cards where the page carries none), the leaders, the jobs tab's cards and
// the affiliated and similar pages. A product page (/products/<slug>) renders
// its top card and About; a ranking hub (/hubs/…) its JSON-LD ItemList of
// organizations; a job list (/jobs/search, /jobs/<keyword>-jobs, the
// /jobs-guest/…/seeMoreJobPostings fragment) its job cards, each linked to its
// job view with the tracking query stripped. None of these states a
// publication date, so none renders one; a page without the entity its
// address promises falls through to the generic path.

// linkedInOrgPageKey prefixes the pageKey of a signed-out company or showcase
// page.
const linkedInOrgPageKey = "d_org_guest"

// linkedInJobListTitle heads a job-list fragment, which carries no heading.
const linkedInJobListTitle = "LinkedIn job listings"

// linkedInGuestJobList is the path of the guest job-list fragment.
const linkedInGuestJobList = "jobs-guest/jobs/api/seeMoreJobPostings"

// linkedInOrgTabs are the company tabs rendered as the company ("" the
// overview).
var linkedInOrgTabs = map[string]bool{"": true, "life": true, "jobs": true, "about": true, "posts": true}

// linkedInFollowers reads a follower count: a top card's, or a showcase's meta
// description (its top card states none).
var linkedInFollowers = regexp.MustCompile(`(\d[\d,.]*[KMB]?)\s+followers`)

// ldOrganization is a company's or showcase's Organization.
type ldOrganization struct {
	Name        ldText         `json:"name"`
	Description ldText         `json:"description"`
	Slogan      ldText         `json:"slogan"`
	Address     ldAddress      `json:"address"`
	SameAs      ldList[ldText] `json:"sameAs"`
	Employees   *struct {
		Value ldText `json:"value"`
	} `json:"numberOfEmployees"`
}

// ldItemList is a ranking hub's ItemList: each ListItem's position and the
// organization it ranks.
type ldItemList struct {
	Name  ldText `json:"name"`
	Items []struct {
		Position ldText `json:"position"`
		Item     *struct {
			Name    ldText    `json:"name"`
			URL     ldText    `json:"url"`
			Address ldAddress `json:"address"`
		} `json:"item"`
	} `json:"itemListElement"`
}

// linkedInFamilyKind reads the kind of an organization-family or list
// address: a company or one of its rendered tabs, a showcase, a product, a
// hub, a job search, a keyword job list or the guest job-list fragment.
func linkedInFamilyKind(path string) linkedInPageKind {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case segments[0] == "company" && (len(segments) == 2 || len(segments) == 3) && segments[1] != "":
		if len(segments) == 2 || linkedInOrgTabs[segments[2]] {
			return linkedInCompany
		}
	case segments[0] == "showcase" && len(segments) == 2 && segments[1] != "":
		return linkedInCompany
	case segments[0] == "products" && len(segments) == 2 && segments[1] != "":
		return linkedInProduct
	case segments[0] == "hubs" && len(segments) >= 2:
		return linkedInHub
	case segments[0] == "jobs" && len(segments) == 2 && (segments[1] == "search" ||
		strings.HasSuffix(segments[1], "-jobs") || strings.Contains(segments[1], "-jobs-")):
		return linkedInJobList
	case strings.HasPrefix(strings.Join(segments, "/")+"/", linkedInGuestJobList+"/"):
		return linkedInJobList
	}
	return linkedInNone
}

// linkedInOrgPage renders a company or showcase page, or one of a company's
// tabs; false when the page proves no organization (a wall, another kind's
// page), with the error of an Organization that did not decode.
func linkedInOrgPage(
	doc *html.Node,
	page *url.URL,
	address string,
	entities []ldEntity,
) (string, bool, error) {
	var org ldOrganization
	hasOrg, err := ldDecode(entities, "Organization", &org)
	title := linkedInVisibleClassText(doc, "top-card-layout__title")
	if !hasOrg && (title == "" || !strings.HasPrefix(linkedInMetaContent(doc, "pageKey"), linkedInOrgPageKey)) {
		return "", false, err
	}
	name := string(org.Name)
	if name == "" {
		name = title
	}
	fields, industry := linkedInAboutFields(doc)
	if industry == "" {
		industry = linkedInVisibleClassText(doc, "top-card-layout__headline")
	}
	followers := ""
	for _, text := range []string{
		linkedInVisibleClassText(doc, "top-card-layout__first-subline"),
		linkedInVisibleClassText(doc, "top-card-layout__second-subline"),
		linkedInMetaContent(doc, "description"),
	} {
		if match := linkedInFollowers.FindStringSubmatch(text); match != nil {
			followers = match[1]
			break
		}
	}

	var out strings.Builder
	out.WriteString("# " + name + "\n\n")
	if meta := linkedInMeta("Industry", industry, "Followers", followers); meta != "" {
		out.WriteString(meta + "  \n")
	}
	out.WriteString("**Page:** " + address + "\n\n")

	var about []string
	if node := firstWithAttr(doc, "data-test-id", "about-us__description", nil); node != nil {
		about = linkedInParagraphs(rawText(node))
	}
	if len(about) == 0 {
		about = linkedInParagraphs(string(org.Description))
	}
	if len(about) == 0 && org.Slogan != "" {
		about = []string{string(org.Slogan)}
	}
	if len(fields) == 0 {
		fields = linkedInOrgFields(org)
	}
	if len(about) > 0 || len(fields) > 0 {
		out.WriteString("## About\n\n")
		if len(about) > 0 {
			out.WriteString(strings.Join(about, "\n\n") + "\n\n")
		}
		if len(fields) > 0 {
			out.WriteString(strings.Join(fields, "\n") + "\n\n")
		}
	}
	linkedInSection(&out, "Locations", linkedInLocations(doc))
	if leaders := firstWithAttr(doc, "data-test-id", "leaders-at", nil); leaders != nil {
		heading := linkedInVisibleClassText(leaders, "core-section-container__title")
		if heading == "" {
			heading = "Leaders"
		}
		linkedInSection(&out, heading, linkedInCardLinks(leaders, page, "base-main-card"))
	}
	if strings.HasSuffix(strings.TrimSuffix(page.Path, "/"), "/jobs") {
		if cards := linkedInJobCards(doc, page); len(cards) > 0 {
			out.WriteString(fmt.Sprintf("## Jobs\n\n%d jobs listed on this page\n\n", len(cards)) +
				strings.Join(cards, "\n") + "\n\n")
		}
	}
	updates, updatesErr := linkedInUpdates(doc, page, entities)
	linkedInSection(&out, "Updates", updates)
	for _, aside := range [][2]string{{"affiliated-pages", "Affiliated pages"}, {"similar-pages", "Similar pages"}} {
		if section := firstWithAttr(doc, "data-test-id", aside[0], nil); section != nil {
			linkedInSection(&out, aside[1], linkedInCardLinks(section, page, "base-aside-card"))
		}
	}
	return strings.TrimRight(out.String(), "\n") + "\n", true, errors.Join(err, updatesErr)
}

// linkedInAboutFields reads a company's about-us list as "- **Term:** value"
// lines in page order, and its Industry apart (the heading line carries it).
// A linked value (the website) is its link's text, never the hidden caption.
func linkedInAboutFields(doc *html.Node) (fields []string, industry string) {
	for _, term := range elementsByTag(doc, atom.Dt) {
		if term.Parent == nil || !strings.HasPrefix(nodeAttr(term.Parent, "data-test-id"), "about-us__") {
			continue
		}
		definition := term.NextSibling
		for definition != nil && (definition.Type != html.ElementNode || definition.DataAtom != atom.Dd) {
			definition = definition.NextSibling
		}
		if definition == nil {
			continue
		}
		value := linkedInVisibleText(definition)
		if link := firstElement(definition, "a"); link != nil {
			value = nodeText(link)
		}
		label := nodeText(term)
		switch {
		case value == "":
		case label == "Industry":
			industry = value
		default:
			fields = append(fields, "- **"+label+":** "+value)
		}
	}
	return fields, industry
}

// linkedInOrgFields is the about-us list an Organization states, for a page
// whose markup carries none.
func linkedInOrgFields(org ldOrganization) []string {
	var fields []string
	if websites := linkedInTexts(org.SameAs); len(websites) > 0 {
		fields = append(fields, "- **Website:** "+websites[0])
	}
	if org.Employees != nil && org.Employees.Value != "" {
		fields = append(fields, "- **Employees on LinkedIn:** "+string(org.Employees.Value))
	}
	if headquarters := linkedInJoin(", ", string(org.Address.Locality), string(org.Address.Region),
		string(org.Address.Country)); headquarters != "" {
		fields = append(fields, "- **Headquarters:** "+headquarters)
	}
	return fields
}

// linkedInLocations reads a company's Locations section: each address's
// lines joined, the primary one marked.
func linkedInLocations(doc *html.Node) []string {
	section := firstClassElement(doc, atom.Section, "locations")
	if section == nil {
		return nil
	}
	var lines []string
	for _, item := range elementsByTag(section, atom.Li) {
		var parts []string
		for _, line := range elementsByTag(item, atom.P) {
			parts = append(parts, nodeText(line))
		}
		location := linkedInJoin(", ", parts...)
		if location == "" {
			continue
		}
		if tag := firstClass(item, "tag-sm"); tag != nil && nodeText(tag) != "" {
			location += " (" + nodeText(tag) + ")"
		}
		lines = append(lines, "- "+location)
	}
	return lines
}

// linkedInUpdates lists an organization's updates, each "date · excerpt ·
// link": its JSON-LD SocialMediaPostings, or the update cards of the markup
// (their dates as the page words them) when it states none.
func linkedInUpdates(doc *html.Node, page *url.URL, entities []ldEntity) ([]string, error) {
	postings, err := ldPostings(entities, "SocialMediaPosting")
	var lines []string
	for _, post := range postings {
		excerpt := linkedInTrim(strings.Join(strings.Fields(string(post.Text)), " "), linkedInExcerptRunes)
		if line := linkedInJoin(" · ", linkedInDay(string(post.DatePublished)), excerpt,
			string(post.URL)); line != "" {
			lines = append(lines, "- "+line)
		}
	}
	if len(postings) > 0 {
		return lines, err
	}
	for _, card := range classElements(doc, "main-feed-activity-card") {
		when := ""
		if stamp := firstElement(card, "time"); stamp != nil {
			when = nodeText(stamp)
		}
		excerpt := ""
		if commentary := firstWithAttr(card, "data-test-id", "main-feed-activity-card__commentary", nil); commentary != nil {
			excerpt = linkedInTrim(linkedInVisibleText(commentary), linkedInExcerptRunes)
		}
		link := ""
		if card.Parent != nil {
			if overlay := firstClass(card.Parent, "main-feed-card__overlay-link"); overlay != nil {
				link = linkedInLink(page, nodeAttr(overlay, "href"))
			}
		}
		if line := linkedInJoin(" · ", when, excerpt, link); line != "" {
			lines = append(lines, "- "+line)
		}
	}
	return lines, err
}

// linkedInCardLinks lists the cards of class under section — leaders,
// affiliated or similar pages — as "[title](link) · subtitle".
func linkedInCardLinks(section *html.Node, page *url.URL, class string) []string {
	var lines []string
	for _, card := range classElements(section, class) {
		title := linkedInVisibleClassText(card, class+"__title")
		if title == "" {
			continue
		}
		link := nodeAttr(card, "href")
		if link == "" {
			if anchor := firstElement(card, "a"); anchor != nil {
				link = nodeAttr(anchor, "href")
			}
		}
		if link = linkedInLink(page, link); link != "" {
			title = "[" + title + "](" + link + ")"
		}
		lines = append(lines, "- "+linkedInJoin(" · ", title, linkedInVisibleClassText(card, class+"__subtitle")))
	}
	return lines
}

// linkedInProductPage renders a product page: its name, category and company
// from the top card, and its About; false when the page shows no product top
// card.
func linkedInProductPage(doc *html.Node, address string) (string, bool) {
	name := linkedInVisibleClassText(doc, "top-card-layout__title")
	if name == "" {
		return "", false
	}
	category, company := "", ""
	if headline := firstClass(doc, "top-card-layout__headline"); headline != nil {
		for _, link := range elementsByTag(headline, atom.A) {
			switch href := nodeAttr(link, "href"); {
			case strings.Contains(href, "/products/categories/"):
				category = nodeText(link)
			case strings.Contains(href, "/company/"):
				company = nodeText(link)
			}
		}
		if category == "" && company == "" {
			category = nodeText(headline)
		}
	}
	var out strings.Builder
	out.WriteString("# " + name + "\n\n")
	if meta := linkedInMeta("Category", category, "Company", company); meta != "" {
		out.WriteString(meta + "  \n")
	}
	out.WriteString("**Product:** " + address + "\n")
	if section := firstClassElement(doc, atom.Section, "about"); section != nil {
		var about []string
		for _, paragraph := range elementsByTag(section, atom.P) {
			if text := linkedInVisibleText(paragraph); text != "" {
				about = append(about, text)
			}
		}
		if len(about) > 0 {
			out.WriteString("\n## About\n\n" + strings.Join(about, "\n\n") + "\n")
		}
	}
	return out.String(), true
}

// linkedInHubPage renders a ranking hub's ItemList: its title, then each
// organization by rank, linked, with its location; false when the page states
// no ranked organization.
func linkedInHubPage(doc *html.Node, address string, entities []ldEntity) (string, bool, error) {
	var list ldItemList
	if found, err := ldDecode(entities, "ItemList", &list); !found {
		return "", false, err
	}
	var lines []string
	for index, entry := range list.Items {
		if entry.Item == nil || entry.Item.Name == "" {
			continue
		}
		rank := string(entry.Position)
		if rank == "" {
			rank = fmt.Sprint(index + 1)
		}
		label := string(entry.Item.Name)
		if entry.Item.URL != "" {
			label = "[" + label + "](" + string(entry.Item.URL) + ")"
		}
		lines = append(lines, rank+". "+linkedInJoin(" · ", label, linkedInJoin(", ",
			string(entry.Item.Address.Locality), string(entry.Item.Address.Region),
			string(entry.Item.Address.Country))))
	}
	if len(lines) == 0 {
		return "", false, nil
	}
	title := string(list.Name)
	if title == "" {
		title = pageTitle(doc)
	}
	title = strings.TrimSpace(strings.TrimSuffix(title, "| LinkedIn"))
	return "# " + title + "\n\n**Hub:** " + address + "\n\n" + strings.Join(lines, "\n") + "\n", true, nil
}

// linkedInJobListPage renders a job list — a search, a keyword list or the
// guest fragment — as its job cards under the page's heading; false when the
// page holds no job card.
func linkedInJobListPage(doc *html.Node, page *url.URL, address string) (string, bool) {
	cards := linkedInJobCards(doc, page)
	if len(cards) == 0 {
		return "", false
	}
	title := linkedInJobListTitle
	if !strings.HasPrefix(strings.TrimPrefix(page.Path, "/"), linkedInGuestJobList) {
		if heading := firstElement(doc, "h1"); heading != nil && linkedInVisibleText(heading) != "" {
			title = linkedInVisibleText(heading)
		} else if named := strings.TrimSuffix(pageTitle(doc), " | LinkedIn"); named != "" {
			title = named
		}
	}
	return fmt.Sprintf("# %s\n\n**Page:** %s\n\n%d jobs listed on this page\n\n%s\n",
		title, address, len(cards), strings.Join(cards, "\n")), true
}

// linkedInJobCards lists a page's job cards (a base card naming a
// jobPosting), each "[title](view) · company · location · listed day", its
// salary and benefit badge after when the card shows them; the view link
// without its tracking query.
func linkedInJobCards(doc *html.Node, page *url.URL) []string {
	var lines []string
	for _, card := range classElements(doc, "base-card") {
		if !strings.HasPrefix(nodeAttr(card, "data-entity-urn"), "urn:li:jobPosting:") {
			continue
		}
		title := linkedInFirstClassText(card, "base-search-card__title", "base-main-card__title")
		if title == "" {
			continue
		}
		link := nodeAttr(card, "href")
		if anchor := firstClass(card, "base-card__full-link"); anchor != nil {
			link = nodeAttr(anchor, "href")
		}
		if link = linkedInLink(page, link); link != "" {
			title = "[" + title + "](" + link + ")"
		}
		listed := ""
		if stamp := firstElement(card, "time"); stamp != nil {
			listed = nodeAttr(stamp, "datetime")
		}
		if listed != "" {
			listed = "listed " + linkedInDay(listed)
		}
		lines = append(lines, "- "+linkedInJoin(" · ", title,
			linkedInFirstClassText(card, "base-search-card__subtitle", "base-main-card__subtitle"),
			linkedInFirstClassText(card, "job-search-card__location", "main-job-card__location"),
			listed,
			linkedInFirstClassText(card, "job-search-card__salary-info"),
			linkedInFirstClassText(card, "job-posting-benefits__text")))
	}
	return lines
}

// linkedInSection writes "## heading" and its lines; nothing when none.
func linkedInSection(out *strings.Builder, heading string, lines []string) {
	if len(lines) > 0 {
		out.WriteString("## " + heading + "\n\n" + strings.Join(lines, "\n") + "\n\n")
	}
}

// linkedInLink resolves href against the page, without its query and
// fragment (LinkedIn's tracking); "" when href is empty or unreadable.
func linkedInLink(page *url.URL, href string) string {
	if strings.TrimSpace(href) == "" {
		return ""
	}
	target, err := page.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	target.RawQuery, target.Fragment = "", ""
	return target.String()
}

// linkedInMetaContent is the content of the page's meta named name (its
// pageKey, its description), "" when it has none.
func linkedInMetaContent(doc *html.Node, name string) string {
	for _, meta := range elementsByTag(doc, atom.Meta) {
		if nodeAttr(meta, "name") == name {
			return strings.TrimSpace(nodeAttr(meta, "content"))
		}
	}
	return ""
}

// linkedInFirstClassText is the visible text of the first class of classes
// that node holds; "" when none.
func linkedInFirstClassText(node *html.Node, classes ...string) string {
	for _, class := range classes {
		if text := linkedInVisibleClassText(node, class); text != "" {
			return text
		}
	}
	return ""
}

// linkedInVisibleClassText is the visible text of node's first element of
// class; "" when none.
func linkedInVisibleClassText(node *html.Node, class string) string {
	if found := firstClass(node, class); found != nil {
		return linkedInVisibleText(found)
	}
	return ""
}

// linkedInVisibleText is node's text without its screen-reader-only and
// hidden elements (an "is an Influencer" note, a link's hidden caption).
func linkedInVisibleText(node *html.Node) string {
	var parts []string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		switch {
		case current.Type == html.TextNode:
			parts = append(parts, current.Data)
			return
		case current.Type == html.ElementNode && (hasClass(current, "sr-only") ||
			hasClass(current, "hidden") || hasClass(current, "screen-reader-text")):
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}
