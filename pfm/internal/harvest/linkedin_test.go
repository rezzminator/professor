package harvest

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// linkedInFixture parses one synthetic page of testdata/linkedin: the
// structure of a signed-out LinkedIn page (its JSON-LD shape and the classes
// the extractor reads) around invented people, companies and posts.
func linkedInFixture(t *testing.T, name string) *html.Node {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "linkedin", name))
	if err != nil {
		t.Fatalf("read the LinkedIn fixture %s: %v", name, err)
	}
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse the LinkedIn fixture %s: %v", name, err)
	}
	return doc
}

// linkedInChrome is the navigation, cookie banner, sign-in gate and login
// form every fixture carries around its entity.
var linkedInChrome = []string{
	"Skip to main content", "Accept cookies", "Join now", "Sign in", "Email or phone", "Password",
	"Forgot password", "User Agreement", "Show more", "To view or add a comment",
}

// TestLinkedInPagesRenderTheirEntity: through the dispatcher, a LinkedIn
// post, feed update, job view and profile each render their JSON-LD entity —
// a post's author its own, never a commenter's; a job's posted date its
// datePosted; a profile with no publication date — with the login wall named
// and none of the page's chrome.
func TestLinkedInPagesRenderTheirEntity(t *testing.T) {
	const postPath = "/posts/contoso-robotics_harvest-activity-7000000000000000001-abcd"
	for _, tc := range []struct {
		name, source, fixture string
		want, absent          []string
		gap                   string
	}{
		{
			name:    "post",
			source:  "https://www.linkedin.com" + postPath + "?trk=public",
			fixture: "post.html",
			want: []string{
				"# Our new arm picks strawberries without bruising a single one.\n",
				"**Author:** Contoso Robotics · **Published:** 2026-09-24 16:30 UTC",
				"**Post:** https://www.linkedin.com" + postPath + "  \n",
				"**Reactions:** 42 · **Comments:** 5 stated · 2 loaded",
				"The field trial ran for six weeks on a test farm.  \nEvery gripper reported its force at 200 Hz.",
				"- **Jordan Sample** · 2026-09-25 08:12 UTC\n" +
					"  Gentle grippers are the hard part.  \n  What force sensor",
				"- **Riley Placeholder** · 2026-09-26 19:40 UTC\n  Congratulations to the whole field team.",
			},
			absent: []string{"**Author:** Jordan Sample", "**Author:** Riley Placeholder", "trk=public"},
			gap:    "linkedin post: 2 of 5 comments loaded — 3 stated comment(s) not in the page",
		},
		{
			name:    "feed update",
			source:  "https://ca.linkedin.com/feed/update/urn:li:ugcPost:7000000000000000002/",
			fixture: "feed-update.html",
			want: []string{
				"# Three lessons from a year of running a robot fleet in real warehouses, " +
					"written down so nobody else…\n",
				"**Author:** Avery Example · **Published:** 2026-09-17 14:08 UTC",
				"**Reactions:** 97 · **Comments:** 2 stated · 2 loaded\n",
				"1. Charge before you need to.  \n2. Log every stop.",
				"- **Casey Instance** · 2026-09-17 14:43 UTC\n  Avery, the second lesson",
				"- **Morgan Mock** · 2026-09-18 09:00 UTC",
			},
			absent: []string{"**Author:** Casey Instance", "gaps:"},
		},
		{
			name:    "job view",
			source:  "https://www.linkedin.com/jobs/view/robotics-test-engineer-at-contoso-robotics-4000000001",
			fixture: "job.html",
			want: []string{
				"# Robotics Test Engineer\n",
				"**Company:** Contoso Robotics · **Location:** Springfield, IL, US",
				"**Posted:** 2026-09-10 · **Valid through:** 2026-10-18 · **Employment type:** Full-time",
				"**Salary:** $90,000.00/yr - $120,000.00/yr · **Applicants:** 12 applicants",
				"- **Seniority level:** Mid-Senior level",
				"- **Job function:** Engineering and Information Technology",
				"- **Industries:** Machinery Manufacturing",
				"- **Education:** bachelor degree",
				"- **Skills:** Test Planning, Mechanical Testing",
				"## Description\n\nContoso Robotics builds field robots for farms.",
				"You will test grippers before each season.",
				"- Write test plans\n- Run field trials",
			},
			absent: []string{"$85,000.00", "**Published:**", "**Posted:** 2026-03-18", "Similar jobs", "Apply"},
		},
		{
			name:    "profile",
			source:  "https://www.linkedin.com/in/avery-example-0000/",
			fixture: "profile.html",
			want: []string{
				"# Avery Example\n",
				"**Headline:** Staff Robotics Engineer at Contoso Robotics · " +
					"**Location:** Springfield, Illinois, United States · **Followers:** 1,200",
				"**Profile:** https://www.linkedin.com/in/avery-example-0000\n",
				"## About\n\nI build robots that work outside, in mud and rain.\n\nAsk me about grippers.",
				"## Experience\n\n- Staff Robotics Engineer · Contoso Robotics · 2021 – Present\n" +
					"- Controls Engineer · Fabrikam Automation · 2014 – 2021",
				"## Education\n\n- Example State University · BSc, Mechanical Engineering · 2010 – 2014",
				"## Articles\n\n- [What a robot fleet taught me about patience]" +
					"(https://www.linkedin.com/pulse/what-robot-fleet-taught-me-avery-example-0000) · 2026-08-02",
				"## Recent activity\n\n- 2026-09-28 · We shipped the charging dock update today. Thanks to everyone",
			},
			absent: []string{"**Published:**", "Contact Info"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extraction, extractor, ok := extractForSite(tc.source, linkedInFixture(t, tc.fixture))
			if !ok || extractor != "linkedin-page" {
				t.Fatalf("the page was not rendered by linkedin-page (extractor %q, ok %v)", extractor, ok)
			}
			for _, want := range tc.want {
				if !strings.Contains(extraction.markdown, want) {
					t.Errorf("the rendering lacks %q:\n%s", want, extraction.markdown)
				}
			}
			for _, absent := range append(tc.absent, linkedInChrome...) {
				if strings.Contains(extraction.markdown, absent) {
					t.Errorf("the rendering carries %q:\n%s", absent, extraction.markdown)
				}
			}
			if !strings.Contains(extraction.partial, loginWallReason) {
				t.Errorf("partial = %q, want the login wall %q", extraction.partial, loginWallReason)
			}
			if tc.gap != "" && !strings.Contains(extraction.partial, tc.gap) {
				t.Errorf("partial = %q, want the gap %q", extraction.partial, tc.gap)
			}
			if tc.gap == "" && extraction.partial != loginWallReason {
				t.Errorf("partial = %q, want the login wall alone", extraction.partial)
			}
		})
	}
}

// TestLinkedInWallsFallThrough: a page without the entity its address
// promises — the /authwall "Join LinkedIn" page served in a profile's, a
// post's or a job's place — is not rendered, and names nothing unrendered,
// so the generic path names the wall.
func TestLinkedInWallsFallThrough(t *testing.T) {
	for _, source := range []string{
		"https://www.linkedin.com/in/avery-example-0000",
		"https://www.linkedin.com/posts/contoso-robotics_harvest-activity-7000000000000000001-abcd",
		"https://www.linkedin.com/jobs/view/4000000001/",
	} {
		extraction, extractor, ok := extractForSite(source, linkedInFixture(t, "authwall.html"))
		if ok || extraction.markdown != "" || extraction.unrendered != "" {
			t.Errorf("%s: the authwall was rendered (extractor %q, ok %v, unrendered %q)",
				source, extractor, ok, extraction.unrendered)
		}
	}
	// A post's address holding a JobPosting is not the entity it promises.
	if _, _, ok := extractForSite("https://www.linkedin.com/posts/contoso-robotics_x-activity-1",
		linkedInFixture(t, "job.html")); ok {
		t.Error("a post address was rendered from a JobPosting")
	}
}

// TestLinkedInClaimsOnlyItsPages: the registry entry claims a profile's root,
// a job view, a post and a feed update on linkedin.com and its country
// subdomains, and nothing else of the site.
func TestLinkedInClaimsOnlyItsPages(t *testing.T) {
	var linkedIn *siteExtractor
	for index := range siteExtractors {
		if siteExtractors[index].name == "linkedin-page" {
			linkedIn = &siteExtractors[index]
		}
	}
	if linkedIn == nil {
		t.Fatal("no linkedin-page extractor is registered")
	}
	empty := &html.Node{Type: html.DocumentNode}
	for source, want := range map[string]bool{
		"https://www.linkedin.com/in/avery-example-0000":                               true,
		"https://linkedin.com/in/avery-example-0000/":                                  true,
		"https://ca.linkedin.com/in/avery-example-0000":                                true,
		"https://www.linkedin.com/jobs/view/4000000001/":                               true,
		"https://www.linkedin.com/posts/avery-example-0000_x-activity-1-abcd":          true,
		"https://uk.linkedin.com/feed/update/urn:li:activity:7000000000000000004/":     true,
		"https://www.linkedin.com/in/avery-example-0000/details/experience/":           true,
		"https://www.linkedin.com/in/":                                                 false,
		"https://www.linkedin.com/company/contoso-robotics":                            true,
		"https://www.linkedin.com/jobs/search?keywords=robotics":                       true,
		"https://www.linkedin.com/pulse/what-robot-fleet-taught-me-avery-example-0000": false,
		"https://www.linkedin.com/authwall?sessionRedirect=x":                          false,
		"https://www.notlinkedin.com/in/avery-example-0000":                            false,
	} {
		page, err := url.Parse(source)
		if err != nil {
			t.Fatalf("parse %s: %v", source, err)
		}
		if got := linkedIn.claims(page, empty); got != want {
			t.Errorf("claims(%s) = %v, want %v", source, got, want)
		}
	}
}
