package harvest

import (
	"net/url"
	"strings"
	"testing"
)

// TestLinkedInCollectionPagesRender: through the dispatcher, a LinkedIn
// newsletter, a top-content article and topic, a Learning course, an embedded
// post and the guest job-posting fragment each render their content — the
// newsletter's author by name alone, its headline on a line of its own; no
// Published line on a collection; the course named paywalled, every other
// page the login wall — and none of the page's chrome.
func TestLinkedInCollectionPagesRender(t *testing.T) {
	for _, tc := range []struct {
		name, source, fixture string
		want, absent          []string
		partial               string
	}{
		{
			name:    "newsletter",
			source:  "https://www.linkedin.com/newsletters/field-robotics-weekly-7000000000000000010/?trk=x",
			fixture: "newsletter.html",
			want: []string{
				"# Field Robotics Weekly\n\nThe notebook of Avery Example\n\n",
				"Newsletter · Published weekly · 4,210 subscribers  \n",
				"**Author:** [Avery Example](https://www.linkedin.com/in/avery-example-0000)  \n" +
					"**Author headline:** Staff Robotics Engineer at Contoso Robotics  \n",
				"**Newsletter:** https://www.linkedin.com/newsletters/field-robotics-weekly-7000000000000000010/\n",
				"## Editions\n\n" +
					"- What a gripper learns in the rain · Sep 17, 2026 · " +
					"https://www.linkedin.com/pulse/what-gripper-learns-rain-avery-example-abcd\n" +
					"- Charging docks in the mud · Aug 26, 2026 · " +
					"https://www.linkedin.com/pulse/charging-docks-mud-avery-example-efgh\n",
			},
			absent: []string{
				"**Published:**", "Join to Subscribe", "More from this author", "A robot fleet diary", "Report this",
				"Created by", "trk=",
			},
			partial: loginWallReason,
		},
		{
			name:    "top-content article",
			source:  "https://www.linkedin.com/top-content/robotics/robots-in-farming/how-robots-will-change-farm-work/",
			fixture: "top-content-article.html",
			want: []string{
				"# How Robots Will Change Farm Work\n\n",
				"## Summary\n\nRobots are moving from test plots to working farms",
				"**Start small:**", "Trial one crop row before a whole field.",
				"## Posts\n\n- [Avery Example](https://www.linkedin.com/in/avery-example-0000) · 2026-02-10 · " +
					"318 reactions  \n  Robots Are Changing the Harvest The era of hand-picked strawberries",
				"  https://www.linkedin.com/posts/avery-example-0000_robots-harvest-activity-7000000000000000030-wxyz\n",
				"- [Fabrikam Automation](https://www.linkedin.com/company/fabrikam-automation) · 2025-05-18 · " +
					"57 reactions  \n  Our planting robot covered 40 acres last week without a single stop.  \n",
			},
			absent: []string{
				"**Published:**", "Explore top LinkedIn content", "Explore categories", "Robots in Farming", "7mo",
				"trk=",
			},
			partial: loginWallReason,
		},
		{
			name:    "top-content topic",
			source:  "https://www.linkedin.com/top-content/robotics/",
			fixture: "top-content-topic.html",
			want: []string{
				"# Robotics\n\n",
				"- [Jordan Sample](https://uk.linkedin.com/in/jordan-sample-0000) · 2026-09-10 · 1024 reactions  \n" +
					"  Security and safety of field robots I think we can all agree",
			},
			absent:  []string{"## Summary", "**Published:**", "Explore categories", "2w", "trk="},
			partial: loginWallReason,
		},
		{
			name:    "learning course",
			source:  "https://www.linkedin.com/learning/gripper-design-foundations?trk=nav",
			fixture: "learning-course.html",
			want: []string{
				"# Gripper Design Foundations\n\n",
				"**Instructor:** Avery Example · **Duration:** 1h 5m · **Skill level:** Beginner · " +
					"**Updated:** 3/2/2026 · **Learners:** 5,210  \n",
				"Liked by 1,204 users  \n",
				"**Course:** https://www.linkedin.com/learning/gripper-design-foundations\n",
				"## Course details\n\nA soft-fruit gripper has to feel before it squeezes.",
				"## Skills\n\n- Robotics\n- Mechanical Design\n",
				"## Contents\n\n### Introduction\n\n- Why grippers matter · 45s\n\n" +
					"### 1. Sensing Force\n\n- Force sensors · 3m 10s\n- Calibrating in the field · 4m 2s · locked\n",
				"## Learner reviews\n\n4.6 out of 5 · 312 ratings\n",
			},
			absent: []string{
				"**Published:**", "Start my 1-month free trial", "Buy for my team", "Preview", "certificate",
				"(Locked)", "How are ratings calculated", "trk=",
			},
			partial: paywallReason,
		},
		{
			name:    "embed",
			source:  "https://www.linkedin.com/embed/feed/update/urn:li:activity:7000000000000000020",
			fixture: "embed.html",
			want: []string{
				"# We shipped the charging dock update today.\n\n",
				"**Author:** Contoso Robotics · **Posted:** 4d  \n",
				"**Post:** https://www.linkedin.com/feed/update/urn:li:activity:7000000000000000020  \n",
				"**Reactions:** 61 · **Comments:** 8\n\n",
				"We shipped the charging dock update today.\n\n" +
					"Every robot now parks itself before the battery drops below 20%.  \nDetails: https://example.com/dock",
			},
			absent: []string{
				"48,000 followers", "Share this post", "LinkedIn respects your privacy", "Reject", "trk=",
			},
			partial: loginWallReason,
		},
		{
			name:    "guest job posting",
			source:  "https://www.linkedin.com/jobs-guest/jobs/api/jobPosting/4000000002",
			fixture: "job-guest-posting.html",
			want: []string{
				"# Field Test Technician\n\n",
				"**Company:** Contoso Robotics · **Location:** Springfield, IL  \n",
				"**Posted:** 2 weeks ago · **Applicants:** 26 applicants  \n",
				"**Job:** https://www.linkedin.com/jobs/view/field-test-technician-at-contoso-robotics-4000000002\n",
				"## Criteria\n\n- **Seniority level:** Entry level\n- **Employment type:** Full-time\n" +
					"- **Industries:** Machinery Manufacturing\n",
				"## Description\n\n",
				"Contoso Robotics tests every gripper on a working farm before the season.",
				"- Set up the test rows\n- Log each gripper's force readings",
			},
			absent:  []string{"Join or sign in", "See who", "Similar jobs", "Robot Wrangler", "Apply", "trk="},
			partial: loginWallReason,
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
			if extraction.partial != tc.partial {
				t.Errorf("partial = %q, want %q", extraction.partial, tc.partial)
			}
		})
	}
}

// TestLinkedInCollectionWallsFallThrough: the /authwall page served at a
// newsletter's, a top-content page's, a course's, an embed's or a guest job
// posting's address is not rendered and names nothing unrendered; a Learning
// catalog page is no course, even carrying a course's markup.
func TestLinkedInCollectionWallsFallThrough(t *testing.T) {
	for _, tc := range []struct{ source, fixture string }{
		{"https://www.linkedin.com/newsletters/field-robotics-weekly-7000000000000000010", "authwall.html"},
		{"https://www.linkedin.com/top-content/robotics/", "authwall.html"},
		{"https://www.linkedin.com/learning/gripper-design-foundations", "authwall.html"},
		{"https://www.linkedin.com/embed/feed/update/urn:li:activity:7000000000000000020", "authwall.html"},
		{"https://www.linkedin.com/jobs-guest/jobs/api/jobPosting/4000000002", "authwall.html"},
		{"https://www.linkedin.com/learning/browse", "learning-course.html"},
		{"https://www.linkedin.com/learning/", "learning-course.html"},
		{"https://www.linkedin.com/learning/search?keywords=robotics", "learning-course.html"},
		{"https://www.linkedin.com/learning/paths/robotics-basics", "learning-course.html"},
	} {
		extraction, extractor, ok := extractForSite(tc.source, linkedInFixture(t, tc.fixture))
		if ok || extraction.markdown != "" || extraction.unrendered != "" {
			t.Errorf("%s with %s: rendered (extractor %q, ok %v, unrendered %q)",
				tc.source, tc.fixture, extractor, ok, extraction.unrendered)
		}
	}
}

// TestLinkedInClaimsCollectionPages: the registry entry claims a newsletter,
// a top-content page, a course, an embedded post and a guest job posting,
// and no Learning catalog page or bare prefix.
func TestLinkedInClaimsCollectionPages(t *testing.T) {
	for source, want := range map[string]bool{
		"https://www.linkedin.com/newsletters/field-robotics-weekly-7000000000000000010": true,
		"https://www.linkedin.com/top-content/robotics/robots-in-farming/":               true,
		"https://www.linkedin.com/learning/gripper-design-foundations":                   true,
		"https://www.linkedin.com/embed/feed/update/urn:li:share:7000000000000000021":    true,
		"https://www.linkedin.com/jobs-guest/jobs/api/jobPosting/4000000002":             true,
		"https://www.linkedin.com/newsletters/":                                          false,
		"https://www.linkedin.com/top-content/":                                          false,
		"https://www.linkedin.com/learning/":                                             false,
		"https://www.linkedin.com/learning/browse":                                       false,
		"https://www.linkedin.com/learning/topics/robotics":                              false,
		"https://www.linkedin.com/embed/feed/update/":                                    false,
		"https://www.linkedin.com/jobs-guest/jobs/api/jobPosting/":                       false,
	} {
		page, err := url.Parse(source)
		if err != nil {
			t.Fatalf("parse %s: %v", source, err)
		}
		if got := isLinkedInPage(page); got != want {
			t.Errorf("isLinkedInPage(%s) = %v, want %v", source, got, want)
		}
	}
}
