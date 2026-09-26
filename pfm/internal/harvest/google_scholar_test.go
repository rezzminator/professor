package harvest

import (
	"context"
	"net/http"
	"testing"
)

func TestGoogleScholarVersionsPageSuppliesSecondPagePDF(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	var versionRequests int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("cluster") == "123" {
			versionRequests++
			return response(
				r,
				http.StatusOK,
				"text/html",
				`<div class="gs_ri"><h3 class="gs_rt"><a href="https://doi.org/10.1234/provider.fixture">Versions fixture</a></h3><div class="gs_a">A Author - Journal, 2020 - repository.example</div><div class="gs_or_ggsm"><a href="https://repository.test/versions.pdf">[PDF]</a></div></div>`,
			), nil
		}
		return response(
			r,
			http.StatusOK,
			"text/html",
			`<div class="gs_ri"><h3 class="gs_rt"><a href="https://doi.org/10.1234/provider.fixture">Versions fixture</a></h3><div class="gs_a">A Author - Journal, 2020 - repository.example</div><div class="gs_fl"><a href="/scholar?cluster=123">All 2 versions</a></div></div>`,
		), nil
	})}
	resolver := &Resolver{Client: client, GoogleScholarURL: "https://scholar.test"}
	got, err := resolver.googleScholar(context.Background(), "fixture", 4)
	if err != nil || len(got) != 1 || got[0].URL != "https://repository.test/versions.pdf" || versionRequests != 1 {
		t.Fatalf("Scholar versions result = %#v err=%v requests=%d", got, err, versionRequests)
	}
}
