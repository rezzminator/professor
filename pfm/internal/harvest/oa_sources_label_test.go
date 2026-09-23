package harvest

import (
	"context"
	"net/http"
	"testing"
)

// TestOAOpenAlexLabelsACandidateByWhatItIs: OpenAlex's open_access.oa_url is
// the best copy's landing page unless the copy names a PDF (10.1038/nature14539
// hands out a HAL record page, abstract and metadata only); a record page is
// an HTML candidate, and the location's pdf_url is the PDF one.
func TestOAOpenAlexLabelsACandidateByWhatItIs(t *testing.T) {
	cases := map[string]struct {
		payload string
		want    map[string]string
	}{
		"record page with no pdf_url is HTML": {
			payload: `{"open_access":{"oa_url":"https://hal.science/hal-00000001","oa_status":"green"},
				"best_oa_location":{"is_oa":true,"landing_page_url":"https://hal.science/hal-00000001","pdf_url":null},
				"locations":[{"is_oa":true,"landing_page_url":"https://hal.science/hal-00000001","pdf_url":null}]}`,
			want: map[string]string{"https://hal.science/hal-00000001": kindHTML},
		},
		"oa_url that is the best location's pdf_url is a PDF": {
			payload: `{"open_access":{"oa_url":"https://repo.example/file/paper","oa_status":"green"},
				"best_oa_location":{"is_oa":true,"landing_page_url":"https://repo.example/rec/1","pdf_url":"https://repo.example/file/paper"}}`,
			want: map[string]string{"https://repo.example/file/paper": kindPDF},
		},
		"oa_url ending in .pdf is a PDF, a location's pdf_url is a PDF": {
			payload: `{"open_access":{"oa_url":"https://repo.example/paper.pdf","oa_status":"green"},
				"locations":[{"is_oa":true,"pdf_url":"https://mirror.example/get?id=1"}]}`,
			want: map[string]string{
				"https://repo.example/paper.pdf":  kindPDF,
				"https://mirror.example/get?id=1": kindPDF,
			},
		},
	}
	for name, tc := range cases {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(r, tc.payload), nil
		})}
		got, err := (&Resolver{Client: client}).openAlexDOI(context.Background(), client, "10.1234/example")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		kinds := map[string]string{}
		for _, candidate := range got {
			kinds[candidate.URL] = candidate.Kind
		}
		if len(kinds) != len(tc.want) {
			t.Fatalf("%s: candidates = %#v, want %v", name, got, tc.want)
		}
		for link, kind := range tc.want {
			if kinds[link] != kind {
				t.Errorf("%s: %s labelled %q, want %q (candidates %#v)", name, link, kinds[link], kind, got)
			}
		}
	}
}
