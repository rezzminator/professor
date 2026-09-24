package harvest

import (
	"net/http"
	"strings"
	"testing"
)

// abstractLandingMarkdown is the heading structure the sim measured on an
// abstract-only publisher landing page (link.springer.com, 10.1007/BF00994018):
// an abstract, related-article links, references and the article's apparatus.
func abstractLandingMarkdown() string {
	prose := strings.Repeat("The support-vector network is a new learning machine for two-group classification. ", 11)
	return "# Support-vector networks\n\n## Abstract\n\n" + prose + "\n\n## Article PDF\n\n" +
		"### Similar content being viewed by others\n\n### [A related chapter](https://publisher.test/chapter/1)\n\n" +
		"## References\n\n" + strings.Repeat("1. A. Author. A cited work. A journal, 1990.\n", 60) +
		"\n## Author information\n\n### Authors and Affiliations\n\n## Rights and permissions\n\n" +
		strings.Repeat("Reprints and permissions for this article. ", 7) +
		"\n\n## About this article\n\n### Cite this article\n\nAuthor, A. Support-vector networks. 1995.\n\n### Keywords\n\n" +
		strings.Repeat("learning machines, ", 28)
}

// registerLandingMarkdown is science.org's measured shape for
// 10.1126/science.1127647: the abstract and references under one access box.
func registerLandingMarkdown() string {
	return "**Title:** Reducing the Dimensionality of Data\n\n## Register and access this article for free\n\n" +
		strings.Repeat("High-dimensional data can be converted to low-dimensional codes by training a network. ", 280)
}

// recordPageMarkdown is hal.science's measured shape for its record of
// 10.1038/nature14539: the site's search form, the authors and their
// affiliations, the abstract, the domains and the record's cite, export and
// share boxes — 5 sections past 200 characters and 5,168 characters of prose
// under headings that are not an article's apparatus, and no body at all.
func recordPageMarkdown() string {
	lines := func(line string, n int) string { return strings.Repeat(line+"\n", n) }
	return "  ### Advanced Search\n\n" + lines("+ Work title", 280) + "  ### Search using SolR syntax\n\n" +
		lines("+ Subtitle", 10) + "## Deep learning\n\n" + lines("Département d'Informatique [Montreal]", 16) +
		"#### Abstract\n\n" + strings.Repeat("Deep learning allows computational models to learn. ", 16) + "\n\n" +
		"#### Domains\n\n" + lines("* [Artificial Intelligence [cs.AI]](/search/index/q/ai)", 10) +
		"### Dates and versions\n\nhal-04206682 , version 1\n\n### Identifiers\n\nHAL Id : hal-04206682\n\n" +
		"### Cite\n\n" + lines("Yann Lecun, Yoshua Bengio. Deep learning. Nature, 2015.", 4) +
		"### Export\n\n" + lines("BibTeX, TEI Dublin Core, DC, EndNote", 10) + "### Altmetric\n\n### Share\n\n" +
		lines("Gmail Facebook X LinkedIn More", 60)
}

func articleLandingMarkdown() string {
	section := func(name string) string {
		return "## " + name + "\n\n" + strings.Repeat(
			"The body of the article develops its argument in full prose here. ",
			30,
		) + "\n\n"
	}
	return "# An article\n\n## Abstract\n\nA short abstract.\n\n" + section("1. Introduction") + section("2. Methods") +
		section("3. Results") + section("4. Discussion") + "## References\n\n1. A cited work.\n"
}

// TestLandingFullTextTellsAnAbstractPageFromAnArticle: the measured abstract
// pages carry no body section; an article carries its body under several.
func TestLandingFullTextTellsAnAbstractPageFromAnArticle(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		want    bool
	}{
		"springer abstract page": {abstractLandingMarkdown(), false},
		"science register box":   {registerLandingMarkdown(), false},
		"hal record page":        {recordPageMarkdown(), false},
		"full-text article":      {articleLandingMarkdown(), true},
	} {
		if got, measure := landingFullText(tc.content); got != tc.want {
			t.Errorf("%s: landingFullText = %v (%s), want %v", name, got, measure, tc.want)
		}
	}
}

func abstractLandingHTML(body string) string {
	return "<html><head><title>Support-vector networks</title></head><body><article><h1>Support-vector networks</h1>" + body + "</article></body></html>"
}

// TestAbstractOnlyLandingLosesToTheOpenAccessCopy: read (publications) on a DOI with a
// caller header whose landing page reads cleanly but carries only the
// abstract answers from the open-access copy, and partial names why the
// landing page was not used.
func TestAbstractOnlyLandingLosesToTheOpenAccessCopy(t *testing.T) {
	ctx, _ := captureLogs(t)
	abstract := "<h2>Abstract</h2><p>" + strings.Repeat(
		"The support-vector network is a new learning machine. ",
		18,
	) + "</p>" +
		"<h2>References</h2><ol>" + strings.Repeat(
		"<li>A. Author. A cited work. A journal, 1990.</li>",
		40,
	) + "</ol>" +
		"<h2>Rights and permissions</h2><p>" + strings.Repeat(
		"Reprints and permissions. ",
		12,
	) + "</p>"
	web := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "doi.org":
			moved := response(r, http.StatusFound, "text/html", "")
			moved.Header.Set("Location", "https://publisher.test/article/abstract-test")
			return moved, nil
		case "publisher.test":
			return response(r, http.StatusOK, "text/html", abstractLandingHTML(abstract)), nil
		case "api.openalex.org":
			return jsonResponse(
				r,
				`{"open_access":{"oa_url":"https://mirror.test/copy.html","oa_status":"green"}}`,
			), nil
		case "mirror.test":
			return response(r, http.StatusOK, "text/html",
				"<html><body>"+strings.Repeat("<p>the work's full text</p>", 80)+"</body></html>"), nil
		}
		return response(r, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(), Client: &http.Client{Transport: web}, Chrome: &http.Client{Transport: web},
		Jina: &http.Client{Transport: web}, OA: &http.Client{Transport: web}, Converter: &fakeConverter{},
	})
	got := probeLines().Fetch(ctx, h, "10.9999/abstract-test", FetchOptions{})
	if got.Error != "" {
		t.Fatalf("Fetch error = %q, want the open-access copy", got.Error)
	}
	if !strings.Contains(got.Content, "the work's full text") {
		t.Fatalf("content = %.300q, want the open-access full text, not the abstract page", got.Content)
	}
	if !strings.Contains(got.Partial, "carries no full text") || !strings.Contains(got.Partial, headerlessNote) {
		t.Fatalf("partial = %q, want the abstract-only landing page and the headerless copy named", got.Partial)
	}
}
