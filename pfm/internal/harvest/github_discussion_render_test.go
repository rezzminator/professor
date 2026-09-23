package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestGitHubDiscussionUnloadedFlagsThePartial: a loader the site refused, or
// answered with a page that is not this discussion's fragment, stays a named
// gap, and the counts it hides flag the artifact partial.
func TestGitHubDiscussionUnloadedFlagsThePartial(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(site *ghdSite)
		want  []string
	}{
		{
			"the hidden items refused",
			func(site *ghdSite) { site.status[ghdPagesURI] = http.StatusInternalServerError },
			[]string{
				"**Comments:** 4 stated · 2 loaded",
				"\"2 hidden items\" loader not loaded",
				"HTTP 500",
			},
		},
		{
			"the replies answered by a sign-in page",
			func(site *ghdSite) {
				site.answers[ghdThreadURI] = "<html><body><h1>Sign in to GitHub</h1></body></html>"
			},
			[]string{
				"**Replies:** 4 stated · 3 loaded",
				"\"Show 1 previous reply\" loader of comment #3963590 not loaded",
				"holding no comment of this discussion",
			},
		},
		{
			"the stated counts removed",
			func(site *ghdSite) {
				site.answers[ghdPath] = strings.Replace(site.answers[ghdPath], "4 comments", "", 1)
			},
			[]string{"the stated comment and reply counts were not read"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := newGHDSite(t)
			tc.setup(site)
			h, _ := site.harvester(t)
			result := h.FetchWithOptions(context.Background(), ghdURL, FetchOptions{Refresh: true})
			if result.Partial == "" {
				t.Fatalf("an unloaded part is not flagged partial:\n%.1500s", result.Content)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.Content+"\n"+result.Partial, want) {
					t.Fatalf(
						"the artifact does not name %q:\npartial=%q\n%.1500s",
						want,
						result.Partial,
						result.Content,
					)
				}
			}
		})
	}
}
