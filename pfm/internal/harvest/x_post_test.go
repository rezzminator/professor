package harvest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The fixtures are captured FxTwitter API v2 answers (testdata/x), scrubbed:
// handles example_user, ids 10000000000000000NN, texts placeholders, media
// and card addresses example paths. thread.json is a post with a video and
// the author's one-post continuation carrying a link card (/2/thread);
// note.json a long "note" post (is_note_tweet) past the classic 280
// characters, as /2/thread answers it. post-page.html is the signed-out page
// x.com serves: the post's preview under its sign-in bar.
const (
	xTestID   = "1000000000000000001"
	xTestPath = "/example_user/status/" + xTestID
	xTestURL  = "https://x.com" + xTestPath
	// xTestAPIHost is the FxTwitter API's host, spelled out: the test pins
	// the public route the extractor reads.
	xTestAPIHost = "api.fxtwitter.com"
	xTestThread  = xTestAPIHost + "/2/thread/" + xTestID
)

func xSite(t *testing.T, thread string) *socialSite {
	page := socialFixture(t, "x/post-page.html")
	return &socialSite{
		apiHosts: map[string]bool{xTestAPIHost: true},
		answers: map[string]string{
			"x.com" + xTestPath: page,
			xTestThread:         thread,
		},
	}
}

// TestXStatusAddresses: the post addresses the X extractor claims, each with
// the post id its API request names, and the X pages it leaves to the generic
// path.
func TestXStatusAddresses(t *testing.T) {
	t.Parallel()
	var extractor *siteExtractor
	for index := range siteExtractors {
		if siteExtractors[index].name == "x-post" {
			extractor = &siteExtractors[index]
		}
	}
	if extractor == nil {
		t.Fatal("no x-post site extractor is registered")
	}
	doc, err := html.Parse(strings.NewReader(socialFixture(t, "x/post-page.html")))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source string
		id     string
	}{
		{source: xTestURL, id: xTestID},
		{source: "https://twitter.com" + xTestPath, id: xTestID},
		{source: "https://mobile.twitter.com" + xTestPath, id: xTestID},
		{source: "https://www.x.com" + xTestPath + "?s=20", id: xTestID},
		{source: "https://x.com/example_user/statuses/" + xTestID, id: xTestID},
		{source: "https://x.com/i/web/status/" + xTestID, id: xTestID},
		{source: "https://x.com/i/status/" + xTestID, id: xTestID},
		{source: xTestURL + "/photo/2", id: xTestID},
		{source: xTestURL + "/video/1", id: xTestID},
		{source: "https://x.com/example_user"},
		{source: xTestURL + "/likes"},
		{source: "https://x.com/example_user/status/not-a-number"},
		{source: "https://x.com/example_user/status/"},
		{source: "https://example.org" + xTestPath},
		{source: "https://" + xTestAPIHost + "/2/thread/" + xTestID},
	} {
		page, err := url.Parse(tc.source)
		if err != nil {
			t.Fatal(err)
		}
		if claimed := extractor.claims(page, doc); claimed != (tc.id != "") {
			t.Errorf("%s: claimed %v, want %v", tc.source, claimed, tc.id != "")
		}
		if id, _ := xStatusID(page); id != tc.id {
			t.Errorf("%s: post id %q, want %q", tc.source, id, tc.id)
		}
	}
}

// TestXPostReadsTheFullPostFromTheFxTwitterAPI: an X post is read from the
// FxTwitter API rather than from the signed-out page: the post's whole text,
// its video, its stated counts and the author's continuation with its link
// card; no sign-in wall is named and no credential sent. Every other address
// of a post names the same API request (TestXStatusAddresses).
func TestXPostReadsTheFullPostFromTheFxTwitterAPI(t *testing.T) {
	t.Parallel()
	site := xSite(t, socialFixture(t, "x/thread.json"))
	result := site.harvester(t).FetchWithOptions(context.Background(), xTestURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the post is not complete: method=%q partial=%q error=%q\n%.1500s",
			result.Method, result.Partial, result.Error, result.Content)
	}
	for _, text := range []string{
		"# Example User (@example_user) on X\n\n",
		"**Author:** @example_user · **Posted:** 2026-07-26 18:55 UTC  \n",
		"**Post:** https://x.com/example_user/status/" + xTestID + "  \n",
		"**Replies:** 21 (not read) · **Reposts:** 17 · **Quotes:** 6 · **Likes:** 40 · **Views:** 1000\n\n",
		"A first paragraph of the post.\n\nA second paragraph of the post.\n\nA third paragraph & its last line\n\n",
		"[Video (3:08)](https://video.twimg.com/amplify_video/1000000000000000003/vid/avc1/1280x720/example.mp4)",
		"## The author's thread continues\n\n",
		"- **@example_user** · 2026-07-26 18:56 UTC · " +
			"[1000000000000000002](https://x.com/example_user/status/1000000000000000002)\n" +
			"  https://example.org/project\n\n" +
			"  [Example project: a page about the project](https://example.org/project) — " +
			"A page describing the project.\n",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%s", text, result.Content)
		}
	}
	if strings.Contains(result.Content, "Don’t miss what’s happening") {
		t.Fatalf("the artifact holds the signed-out page's wall:\n%s", result.Content)
	}
	if strings.Join(site.requests, "\n") != xTestThread {
		t.Fatalf("API requests %v, want the post's thread alone", site.requests)
	}
	for _, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
			t.Fatalf("an API request carried a credential: %v", header)
		}
	}
}

// TestXNotePostReadsItsWholeText: a long "note" post reads in full, past the
// 280 characters the signed-out preview and X's own embed stop at.
func TestXNotePostReadsItsWholeText(t *testing.T) {
	t.Parallel()
	var answer struct {
		Status struct {
			Text        string `json:"text"`
			IsNoteTweet bool   `json:"is_note_tweet"`
		} `json:"status"`
	}
	note := socialFixture(t, "x/note.json")
	if err := json.Unmarshal([]byte(note), &answer); err != nil {
		t.Fatal(err)
	}
	if !answer.Status.IsNoteTweet || len(answer.Status.Text) <= 280 {
		t.Fatalf("the fixture is not a long note post: note %v, %d chars", answer.Status.IsNoteTweet,
			len(answer.Status.Text))
	}
	result := xSite(t, note).harvester(t).FetchWithOptions(context.Background(), xTestURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the note post is not complete: partial=%q error=%q", result.Partial, result.Error)
	}
	if !strings.Contains(result.Content, "\n\n"+answer.Status.Text+"\n") {
		t.Fatalf("the artifact lacks the note's whole text:\n%s", result.Content)
	}
}

// xStatusAnswer is a /2/thread answer holding one post: the minimal post the
// API documents (APITwitterStatus) with fields set over it.
func xStatusAnswer(t *testing.T, fields map[string]any) string {
	t.Helper()
	status := map[string]any{
		"type": "status", "id": xTestID, "url": "https://x.com/example_user/status/" + xTestID,
		"text": "The post's text.", "created_at": "Sun Jul 26 18:55:44 +0000 2026",
		"created_timestamp": 1785092144, "likes": 1, "reposts": 0, "quotes": 0, "replies": 0,
		"author": map[string]any{"type": "profile", "name": "Example User", "screen_name": "example_user"},
		"media":  map[string]any{}, "raw_text": map[string]any{"text": "The post's text."},
		"is_note_tweet": false, "community_note": nil, "replying_to": nil, "provider": "twitter",
	}
	for key, value := range fields {
		status[key] = value
	}
	body, err := json.Marshal(map[string]any{"code": 200, "status": status, "thread": []any{status}})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestXPostRendersEveryFormTheAPIDocuments: each form APITwitterStatus
// documents renders in the post — media of each kind, a quoted post or its
// tombstone, a poll, a community note, a reply's parent, an article's
// blocks, the author's earlier thread posts (a deleted one among them) — and
// an article block of a kind this reader does not know flags
// the artifact partial, named.
func TestXPostRendersEveryFormTheAPIDocuments(t *testing.T) {
	t.Parallel()
	articleBlock := func(key, kind, text string, entity int) map[string]any {
		ranges := []any{}
		if entity >= 0 {
			ranges = append(ranges, map[string]any{"key": entity, "offset": 0, "length": 1})
		}
		return map[string]any{
			"key": key, "type": kind, "text": text, "data": map[string]any{},
			"entityRanges": ranges, "inlineStyleRanges": []any{},
		}
	}
	article := func(blocks, entities []any) map[string]any {
		return map[string]any{
			"id": "1000000000000000010", "title": "An article title", "preview_text": "A paragraph.",
			"created_at": "2026-07-26T18:55:44.000Z",
			"content":    map[string]any{"blocks": blocks, "entityMap": entities},
			"media_entities": []any{map[string]any{
				"id": "1", "media_key": "3_1000000000000000011", "media_id": "1000000000000000011",
				"media_info": map[string]any{
					"__typename":       "ApiImage",
					"original_img_url": "https://pbs.twimg.com/media/article-example.jpg",
				},
			}},
		}
	}
	entity := func(key, kind string, data map[string]any) map[string]any {
		return map[string]any{"key": key, "value": map[string]any{"type": kind, "data": data}}
	}
	for _, tc := range []struct {
		name    string
		fields  map[string]any
		earlier []any // the author's thread posts before the post
		// outOfThread: the API's thread holds the earlier posts, not the post
		outOfThread bool
		want        []string
		partial     string
	}{
		{
			name: "the author's thread without the post itself",
			earlier: []any{map[string]any{
				"type": "status", "id": "1000000000000000012",
				"url": "https://x.com/example_user/status/1000000000000000012", "text": "The thread's opening post.",
				"created_timestamp": 1785091800,
				"author":            map[string]any{"name": "Example User", "screen_name": "example_user"},
				"media":             map[string]any{},
			}},
			outOfThread: true,
			want: []string{
				"The post's text.\n\n---\n\n## The author's thread\n\n" +
					"- **@example_user** · 2026-07-26 18:50 UTC · " +
					"[1000000000000000012](https://x.com/example_user/status/1000000000000000012)\n" +
					"  The thread's opening post.\n",
			},
		},
		{
			name: "a post in the middle of the author's thread, one earlier post deleted",
			earlier: []any{
				map[string]any{
					"type": "status", "id": "1000000000000000012",
					"url": "https://x.com/example_user/status/1000000000000000012", "text": "The thread's opening post.",
					"created_timestamp": 1785091800,
					"author":            map[string]any{"name": "Example User", "screen_name": "example_user"},
					"media":             map[string]any{},
				},
				map[string]any{
					"type": "tombstone", "provider": "twitter", "reason": "deleted",
					"message": "This post was deleted by the post author.",
				},
			},
			want: []string{
				"## Earlier in the author's thread\n\n" +
					"- **@example_user** · 2026-07-26 18:50 UTC · " +
					"[1000000000000000012](https://x.com/example_user/status/1000000000000000012)\n" +
					"  The thread's opening post.\n" +
					"- *a post of the thread is unavailable (deleted): This post was deleted by the post author.*\n" +
					"\n---\n\nThe post's text.",
			},
		},
		{
			name: "a photo with its alt text, a gif and a media kind the API adds later",
			fields: map[string]any{"media": map[string]any{"all": []any{
				map[string]any{
					"type": "photo", "url": "https://pbs.twimg.com/media/example.jpg",
					"altText": "A chart", "width": 1, "height": 1,
				},
				map[string]any{
					"type": "gif", "url": "https://video.twimg.com/tweet_video/example.mp4",
					"width": 1, "height": 1, "duration": 0, "formats": []any{},
				},
				map[string]any{"type": "audio", "url": "https://example.org/a.mp3", "width": 0, "height": 0},
			}}},
			want: []string{
				"![A chart](https://pbs.twimg.com/media/example.jpg)",
				"[GIF](https://video.twimg.com/tweet_video/example.mp4)",
				"[audio](https://example.org/a.mp3)",
			},
		},
		{
			name: "photos and videos listed apart, a mosaic of them, an external video and a broadcast",
			fields: map[string]any{"media": map[string]any{
				"photos": []any{map[string]any{
					"type": "photo", "url": "https://pbs.twimg.com/media/one.jpg",
					"width": 1, "height": 1,
				}},
				"videos": []any{map[string]any{
					"type": "video", "url": "https://video.twimg.com/two.mp4",
					"width": 1, "height": 1, "duration": 59.9, "formats": []any{},
				}},
				"mosaic": map[string]any{
					"type":    "mosaic_photo",
					"formats": map[string]any{"webp": "https://mosaic.example/m.webp", "jpeg": "https://mosaic.example/m.jpg"},
				},
				"external": map[string]any{"type": "video", "url": "https://www.youtube.com/watch?v=example"},
				"broadcast": map[string]any{
					"url": "https://x.com/i/broadcasts/example", "title": "A live talk",
					"state": "ENDED",
				},
			}},
			want: []string{
				"![](https://pbs.twimg.com/media/one.jpg)",
				"[Video (0:59)](https://video.twimg.com/two.mp4)",
				"[External video](https://www.youtube.com/watch?v=example)",
				"[Broadcast: A live talk (ENDED)](https://x.com/i/broadcasts/example)",
			},
		},
		{
			name: "a quoted post",
			fields: map[string]any{"quote": map[string]any{
				"type": "status", "id": "1000000000000000005",
				"url": "https://x.com/other_user/status/1000000000000000005", "text": "The quoted words.",
				"created_timestamp": 1785000000, "author": map[string]any{"name": "Other", "screen_name": "other_user"},
				"media": map[string]any{},
			}},
			want: []string{"> **Quoting @other_user** · 2026-07-25 17:20 UTC · " +
				"[1000000000000000005](https://x.com/other_user/status/1000000000000000005)\n>\n> The quoted words."},
		},
		{
			name: "a quoted post no longer there",
			fields: map[string]any{"quote": map[string]any{
				"type": "tombstone", "provider": "twitter", "reason": "deleted",
				"message": "This post was deleted by the post author.",
			}},
			want: []string{"> *The quoted post is unavailable (deleted): This post was deleted by the post author.*"},
		},
		{
			name: "a poll, a link card and a community note",
			fields: map[string]any{
				"poll": map[string]any{"choices": []any{
					map[string]any{"label": "Yes", "count": 30, "percentage": 75},
					map[string]any{"label": "No", "count": 10, "percentage": 25},
				}, "total_votes": 40, "ends_at": "2026-07-27T18:55:44Z", "time_left_en": "Final results"},
				"card":           map[string]any{"url": "https://example.org/story", "title": "A story"},
				"community_note": map[string]any{"text": "Readers added context.", "facets": []any{}},
			},
			want: []string{
				"**Poll** · 40 votes · Final results\n- Yes: 30 (75%)\n- No: 10 (25%)",
				"[A story](https://example.org/story)",
				"> **Community note:** Readers added context.",
			},
		},
		{
			name: "a reply to another account's post",
			fields: map[string]any{"replying_to": map[string]any{
				"screen_name": "other_user", "status": "1000000000000000006",
				"url": "https://x.com/other_user/status/1000000000000000006",
			}},
			want: []string{"**In reply to:** @other_user · https://x.com/other_user/status/1000000000000000006  \n"},
		},
		{
			name: "an article",
			fields: map[string]any{"article": article([]any{
				articleBlock("a", "header-one", "A heading", -1),
				articleBlock("b", "unstyled", "A paragraph.\nIts second line.", -1),
				articleBlock("c", "unordered-list-item", "An item", -1),
				articleBlock("d", "ordered-list-item", "A step", -1),
				articleBlock("e", "blockquote", "A quote.", -1),
				articleBlock("f", "atomic", " ", 0),
				articleBlock("g", "atomic", " ", 1),
				articleBlock("h", "atomic", " ", 2),
				articleBlock("i", "header-two", "A subheading", -1),
			}, []any{
				entity("0", "MEDIA", map[string]any{"entityKey": "0", "mediaItems": []any{
					map[string]any{
						"localMediaId": "1", "mediaCategory": "DraftTweetImage",
						"mediaId": "1000000000000000011",
					},
				}}),
				entity("1", "MARKDOWN", map[string]any{"entityKey": "1", "markdown": "```go\nfmt.Println()\n```"}),
				entity("2", "TWEET", map[string]any{"tweetId": "1000000000000000007"}),
			})},
			want: []string{
				"## An article title\n\n### A heading\n\nA paragraph.  \nIts second line.\n\n- An item\n\n1. A step\n\n" +
					"> A quote.\n\n![](https://pbs.twimg.com/media/article-example.jpg)\n\n```go\nfmt.Println()\n```\n\n" +
					"[post 1000000000000000007](https://x.com/i/status/1000000000000000007)\n\n#### A subheading",
			},
		},
		{
			name: "an article block of a kind this reader does not know",
			fields: map[string]any{"article": article([]any{
				articleBlock("a", "unstyled", "A paragraph.", -1),
				articleBlock("b", "atomic", " ", 0),
			}, []any{entity("0", "LATEX", map[string]any{"entityKey": "0"})})},
			want:    []string{"## An article title\n\nA paragraph."},
			partial: "x post: the article's LATEX block was not rendered (a kind this reader does not know)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			answer := xStatusAnswer(t, tc.fields)
			if tc.earlier != nil {
				var thread map[string]any
				if err := json.Unmarshal([]byte(answer), &thread); err != nil {
					t.Fatal(err)
				}
				posts := thread["thread"].([]any)
				if tc.outOfThread {
					posts = nil
				}
				thread["thread"] = append(tc.earlier, posts...)
				raw, err := json.Marshal(thread)
				if err != nil {
					t.Fatal(err)
				}
				answer = string(raw)
			}
			site := xSite(t, answer)
			result := site.harvester(t).FetchWithOptions(context.Background(), xTestURL, FetchOptions{Refresh: true})
			if result.Error != "" || result.Partial != tc.partial {
				t.Fatalf("partial=%q error=%q, want partial %q\n%s", result.Partial, result.Error, tc.partial,
					result.Content)
			}
			for _, text := range tc.want {
				if !strings.Contains(result.Content, text) {
					t.Fatalf("the artifact lacks %q:\n%s", text, result.Content)
				}
			}
		})
	}
}

// TestXPostNotLoadedServesThePage: a post the API would not answer, or
// answers unavailable, is not claimed; the page goes the generic path with
// the gap and its cause named.
func TestXPostNotLoadedServesThePage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		answer string
		cause  string
	}{
		{name: "the API refuses", status: http.StatusNotFound, cause: "HTTP 404"},
		{
			name: "the API answers the post is private",
			answer: `{"code":401,"status":{"type":"tombstone","provider":"twitter","reason":"private",` +
				`"message":"This post is from an account that is private."},"thread":null,"author":null}`,
			cause: "answered that the post is private: This post is from an account that is private.",
		},
		{name: "the API answers another post", answer: strings.ReplaceAll(xStatusAnswer(t, nil), xTestID,
			"1000000000000000099"), cause: "answered by another post (1000000000000000099)"},
		{name: "the API answers no JSON", answer: "<html>busy</html>", cause: "not the FxTwitter API's JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			site := xSite(t, tc.answer)
			if tc.status != 0 {
				site.status = map[string]int{xTestThread: tc.status}
			}
			result := site.servingHarvester(t).
				FetchWithOptions(context.Background(), xTestURL, FetchOptions{Refresh: true})
			for _, want := range []string{"x post: the post was not loaded from the FxTwitter API", tc.cause} {
				if !strings.Contains(result.Partial, want) {
					t.Fatalf("the partial marker lacks %q: %q (error %q)", want, result.Partial, result.Error)
				}
			}
		})
	}
}
