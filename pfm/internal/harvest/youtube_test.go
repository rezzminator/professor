package harvest

import (
	"context"
	"strings"
	"testing"
)

// The fixtures are a captured YouTube watch page and the two answers read for
// it (testdata/youtube), trimmed and scrubbed: the video id, title, channel,
// description and caption words are placeholders; the page is cut to its
// player chrome and the two scripts, ytInitialPlayerResponse (videoDetails,
// the one captioned track the page names, its microformat) and ytInitialData
// (the comments panel stating "5K", the chapter markers cut to three); the
// player answer (the Android client's) to its videoDetails and caption track;
// the json3 track to a few of its captured events, the line-break appends
// among them.
const (
	youtubeURL        = "https://www.youtube.com/watch?v=abcDEF12345"
	youtubePlayerAPI  = "www.youtube.com/youtubei/v1/player?prettyPrint=false"
	youtubeTimedText  = "www.youtube.com/api/timedtext?v=abcDEF12345&ei=placeholder&caps=asr&opi=112496729"
	youtubeTrackQuery = "&xoaf=5&xowf=1&hl=en&ip=0.0.0.0&ipbits=0&expire=1790172023" +
		"&sparams=ip,ipbits,expire,v,ei,caps,opi,xoaf&signature=placeholder&key=yt8&kind=asr&lang=en&fmt=json3"
)

func youtubeSite(t *testing.T) *socialSite {
	return &socialSite{answers: map[string]string{
		"www.youtube.com/watch?v=abcDEF12345": socialFixture(t, "youtube/watch-page.html"),
		youtubePlayerAPI:                      socialFixture(t, "youtube/player.json"),
		youtubeTimedText + youtubeTrackQuery:  socialFixture(t, "youtube/transcript.json"),
	}}
}

// TestYouTubeVideoLoadsTranscript: a watch page renders its title, channel,
// description and chapters from its scripts, never its player chrome; the
// transcript is read from the player API's caption track as json3 on the
// page's own host and renders as timestamped paragraphs, its caption events
// counted (a line-break append is not one); the comments are named unloaded
// with the page's stated count; a second harvest is identical.
func TestYouTubeVideoLoadsTranscript(t *testing.T) {
	site := youtubeSite(t)
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), youtubeURL, FetchOptions{Refresh: true})
	if result.Error != "" {
		t.Fatalf("the harvest failed: %s", result.Error)
	}
	for _, text := range []string{
		"# A talk title",
		"**Channel:** Channel Name",
		"**Video:** " + youtubeURL,
		"**Published:** 2023-11-23 02:27 UTC",
		"**Length:** 59:48",
		"The first line of the description.  \nA second line with a link: https://example.com/slides",
		"- 0:00 Intro\n- 0:20 The second part\n- 4:17 The third part",
		"**Captions:** English (auto-generated) (automatic (ASR): no manual track in the page language) · " +
			"4 caption events stated · 4 loaded",
		"[0:00] hello everyone this is the first 30-minute talk in the series\n\n[0:31] a later line\n\n" +
			"[1:00:01] the last line",
		"**Comments:** 5K stated · 0 loaded",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.3000s", text, result.Content)
		}
	}
	for _, chrome := range []string{"Tap to unmute", "signed out", "Include playlist"} {
		if strings.Contains(result.Content, chrome) {
			t.Fatalf("the artifact stores the player chrome %q:\n%.3000s", chrome, result.Content)
		}
	}
	if !strings.Contains(result.Partial, "5K comments were not loaded") ||
		strings.Contains(result.Partial, "transcript") {
		t.Fatalf("partial = %q, want the comments named unloaded and nothing else", result.Partial)
	}
	if strings.Join(site.requests, "\n") != youtubeTimedText+youtubeTrackQuery {
		t.Fatalf("timedtext requests %v", site.requests)
	}
	again := h.FetchWithOptions(context.Background(), youtubeURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same video differs")
	}
}

// TestYouTubeVideoTakesTheManualTrack: a manual track in the page's language
// is read before the automatic one, and named as such.
func TestYouTubeVideoTakesTheManualTrack(t *testing.T) {
	site := youtubeSite(t)
	player := site.answers[youtubePlayerAPI]
	asr := `{"baseUrl":"https://www.youtube.com/api/timedtext?v=abcDEF12345&ei=placeholder&caps=asr`
	manual := `{"baseUrl":"https://www.youtube.com/api/timedtext?v=abcDEF12345&ei=placeholder&lang=en&fmt=srv3",` +
		`"name":{"runs":[{"text":"English"}]},"vssId":".en","languageCode":"en","isTranslatable":true,` +
		`"trackName":""},`
	site.answers[youtubePlayerAPI] = strings.Replace(player, asr, manual+asr, 1)
	manualKey := "www.youtube.com/api/timedtext?v=abcDEF12345&ei=placeholder&lang=en&fmt=json3"
	site.answers[manualKey] = site.answers[youtubeTimedText+youtubeTrackQuery]
	result := site.harvester(t).FetchWithOptions(context.Background(), youtubeURL, FetchOptions{Refresh: true})
	if !strings.Contains(result.Content, "**Captions:** English (manual, in the page language) · 4 caption") {
		t.Fatalf("the manual track was not the one read:\n%.3000s", result.Content)
	}
	if strings.Join(site.requests, "\n") != manualKey {
		t.Fatalf("timedtext requests %v", site.requests)
	}
}

// TestYouTubeVideoWithoutCaptionsNamesTheGap: a video whose page names no
// caption track is rendered with the gap named, no caption request sent.
func TestYouTubeVideoWithoutCaptionsNamesTheGap(t *testing.T) {
	site := youtubeSite(t)
	page := site.answers["www.youtube.com/watch?v=abcDEF12345"]
	start := strings.Index(page, `"captions":`)
	end := strings.Index(page, `"videoDetails":`)
	if start < 0 || end < start {
		t.Fatal("the fixture page lost its captions object")
	}
	site.answers["www.youtube.com/watch?v=abcDEF12345"] = page[:start] + page[end:]
	result := site.harvester(t).FetchWithOptions(context.Background(), youtubeURL, FetchOptions{Refresh: true})
	if !strings.Contains(result.Content, "# A talk title") ||
		!strings.Contains(result.Content, "*The video has no captions: no transcript to read.*") {
		t.Fatalf("the missing captions are not named:\n%.3000s", result.Content)
	}
	if !strings.Contains(result.Partial, "the video has no captions") {
		t.Fatalf("partial = %q, want the missing captions named", result.Partial)
	}
	if len(site.requests) != 0 {
		t.Fatalf("a caption request was sent for a video with none: %v", site.requests)
	}
}
