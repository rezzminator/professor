package harvest

import (
	"net/url"
	"strings"
	"testing"
)

// TestLinkedInOrgFamilyPagesRender: through the dispatcher, a company page and
// its life and jobs tabs, a showcase page, a product page, a ranking hub, a
// job search, a keyword job list, the guest job-list fragment and a profile's
// sub-page each render what the page shows a signed-out reader — no
// publication date, none of the page's chrome, a job card linked to its view
// with the tracking query stripped — with the login wall named.
func TestLinkedInOrgFamilyPagesRender(t *testing.T) {
	const company = "https://www.linkedin.com/company/contoso-robotics"
	affiliated := "- [Contoso Field Lab](https://www.linkedin.com/showcase/contoso-field-lab/) · Research Services"
	similar := "- [Fabrikam Labs](https://www.linkedin.com/company/fabrikam-labs) · Automation Machinery Manufacturing\n" +
		"- [Northwind Harvest](https://www.linkedin.com/company/northwind-harvest) · Farming"
	for _, tc := range []struct {
		name, source, fixture string
		want, absent          []string
	}{
		{
			name:    "company",
			source:  company + "?trk=public_profile",
			fixture: "company.html",
			want: []string{
				"# Contoso Robotics\n",
				"**Industry:** Machinery Manufacturing · **Followers:** 12,345  \n",
				"**Page:** " + company + "\n",
				"## About\n\nContoso Robotics builds field robots for farms.",
				"We test every gripper in mud and rain.",
				"- **Website:** https://contoso-robotics.example/\n- **Company size:** 51-200 employees\n" +
					"- **Headquarters:** Springfield, Illinois\n- **Type:** Privately Held\n- **Founded:** 2012\n" +
					"- **Specialties:** Field Robotics, Grippers, and Farm Automation\n",
				"## Locations\n\n- 100 Example Road, Springfield, Illinois 62700, US (Primary)\n" +
					"- 7 Sample Street, Lakeside, Ontario A1A 1A1, CA\n",
				"## Updates\n\n- 2d · The charging dock update shipped today. Every robot now docks on the first try. · " +
					"https://www.linkedin.com/posts/contoso-robotics_dock-update-activity-7000000000000000011-wxyz\n",
				"## Affiliated pages\n\n" + affiliated,
				"## Similar pages\n\n" + similar,
			},
			absent: []string{
				"External link", "Get directions", "See jobs", "Report this company", "LinkedIn Member", "Employees at", "Engineer jobs", "trk=",
			},
		},
		{
			name:    "company life tab",
			source:  company + "/life",
			fixture: "company-life.html",
			want: []string{
				"# Contoso Robotics\n",
				"**Industry:** Machinery Manufacturing · **Followers:** 12,345",
				"## Meet our Contoso Robotics leaders.\n\n- [Avery Example](https://www.linkedin.com/in/avery-example-0000)" +
					" · Staff Robotics Engineer at Contoso Robotics\n",
				"## Affiliated pages\n\n" + affiliated,
				"## Similar pages\n\n" + similar,
			},
			absent: []string{"is an Influencer", "## Jobs", "Next", "trk="},
		},
		{
			name:    "company jobs tab",
			source:  company + "/jobs/",
			fixture: "company-jobs.html",
			want: []string{
				"# Contoso Robotics\n",
				"## Jobs\n\n2 jobs listed on this page\n\n" +
					"- [Robotics Test Engineer](https://www.linkedin.com/jobs/view/robotics-test-engineer-at-contoso-robotics-" +
					"4000000011) · Contoso Robotics · Springfield, IL · listed 2026-09-28\n" +
					"- [Gripper Technician](https://www.linkedin.com/jobs/view/gripper-technician-at-contoso-robotics-" +
					"4000000012) · Contoso Robotics · Lakeside, ON · listed 2026-09-21\n",
			},
			absent: []string{"position=", "4 hours ago", "See all jobs"},
		},
		{
			name:    "showcase",
			source:  "https://www.linkedin.com/showcase/contoso-field-lab/",
			fixture: "showcase.html",
			want: []string{
				"# Contoso Field Lab\n",
				"**Industry:** Research Services · **Followers:** 3,210",
				"## About\n\nWhere the robots meet the mud\n\n- **Website:** https://contoso-robotics.example/lab\n",
				"## Updates\n\n- 2026-09-25 · Week six of the orchard trial: the grippers picked 4,000 apples without " +
					"a bruise. · https://www.linkedin.com/posts/contoso-field-lab_trial-activity-7000000000000000021-abcd\n" +
					"- 2026-09-20 · We are hiring a field technician for the spring season. · " +
					"https://www.linkedin.com/posts/contoso-field-lab_hiring-activity-7000000000000000022-efgh\n",
			},
			absent: []string{"A repost the JSON-LD does not carry"},
		},
		{
			name:    "product",
			source:  "https://www.linkedin.com/products/fabrikam-planner/?trk=x",
			fixture: "product.html",
			want: []string{
				"# Fabrikam Planner\n",
				"**Category:** Project Management Software · **Company:** Fabrikam Labs  \n",
				"**Product:** https://www.linkedin.com/products/fabrikam-planner/\n",
				"## About\n\nPlan field work around the weather.\n\n" +
					"Fabrikam Planner schedules crews, robots and harvests on one board.\n",
			},
			absent: []string{"Add as skill", "Learn more", "Similar products", "Northwind Board"},
		},
		{
			name:    "hub",
			source:  "https://www.linkedin.com/hubs/top-companies/",
			fixture: "hub.html",
			want: []string{
				"# Top Companies to work for in Exampleland (2026)\n",
				"1. [Contoso Robotics](https://www.linkedin.com/company/contoso-robotics) · Springfield\n" +
					"2. [Fabrikam Labs](https://www.linkedin.com/company/fabrikam-labs) · Lakeside\n" +
					"3. [Northwind Harvest](https://www.linkedin.com/company/northwind-harvest)\n",
			},
			absent: []string{"list item 1 of 3", "Top Companies](", "LinkedIn]("},
		},
		{
			name:    "job search",
			source:  "https://www.linkedin.com/jobs/search?keywords=robotics&location=Exampleland",
			fixture: "job-search.html",
			want: []string{
				"# 120 Robotics Jobs in Exampleland\n",
				"3 jobs listed on this page\n",
				"- [Robotics Test Engineer](https://www.linkedin.com/jobs/view/robotics-test-engineer-at-contoso-robotics-" +
					"4000000011) · Contoso Robotics · Springfield, IL · listed 2026-09-10 · $90,000.00 - $120,000.00\n",
				"- [Field Robot Operator](https://www.linkedin.com/jobs/view/field-robot-operator-at-northwind-harvest-" +
					"4000000013) · Northwind Harvest · Lakeside, ON · listed 2026-09-18 · Actively Hiring\n",
				"- [Controls Engineer](https://www.linkedin.com/jobs/view/controls-engineer-at-fabrikam-labs-4000000014)" +
					" · Fabrikam Labs · Remote · listed 2026-09-26\n",
			},
			absent: []string{"position=", "Date posted", "See more jobs", "job alert", "2 weeks ago"},
		},
		{
			name:    "keyword job list",
			source:  "https://www.linkedin.com/jobs/robotics-jobs",
			fixture: "job-search.html",
			want:    []string{"# 120 Robotics Jobs in Exampleland\n", "3 jobs listed on this page\n"},
		},
		{
			name:    "guest job-list fragment",
			source:  "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?keywords=robotics&start=25",
			fixture: "job-guest-list.html",
			want: []string{
				"# LinkedIn job listings\n",
				"2 jobs listed on this page\n",
				"- [Orchard Robot Technician](https://www.linkedin.com/jobs/view/orchard-robot-technician-at-contoso-" +
					"robotics-4000000015) · Contoso Robotics · Springfield, IL · listed 2026-09-27 · Actively Hiring\n",
				"- [Gripper Designer](https://www.linkedin.com/jobs/view/gripper-designer-at-fabrikam-labs-4000000016)" +
					" · Fabrikam Labs · Lakeside, ON · listed 2026-09-12\n",
			},
			absent: []string{"position="},
		},
		{
			name:    "profile sub-page",
			source:  "https://www.linkedin.com/in/avery-example-0000/recent-activity/all/",
			fixture: "profile.html",
			want: []string{
				"# Avery Example\n",
				"**Profile:** https://www.linkedin.com/in/avery-example-0000\n",
				"## Experience\n\n- Staff Robotics Engineer · Contoso Robotics · 2021 – Present",
			},
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
			for _, absent := range append(append(tc.absent, "Published", "Welcome back"), linkedInChrome...) {
				if strings.Contains(extraction.markdown, absent) {
					t.Errorf("the rendering carries %q:\n%s", absent, extraction.markdown)
				}
			}
			if extraction.partial != loginWallReason {
				t.Errorf("partial = %q, want the login wall alone %q", extraction.partial, loginWallReason)
			}
		})
	}
}

// TestLinkedInOrgFamilyPagesWithoutTheirEntityFallThrough: an organization,
// product, hub, job-list or profile sub-page address whose page does not
// prove the entity it promises — the authwall, another kind's page, a
// JobPosting's page under a company address, a product's top card under a
// company tab — is not rendered, and falls through to the generic path.
func TestLinkedInOrgFamilyPagesWithoutTheirEntityFallThrough(t *testing.T) {
	for _, tc := range []struct{ source, fixture string }{
		{"https://www.linkedin.com/company/contoso-robotics", "authwall.html"},
		{"https://www.linkedin.com/company/contoso-robotics/jobs", "authwall.html"},
		{"https://www.linkedin.com/showcase/contoso-field-lab/", "authwall.html"},
		{"https://www.linkedin.com/products/fabrikam-planner/", "authwall.html"},
		{"https://www.linkedin.com/hubs/top-companies/", "authwall.html"},
		{"https://www.linkedin.com/jobs/search?keywords=robotics", "authwall.html"},
		{"https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?start=25", "authwall.html"},
		{"https://www.linkedin.com/in/avery-example-0000/recent-activity/all/", "authwall.html"},
		{"https://www.linkedin.com/company/contoso-robotics", "job.html"},
		{"https://www.linkedin.com/company/contoso-robotics/life", "product.html"},
		{"https://www.linkedin.com/hubs/top-companies/", "job-search.html"},
		{"https://www.linkedin.com/in/avery-example-0000/details/experience/", "company.html"},
	} {
		extraction, extractor, ok := extractForSite(tc.source, linkedInFixture(t, tc.fixture))
		if ok || extraction.markdown != "" {
			t.Errorf("%s holding %s was rendered (extractor %q, ok %v)", tc.source, tc.fixture, extractor, ok)
		}
	}
}

// TestLinkedInOrgLinks: a rendered link is an http(s) address without its
// tracking query — a javascript: or mailto: href is dropped; an update's
// excerpt prefers its articleBody; a website's link text leaves out its
// visually hidden caption.
func TestLinkedInOrgLinks(t *testing.T) {
	page, err := url.Parse("https://www.linkedin.com/company/contoso-robotics")
	if err != nil {
		t.Fatalf("parse the page address: %v", err)
	}
	for href, want := range map[string]string{
		"javascript:alert(1)":                 "",
		"mailto:hello@contoso.example":        "",
		"/company/contoso-robotics/?trk=card": "https://www.linkedin.com/company/contoso-robotics/",
	} {
		if got := linkedInLink(page, href); got != want {
			t.Errorf("linkedInLink(%q) = %q, want %q", href, got, want)
		}
	}

	update := `{"@type":"SocialMediaPosting","text":"Short teaser","articleBody":"The full update body",` +
		`"datePublished":"2026-09-01","url":"https://www.linkedin.com/posts/contoso-robotics_x-1?trk=org"}`
	lines, err := linkedInUpdates(linkedInInline(t, ""), page, linkedInTestEntities(t, update))
	if err != nil {
		t.Fatalf("linkedInUpdates: %v", err)
	}
	if want := "- 2026-09-01 · The full update body · https://www.linkedin.com/posts/contoso-robotics_x-1"; len(
		lines) != 1 || lines[0] != want {
		t.Errorf("updates = %q, want [%q]", lines, want)
	}

	hub := `{"@type":"ItemList","name":"Top Companies","itemListElement":[{"position":1,` +
		`"item":{"name":"Contoso","url":"https://www.linkedin.com/company/contoso?trk=hub"}}]}`
	markdown, found, err := linkedInHubPage(linkedInInline(t, ""), page.String(), linkedInTestEntities(t, hub))
	if !found || err != nil {
		t.Fatalf("the hub was not rendered (found %v, err %v)", found, err)
	}
	if want := "1. [Contoso](https://www.linkedin.com/company/contoso)"; !strings.Contains(markdown, want) {
		t.Errorf("the hub lacks %q:\n%s", want, markdown)
	}

	about := linkedInInline(t, `<dl><div data-test-id="about-us__website"><dt>Website</dt><dd>`+
		`<a href="https://contoso.example">https://contoso.example`+
		`<span class="visually-hidden">External link for Contoso</span></a></dd></div></dl>`)
	fields, _ := linkedInAboutFields(about)
	if want := "- **Website:** https://contoso.example"; len(fields) != 1 || fields[0] != want {
		t.Errorf("about fields = %q, want [%q]", fields, want)
	}
}

// linkedInTestEntities reads JSON-LD blocks into their entities.
func linkedInTestEntities(t *testing.T, blocks ...string) []ldEntity {
	t.Helper()
	entities, err := linkedInEntities(linkedInInline(t, "", blocks...))
	if err != nil {
		t.Fatalf("read the JSON-LD: %v", err)
	}
	return entities
}
