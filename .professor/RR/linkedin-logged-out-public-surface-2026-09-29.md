# RR — LinkedIn's logged-out (public) surface and the mechanisms that serve it

Question: map LinkedIn's logged-out (public) surface and the mechanisms LinkedIn uses to serve it — page types and how much each shows, who controls visibility, authwall and search-referrer behaviour (2023-2026, Googlebot/Bingbot), machine-readable data, robots.txt, official data routes, the legal line on logged-out data. Mark every claim VERIFIED (LinkedIn help/legal/engineering, court records, primary news) or FOLKLORE (scraper-vendor blog or forum only). Out of scope: evasion techniques.

## Answer

LinkedIn serves logged-out visitors a server-rendered, non-personalized "public profile" built "for guests ... and search engine crawlers" ([LinkedIn Engineering, 2016](https://www.linkedin.com/blog/engineering/archive/speed-performance-and-public-profile)). Each member decides which sections appear, down to turning public visibility off entirely ([Help a528138](https://www.linkedin.com/help/linkedin/answer/a528138)). robots.txt and the Crawling Terms forbid automated access without "express permission" and limit whitelisted crawling to search indexing ([robots.txt](https://www.linkedin.com/robots.txt), [Crawling Terms](https://www.linkedin.com/legal/crawling-terms)). The Ninth Circuit let hiQ keep scraping public profiles under a 2022 preliminary injunction ([9th Cir. opinion](https://cdn.ca9.uscourts.gov/datastore/opinions/2022/04/18/17-16783.pdf)). LinkedIn has since won through contract and consent judgments, most recently against ProAPIs on 16 September 2026 ([Digital Policy Alert](https://digitalpolicyalert.org/event/43954-us-district-court-issued-final-judgment-in-linkedin-v-proapis-data-scraping-lawsuit)). The widely repeated claim that a Google-referred visitor sees more than a direct visitor has no primary source. The direct tests (§ Direct tests) disprove it: the referrer changed nothing. What does gate a logged-out visitor is a per-session view budget, plus some page types walled outright. The data travels as server-rendered HTML carrying schema.org JSON-LD.

## Fetch outcomes (failure report)

- `/in/williamhgates` returned HTTP 429 Too Many Requests on one fetch. On another it returned 200 with a public profile.
- `/school/stanford-university`, `/pub/dir/John/Smith` and `/directory/people-a` returned HTTP 999 via WebFetch.
- `/directory/people-a` loaded via the harvester, a different fetcher, with the title "Member Directory: A".
- `/events/7031141634369056768` and `/groups/4754743/` returned a sign-in page with no content. The page was in French, likely the fetcher's locale.
- A `/learning/` course URL and one `/pulse/` URL returned 404.
- The DMA and robots.txt reads came back complete. The Ninth Circuit PDF came back partially: header and summary only.
- Justia and CourtListener refused (403 / empty body), and so did Casemine (403).

## Map

### 1. Page types visible without login

These page statuses come from direct fetches by diggers. Each HTTP status and "visible" description is the fetch model's summary, not raw page text, so they are unquoted and should be read as observations made on 2026-09-29 from one fetcher.

- **Member profiles, `/in/`.** VERIFIED, from LinkedIn Help:
  - "Your public profile is a simplified version of your LinkedIn profile. Not all sections of your profile can be displayed publicly."
  - It is "visible to people who aren't members, viewers who aren't signed in to LinkedIn ... subject to your off-LinkedIn visibility settings".
  - "in some contexts we may show a more limited profile preview that includes some but not all of the profile fields you've allowed" ([a518980](https://www.linkedin.com/help/linkedin/answer/a518980)).
  - Observed: `/in/satyanadella` returned 200 with "Join to view full profile" and sign-in prompts, but no redirect to the authwall ([fetch](https://www.linkedin.com/in/satyanadella); unquoted).
- **Company and showcase pages.** Observed: `/company/microsoft` returned 200 with about text, employee count, posts and jobs, and no authwall ([fetch](https://www.linkedin.com/company/microsoft); unquoted). `/showcase/linkedin-news/` returned 200 with page metadata and recent posts ([fetch](https://www.linkedin.com/showcase/linkedin-news/); unquoted).
- **School pages.** `/school/stanford-university` returned HTTP 999 to WebFetch and to curl. In a real browser it redirected to `/authwall` on the first view of a fresh session, with or without a Google referrer (§ Direct tests).
- **Jobs:**
  - `/jobs/view/4455455822` returned 200 with the full description and a salary of "$67,692.00 - $101,532.00 + Benefits". Sign-in prompts appeared on apply, save and AI features ([fetch](https://www.linkedin.com/jobs/view/4455455822); unquoted).
  - `/jobs/search?keywords=engineer` returned 200 with about 60 job titles ([fetch](https://www.linkedin.com/jobs/search?keywords=engineer); unquoted).
  - The guest endpoint `/jobs-guest/jobs/api/seeMoreJobPostings/search` returned an HTML fragment of 10 job cards ([fetch](https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?keywords=engineer&start=0); unquoted). `/jobs-guest/jobs/api/jobPosting/` was not tested.
  - LinkedIn Help says only "Anyone searching for jobs on LinkedIn can see your open job post." ([a516650](https://www.linkedin.com/help/linkedin/answer/a516650)). It does not explicitly address logged-out visitors.
- **Posts, `/posts/` and `/feed/update/`.** VERIFIED rule: a post set to "Anyone" is "visible to anyone on or off LinkedIn", and "even people who aren't signed in to LinkedIn can see your post" ([a523141](https://www.linkedin.com/help/linkedin/answer/a523141)).
  - Observed: two `/posts/` URLs returned 200 with the post text. One showed "Sign in to view more content", and comment text was gated ([fetch 1](https://www.linkedin.com/posts/kylecampion_17-simple-post-ideas-for-those-new-to-writing-activity-7349355939327537153-HwkY), [fetch 2](https://www.linkedin.com/posts/shaban00_http-status-code-999-request-denied-activity-7114897289298509824-uhDl); unquoted). `/feed/update/` was not tested.
- **Pulse articles.** Observed: one `/pulse/` article returned 200 with the full article text visible ([fetch](https://www.linkedin.com/pulse/how-use-linkedin-pulse-eloise-farr); unquoted).
- **Newsletters.** Observed: 200, with editions visible and "Join to Subscribe" gating the subscription ([fetch](https://www.linkedin.com/newsletters/marketing-newsletter-7088072161839476736); unquoted).
- **Events and groups.** Observed: a sign-in page with no event or group content ([event](https://www.linkedin.com/events/7031141634369056768), [group](https://www.linkedin.com/groups/4754743/); unquoted). Whether this was an authwall redirect is unknown.
- **Learning.** Observed: the `/learning/` landing page returned 200 with the catalog and "Start free trial" / "Sign in" prompts ([fetch](https://www.linkedin.com/learning/); unquoted). A course page was not tested (404 on the URL tried).
- **Services marketplace.** Observed: `/services` returned 200 with the marketplace home page ([fetch](https://www.linkedin.com/services); unquoted).
- **People directory, `/directory/` and `/pub/dir/`.** HTTP 999 to WebFetch. The harvester returned "Member Directory: A" with letter and country navigation (unquoted).
  - VERIFIED role: "Search engines like Google and Yahoo periodically review our member directory for new and updated public profile information to show in their search results." ([a543660](https://www.linkedin.com/help/linkedin/answer/a543660/profile-does-or-doesn-t-appear-after-name-search-on-web?lang=en)).

### 2. Who controls visibility

- **Member public-profile controls.** VERIFIED:
  - "To hide your profile from public view, toggle Your profile's public visibility to Off."
  - "To hide specific profile information, toggle the related setting to Off to hide that information from your public profile."
  - "it may take several weeks or even months for search tools like Google, Yahoo, or Bing to detect and reflect those changes." ([a528138](https://www.linkedin.com/help/linkedin/answer/a528138))
- **Search-tool opt-out.** VERIFIED: "You can opt out of appearing on search tools or you can determine what sections of your profile are eligible to appear through your public profile." ([a548106](https://www.linkedin.com/help/linkedin/answer/a548106); unquoted in the digger's note)
- **Photo default.** VERIFIED: "The default photo setting is Public." ([a518980](https://www.linkedin.com/help/linkedin/answer/a518980)). "Public" means visible to users not logged in ([a545557](https://www.linkedin.com/help/linkedin/answer/a545557/settings-for-profile-photo-visibility)).
- **Privacy Policy framing.** VERIFIED: "Your profile is fully visible to all Members and customers of our Services. Subject to your settings, it can also be visible to others on or off of our Services" ([Privacy Policy](https://www.linkedin.com/legal/privacy-policy)).
- **Minimum age.** VERIFIED: "The Services are not for use by anyone under the age of 16." ([User Agreement](https://www.linkedin.com/legal/user-agreement)). No fetched LinkedIn page states a public-visibility rule for members under 18. The "locked Off" claim appeared only in a search snippet (see Open).
- **EU/EEA versus rest-of-world defaults.** Not found on any fetched LinkedIn page. The Privacy Policy points Designated Countries and UK users to the European Regional Privacy Notice, which could not be read.
- **Company-page admin controls.** The only control found is deactivation, VERIFIED: "deactivating the page hides it from members and removes it from search results on LinkedIn". It is super-admin only and requires "fewer than 100 associated members" ([a542000](https://www.linkedin.com/help/linkedin/answer/a542000/)). No setting for logged-out visibility of a Page was found.

### 3. Authwall, search referrer and crawlers

- **What is served.** VERIFIED:
  - "Public profile is server-side rendered (SSR), which means that the browser can render the page without JavaScript."
  - "A public profile on the other hand is optimized for guests (LinkedIn users who have not signed up yet) and search engine crawlers. A public profile page is a preview of the member profile page, without any personalization; each user sees the same public profile."
  - Source: [LinkedIn Engineering, May 17, 2016](https://www.linkedin.com/blog/engineering/archive/speed-performance-and-public-profile). It is the only engineering source found, and it predates the 2023-2026 window.
- **Observed 2026 behaviour.** Profiles, posts, company pages, jobs, Pulse and newsletters returned content (200) with inline sign-in prompts ("Join to view full profile", "Sign in to view more content"). Events and groups returned a sign-in page. The school and directory pages returned HTTP 999 (unquoted fetch observations).
- **Referrer split (Google-referred visitors see more than direct ones).** No primary source; it appears only in vendor and forum material (nubela.co, taplio, linkedviewer, dev.to). DISPROVED by direct test (§ Direct tests): a Google arrival rendered the same pages and hit the same walls as a typed URL. The referrer is only recorded, as `original_referer` in the authwall URL.
- **Changes 2023-2026.** No dated primary report found. The vendor claim "After a few profile pages, LinkedIn interrupts anonymous browsing with a sign-in wall" matches the direct test: the third typed profile in a fresh session was walled.
- **HTTP 999.** No LinkedIn primary description exists. The third-party explainer http.dev says "LinkedIn returns 999 Request Denied when its security infrastructure determines a request is not from a legitimate browser session" ([http.dev](https://http.dev/999)). Treat it as FOLKLORE.
- **Googlebot and Bingbot.** VERIFIED: public profiles appear "When people search for you using search tools like Google, Yahoo!, Bing, DuckDuckGo, etc." ([a518980](https://www.linkedin.com/help/linkedin/answer/a518980)).
  - Crawling is by whitelist. robots.txt says "LinkedIn may, in its discretion, permit certain automated access to certain LinkedIn pages, for the limited purpose of including content in approved publicly available search engines." ([robots.txt](https://www.linkedin.com/robots.txt))
  - Google's own policy defines cloaking as "presenting different content to users and search engines with the intent to manipulate search rankings" ([Google spam policies](https://developers.google.com/search/docs/essentials/spam-policies)). This is Google policy, not LinkedIn-specific evidence.

### 4. Machine-readable data

All entries here come from fetch-model summaries of served HTML, not quoted markup, so they are unquoted.

- **Company page.** JSON-LD `Organization` plus `BreadcrumbList`, with name, description, numberOfEmployees and url. Meta tags og:title, og:description, og:url, og:image and og:type. No twitter tags ([fetch](https://www.linkedin.com/company/microsoft)).
- **Profile.** JSON-LD `Person`, with `Organization` for affiliations. OpenGraph and Twitter tags were not observed; unverified ([fetch](https://www.linkedin.com/in/williamhgates)).
- **Job view.** JSON-LD `JobPosting` (salary, posted date, applicant count). Meta tags unverified ([fetch](https://www.linkedin.com/jobs/view/4455455822)).
- **Job search.** `BreadcrumbList` and `ListItem`, plus og:* and twitter:card/title/description ([fetch](https://www.linkedin.com/jobs/search?keywords=engineer)).
- **Pulse.** `Article` plus `Person`, and og:image/title/description and twitter:card ([fetch](https://www.linkedin.com/pulse/how-use-linkedin-pulse-eloise-farr)).
- **Posts.** Settled by the raw read in § Direct tests: `SocialMediaPosting` with comments and Google's paywall markup.
- **Post Inspector.** Returned no documentation. Which og tags LinkedIn reads comes only from third-party blogs (FOLKLORE).

### 5. robots.txt

- **Preamble.** VERIFIED:
  - "The use of robots or other automated means to access LinkedIn without the express permission of LinkedIn is strictly prohibited."
  - "If you would like to apply for permission to crawl LinkedIn, please email whitelist-crawl@linkedin.com."
  - Crawling is "subject to LinkedIn's Crawling Terms and Conditions" ([robots.txt](https://www.linkedin.com/robots.txt)).
- **Crawling Terms.** VERIFIED:
  - "Automated Crawling & Indexing without the express permission of LinkedIn is strictly prohibited."
  - Data use "will be confined solely to search indexing for display in a publicly available search engine on the Internet unless granted separate approval by LinkedIn".
  - No use "in connection with a competitive service (as determined by LinkedIn)" ([Crawling Terms](https://www.linkedin.com/legal/crawling-terms)).
- **Agent blocks.**
  - VERIFIED: "User-agent: LinkedInBot" / "Allow: /".
  - Googlebot, Bingbot, Applebot, Yandex, DuckDuckBot, Baiduspider and about 30 other named agents each get a long Disallow list. It includes /authwall, /search*, /messaging/, /voyager/api and /jobs/view/externalApply/. Each list ends with narrow Allows such as "Allow: /help/".
  - VERIFIED: the Googlebot block has no "Disallow: /in/" line, so `/in/` stays crawlable for whitelisted engines.
- **The `User-agent: *` block.** VERIFIED by a raw byte read. The file is 4,862 lines, and it ends with "User-agent: *" / "Disallow: /" at line 4857. The three summarised reads truncated the file, which explains why they missed it and ended on "Allow: /help/".
- **AI crawlers.** VERIFIED by the raw read. Each is named with its own "Disallow: /": GPTBot (line 4782), ClaudeBot (4648), Google-Extended (4639), CCBot (4815) and PerplexityBot (4788). The same goes for anthropic-ai, Claude-Web, ChatGPT-User, cohere-ai, Meta-ExternalAgent, Bytespider and others, 33 named agents in all. The search-side agents OAI-SearchBot and Claude-SearchBot get the path-level list, which also disallows `/public-profile/`.

### 6. Official data routes

VERIFIED from Microsoft Learn and LinkedIn Help.

- **Sign In with LinkedIn (OIDC).** Scopes are `openid`, `profile` ("the member's lite profile including their id, name, and profile picture") and `email`. userinfo returns sub, name, given_name, family_name, picture, locale, and optionally email ([docs](https://learn.microsoft.com/en-us/linkedin/consumer/integrations/self-serve/sign-in-with-linkedin-v2)). It covers only the authenticated member.
  - "Open Permissions are the only permissions that are available to all developers without special approval" ([Getting Access](https://learn.microsoft.com/en-us/linkedin/shared/authentication/getting-access)).
  - The closest thing to public-profile data is the partner-only `r_sales_nav_profiles`, "matched, publicly available member profile information" (same page).
  - No open API for arbitrary public profiles was found. That is an inference from the permission list.
- **Community Management API.** "manage LinkedIn company pages for clients and access account details (admins, roles, followers) and analytics". It is a "Vetted Product with development and standard tiers" ([overview](https://learn.microsoft.com/en-us/linkedin/marketing/community-management/community-management-overview)).
- **Job Posting API.** "We are currently not accepting new partnerships for LinkedIn's Job Posting API." and "The use of these APIs is restricted to those developers approved by LinkedIn ... sign an API agreement with data restrictions" ([overview](https://learn.microsoft.com/en-us/linkedin/talent/job-postings/api/overview)).
- **DMA Member Data Portability (3rd Party).**
  - "Only LinkedIn users from the European Economic Area are allowed to consent to share their LinkedIn data with 3rd party developer applications." It requires scope `r_dma_portability_3rd_party` plus business verification.
  - "LinkedIn will start archiving all the member data (Account History, Articles, Posts)" (Snapshot API).
  - "Developers can only query changelog events created in the past 28 days." ([docs](https://learn.microsoft.com/en-us/linkedin/dma/member-data-portability/member-data-portability-3rd-party/))
  - LinkedIn Help: "Starting in February 2024, Members based in the EEA and Switzerland can choose whether to connect their core LinkedIn professional networking experience" ([a6215608](https://www.linkedin.com/help/linkedin/answer/a6215608)).
- **Member's own export.** "If you select the larger download, you'll receive an email within 24 hours." and "The data will be available for download for 72 hours." ([a1339364](https://www.linkedin.com/help/linkedin/answer/a1339364); unquoted, as a fetch summary).

### 7. Legal line

- **User Agreement §8.2** (effective November 3, 2025). VERIFIED. It bars "software, devices, scripts, robots or any other means or processes (such as crawlers, browser plugins and add-ons or any other technology) to scrape or copy the Services, including profiles and other data from the Services". It also bars copying "any information ... obtained from the Services, whether directly or through third parties (such as search tools or data aggregators or brokers), without the consent of the content owner". It has no public-data exception. Non-registrants are "Visitors" ([User Agreement](https://www.linkedin.com/legal/user-agreement)).
- **hiQ v. LinkedIn, Ninth Circuit, "Filed April 18, 2022".** VERIFIED. "On remand from the United States Supreme Court, the panel affirmed the district court's order preliminarily enjoining LinkedIn Corp. from denying hiQ Labs, Inc. ... access to publicly available member profiles". The remand came after "The Supreme Court granted certiorari, vacated the panel's judgment, and remanded for further consideration in light of Van Buren" ([opinion](https://cdn.ca9.uscourts.gov/datastore/opinions/2022/04/18/17-16783.pdf)).
  - The secondary reading: "accessing that publicly available data can not violate the CFAA" ([California Lawyers Association](https://calawyers.org/privacy-law/ninth-circuit-holds-data-scraping-is-legal-in-hiq-v-linkedin/)).
- **hiQ's ending.** Secondary sources only.
  - The November 4, 2022 N.D. Cal. ruling (17-cv-03301-EMC) held that user-agreement bans on scraping and fake profiles "are enforceable in a breach of contract claim".
  - There was a "$500,000 judgment", and hiQ must "cease all data scraping on LinkedIn's websites and destroy all source code, data, and algorithms" ([Privacy World](https://www.privacyworld.blog/2022/12/linkedins-data-scraping-battle-with-hiq-labs-ends-with-proposed-judgment/), [Proskauer](https://www.proskauer.com/blog/hiq-and-linkedin-reach-proposed-settlement-in-landmark-scraping-case)).
  - DISPUTED date: Privacy World gives "December 7, 2022", while a search snippet gives December 6, 2022.
- **Proxycurl.** VERIFIED from the company's own post, dated "04 July 2025": "In January earlier this year (2025), LinkedIn filed a lawsuit against Proxycurl. ... Today, we are shutting Proxycurl down." ([Nubela](https://nubela.co/blog/goodbye-proxycurl/)).
  - Permanent injunction and deletion of LinkedIn data, secondary: "The Court has entered these requirements as a permanent injunction" ([Social Media Today](https://www.socialmediatoday.com/news/linkedin-wins-legal-case-data-scrapers-proxycurl/756101/)).
- **ProAPIs.**
  - Filed October 2025, alleging more than a million fake accounts and an "iScraper API" ([BleepingComputer](https://www.bleepingcomputer.com/news/legal/linkedin-sues-proapis-for-using-1m-fake-accounts-to-scrape-user-data/); primary news, filing date unquoted).
  - "On 16 September 2026, the District Court for the Northern District of California issued a final judgment on consent in a civil lawsuit brought by LinkedIn Corporation against ProAPIs Inc", "permanently barring defendants from scraping LinkedIn data and selling it" ([Digital Policy Alert](https://digitalpolicyalert.org/event/43954-us-district-court-issued-final-judgment-in-linkedin-v-proapis-data-scraping-lawsuit); secondary tracker).
- **GDPR:**
  - The CNIL fined KASPR: "On 5 December 2024, the CNIL imposed a fine of 240,000 euros on KASPR". KASPR had scraped LinkedIn contact details, including from members who had restricted their visibility ([CNIL](https://www.cnil.fr/en/data-scraping-kaspr-fined-eu240000)).
  - The Irish DPC fined LinkedIn "administrative fines totalling €310 million", "notified to LinkedIn on 22 October 2024". That case concerns ad-targeting consent, not scraping ([DPC](https://www.dataprotection.ie/en/news-media/press-releases/irish-data-protection-commission-fines-linkedin-ireland-eu310-million)).
  - The GAI privacy update: "For members in the Designated Countries and the UK, we updated our European Regional Privacy Notice on November 3, 2025" ([a5538339](https://www.linkedin.com/help/linkedin/answer/a5538339)).

## Direct tests (2026-09-29)

These are raw curl reads of the served HTML and of robots.txt, plus a real logged-out browser with a fresh, cookieless session per run. They come from one vantage point on one day. LinkedIn varies the page between identical requests: the company page's post feed appeared on one request and was missing on the next.

- **Referrer: no effect.**
  - curl with and without `Referer: https://www.google.com/` returned the same status and the same structured-data types for the profile, company, job-search and guest-jobs pages. Two identical requests differed more than the two referrers did.
  - In the browser, arriving through Google's `/url?q=` redirect set `document.referrer` to `https://www.google.com/`. It rendered the same profile, and hit the same wall on the school page, as a typed URL.
- **The authwall is a per-session view budget, plus page types walled outright.**
  - Profiles: in a fresh session, two typed `/in/` URLs rendered and the third redirected to `/authwall`. Two click-throughs from "Other similar profiles" rendered without a wall; more clicks were not tried.
  - Company pages: rendered as the first page of a session, and walled as the second.
  - School pages: walled on the first view of a fresh session, with or without a Google referrer.
  - Posts and job views: rendered even after the same session had been walled on other pages.
  - `/groups/` and `/feed/`: redirected to login.
  - Profile and company pages load with a dismissable sign-in dialog. The content is rendered underneath it and is present in the HTML.
- **A Googlebot user-agent string sent from a non-Google IP** got the same pages as a browser user agent. That says nothing about how LinkedIn treats a verified Googlebot.
- **Structured data, read from the raw HTML:**
  - Profile. `Person`: name, jobTitle, worksFor (Organization plus OrganizationRole startDate), alumniOf (EducationalOrganization with start and end years), address (locality and country), description, image, and follower count (an `InteractionCounter` of type FollowAction). The page also carries an `Article` for each of the member's Pulse articles and a `DiscussionForumPosting` for each recent post.
  - Company. `Organization`: name, description, address, logo, numberOfEmployees, sameAs. Some responses also carry `SocialMediaPosting` for recent updates.
  - Job view. `JobPosting`: title, description (about 6,000 characters), datePosted, validThrough, employmentType, hiringOrganization, jobLocation (street address, latitude and longitude), skills, educationRequirements, industry, identifier.
  - Post. `SocialMediaPosting`: articleBody, author, datePublished, image, interaction counts, and `comment` entries (a `Comment` with the author `Person` name, the text and the date).
  - The post also carries Google's paywalled-content markup, `"hasPart":{"@type":"WebPageElement","isAccessibleForFree":false,"cssSelector":".details"}`. This is the declaration that tells Google part of the page is gated. The `.details` element is still present in the HTML served to a logged-out visitor; the gate is the client-side overlay.
  - Pulse article. `Article` with `"isAccessibleForFree":true`, an author `Person`, the publisher and counts.
  - Job search. `ItemList`. `/jobs-guest/jobs/api/seeMoreJobPostings/search` returns an HTML fragment of job cards with no JSON-LD.
- **robots.txt:** as corrected in § 5.

## Coverage

- **Page types:** partial. A Learning course page, `/feed/update/` and the `/jobs-guest/jobs/api/jobPosting/` endpoint are untested. School pages are walled from the first view. Groups redirect to login; whether events do is untested.
- **Visibility controls:** partial. No LinkedIn page found states the EU/EEA default or the rule for minors under 18.
- **Authwall and search-referrer behaviour:** settled by direct test. The referrer has no effect, and the wall is a per-session view budget. Its exact size rests on one sequence.
- **Structured data:** settled by a raw read of each page type.
- **robots.txt:** settled by a raw byte read, including the `*` block and the per-agent AI blocks.
- **Official data routes:** settled.
- **Legal line:** settled. hiQ's ending and the ProAPIs judgment rest on secondary sources.
- Digging ended: end of round 3.

## Verification

26 facts were checked on 8 pages: robots.txt, Crawling Terms, the Engineering 2016 post, Help a528138, the Job Posting API overview, the DMA 3rd-party docs, Nubela and Digital Policy Alert. 24 were confirmed.

- **OVERRIDDEN:** robots.txt "User-agent: * / Disallow: /". The verification read marked it NOT ON PAGE, but a raw byte read puts it at line 4857 of 4,862. The summarised reads had truncated the file.
- **NOT ON PAGE:** robots.txt "Disallow: /in/" under Googlebot. It was checked as absent, so the map states it as absent.
- **UNCHECKED:** none.

## Open rabbit holes

- **Referrer test (settled).** A Google-referred visit got nothing more than a direct one (§ Direct tests).
- **View-budget size (open).** The count of free views before the wall, per page type and per click versus typed URL, rests on one sequence.
- **2023-2026 tightening (dug, unsettled).** No dated primary news report was found.
- **Minors (dug, unsettled).** "If you are under 18, your public visibility setting may be permanently locked Off" appeared in a search snippet only. It is not on a518980 as fetched.
- **EU/EEA default visibility (dug, unsettled).** The European Regional Privacy Notice was unreadable. The OpenTermsArchive mirror is undug.
- **robots.txt completeness (settled).** A raw byte read confirmed the `*` block.
- **Raw JSON-LD (settled).** Each page type was read from raw HTML.
- **The hiQ 31 F.4th 1180 "without authorization" holding (dug, unsettled).** The verbatim text and the June 2021 GVR order list are missing.
- **Primary docket for ProAPIs 5:25-cv-08393 and Proxycurl 3:25-cv-00828 (dug, unsettled).** CourtListener returned 403.
- **HTTP 999 (dug, unsettled).** No LinkedIn primary description.
- **LinkedIn's GAI training regions and default (dug, unsettled).** Only secondary snippets name the regions (EU/EEA/CH/UK/Canada/HK).
- **Meta v. Bright Data (2024) on logged-out scraping and terms (undug).**
