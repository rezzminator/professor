package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The YouTube video extractor. A watch page renders a player whose markup is
// chrome ("Tap to unmute", a share dialog); the video itself is in two scripts
// the page carries: ytInitialPlayerResponse (videoDetails: title, channel,
// length, description; the caption tracks) and ytInitialData (the chapters,
// the stated comment count). The caption tracks the page names answer empty
// to a request without the player's proof-of-origin token, so the transcript
// is read in two loaders on the page's own host, through loaders.go's budget:
// the Innertube player API (as the Android client, whose caption tracks need
// no token) names the tracks again, and the chosen track — the manual track in
// the page's language, else the automatic (ASR) one — is read as json3 from
// its same-host timedtext address. Each answer is checked to be this video's
// before it is kept in the page as a harvester-youtube-answer element, so a
// later conversion replays it like any followed loader. Comments load only
// through YouTube's continuation API, which is not followed: the stated count
// is named, unloaded.

const (
	youtubeHost      = "youtube.com"
	youtubeAnswerTag = "harvester-youtube-answer"
	youtubePlayerVar = "ytInitialPlayerResponse"
	youtubeDataVar   = "ytInitialData"
	youtubeKindASR   = "asr"
	// youtubeParagraph: a transcript paragraph starts every 30 seconds.
	youtubeParagraph = 30000
)

type youtubeText struct {
	SimpleText string `json:"simpleText"`
	Runs       []struct {
		Text string `json:"text"`
	} `json:"runs"`
}

func (text youtubeText) String() string {
	if text.SimpleText != "" {
		return text.SimpleText
	}
	var out strings.Builder
	for _, run := range text.Runs {
		out.WriteString(run.Text)
	}
	return out.String()
}

type youtubeTrack struct {
	BaseURL      string      `json:"baseUrl"`
	Name         youtubeText `json:"name"`
	LanguageCode string      `json:"languageCode"`
	Kind         string      `json:"kind"`
}

type youtubePlayer struct {
	Captions *struct {
		Renderer struct {
			Tracks []youtubeTrack `json:"captionTracks"`
		} `json:"playerCaptionsTracklistRenderer"`
	} `json:"captions"`
	VideoDetails struct {
		VideoID          string `json:"videoId"`
		Title            string `json:"title"`
		LengthSeconds    string `json:"lengthSeconds"`
		Author           string `json:"author"`
		ShortDescription string `json:"shortDescription"`
	} `json:"videoDetails"`
	Microformat struct {
		Renderer struct {
			PublishDate string `json:"publishDate"`
		} `json:"playerMicroformatRenderer"`
	} `json:"microformat"`
}

func (player *youtubePlayer) tracks() []youtubeTrack {
	if player.Captions == nil {
		return nil
	}
	return player.Captions.Renderer.Tracks
}

type youtubeChapter struct {
	Title   youtubeText `json:"title"`
	StartMS int64       `json:"timeRangeStartMillis"`
}

type youtubeData struct {
	EngagementPanels []struct {
		Panel struct {
			Identifier string `json:"panelIdentifier"`
			Header     struct {
				Title struct {
					Info youtubeText `json:"contextualInfo"`
				} `json:"engagementPanelTitleHeaderRenderer"`
			} `json:"header"`
		} `json:"engagementPanelSectionListRenderer"`
	} `json:"engagementPanels"`
	PlayerOverlays struct {
		Overlay struct {
			Bar struct {
				Bar struct {
					PlayerBar struct {
						Markers struct {
							Map []struct {
								Value struct {
									Chapters []struct {
										Chapter youtubeChapter `json:"chapterRenderer"`
									} `json:"chapters"`
								} `json:"value"`
							} `json:"markersMap"`
						} `json:"multiMarkersPlayerBarRenderer"`
					} `json:"playerBar"`
				} `json:"decoratedPlayerBarRenderer"`
			} `json:"decoratedPlayerBarRenderer"`
		} `json:"playerOverlayRenderer"`
	} `json:"playerOverlays"`
}

type youtubeTranscript struct {
	Events *[]struct {
		StartMS int64 `json:"tStartMs"`
		Append  int   `json:"aAppend"`
		Segs    []struct {
			Text string `json:"utf8"`
		} `json:"segs"`
	} `json:"events"`
}

// youtubePage is what a watch page holds: its player response, its initial
// data and the API answers kept for it.
type youtubePage struct {
	id         string
	lang       string
	player     youtubePlayer
	data       youtubeData
	api        youtubePlayer
	apiRead    bool
	transcript youtubeTranscript
	textRead   bool
	dropped    bool
}

// isYouTubeWatch accepts a watch address naming a video.
func isYouTubeWatch(page *url.URL) bool {
	return page.Path == "/watch" && page.Query().Get("v") != ""
}

// youtubeScriptJSON decodes the object a page script assigns to name
// ("var name = {...};"); false when no script assigns it.
func youtubeScriptJSON(doc *html.Node, name string, into any) bool {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != nil {
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Script &&
			strings.Contains(rawText(node), "var "+name+" = ") {
			found = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if found == nil {
		return false
	}
	_, rest, _ := strings.Cut(rawText(found), "var "+name+" = ")
	if err := json.NewDecoder(strings.NewReader(rest)).Decode(into); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a YouTube page's script object does not decode",
			"name", name, obs.FieldErr, err.Error())
		return false
	}
	return true
}

// youtubePageOf reads the video a watch page holds and the answers kept for
// it; false for any other page (a consent wall, a removed video).
func youtubePageOf(doc *html.Node, page *url.URL) (youtubePage, bool) {
	state := youtubePage{id: page.Query().Get("v"), lang: "en"}
	if !youtubeScriptJSON(doc, youtubePlayerVar, &state.player) || state.player.VideoDetails.VideoID != state.id {
		return youtubePage{}, false
	}
	youtubeScriptJSON(doc, youtubeDataVar, &state.data)
	if root := firstElement(doc, "html"); root != nil && nodeAttr(root, "lang") != "" {
		state.lang, _, _ = strings.Cut(strings.ToLower(nodeAttr(root, "lang")), "-")
	}
	for _, node := range keptAnswers(doc, youtubeAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped = true
			continue
		}
		var err error
		switch nodeAttr(node, "kind") {
		case "player":
			if err = json.Unmarshal([]byte(rawText(node)), &state.api); err == nil {
				state.apiRead = true
			}
		case "transcript":
			if err = json.Unmarshal([]byte(rawText(node)), &state.transcript); err == nil {
				state.textRead = true
			}
		}
		if err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept YouTube answer no longer decodes; left out",
				"kind", nodeAttr(node, "kind"), obs.FieldErr, err.Error())
		}
	}
	return state, true
}

// youtubeChooseTrack takes the manual track in lang first, else the automatic
// (ASR) track, else the first track; how names which, for the reader.
func youtubeChooseTrack(tracks []youtubeTrack, lang string) (track youtubeTrack, how string, ok bool) {
	for _, candidate := range tracks {
		primary, _, _ := strings.Cut(strings.ToLower(candidate.LanguageCode), "-")
		if candidate.Kind != youtubeKindASR && primary == lang {
			return candidate, "manual, in the page language", true
		}
	}
	for _, candidate := range tracks {
		if candidate.Kind == youtubeKindASR {
			return candidate, "automatic (ASR): no manual track in the page language", true
		}
	}
	if len(tracks) > 0 {
		return tracks[0], "manual, not in the page language (" + lang + ")", true
	}
	return youtubeTrack{}, "", false
}

// youtubeJSON3 rewrites a timedtext address to ask for json3, whatever format
// it named; the other parameters keep their order and spelling (the address
// is signed).
func youtubeJSON3(target string) string {
	base, query, _ := strings.Cut(target, "?")
	var kept []string
	for _, part := range strings.Split(query, "&") {
		if part != "" && !strings.HasPrefix(part, "fmt=") {
			kept = append(kept, part)
		}
	}
	return base + "?" + strings.Join(append(kept, "fmt=json3"), "&")
}

// youtubeLoaders names the player answer, then the transcript, while the page
// lacks them.
func youtubeLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := youtubePageOf(doc, page)
	if !ok || state.dropped || state.textRead || len(state.player.tracks()) == 0 {
		return nil
	}
	origin := url.URL{Scheme: page.Scheme, Host: page.Host}
	if !state.apiRead {
		target := origin.String() + "/youtubei/v1/player?prettyPrint=false"
		body := fmt.Sprintf(`{"context":{"client":{"clientName":"ANDROID","clientVersion":"20.10.38",`+
			`"androidSdkVersion":34,"hl":%q}},"videoId":%q}`, state.lang, state.id)
		key := "youtube-player " + state.id
		return []pageLoader{{
			key:     key,
			label:   "the video's caption tracks",
			method:  http.MethodPost,
			target:  target,
			body:    []byte(body),
			headers: map[string]string{headerContentType: mediaTypeJSON, headerAccept: mediaTypeJSON},
			graft: func(answer []byte, contentType string) error {
				var player youtubePlayer
				if err := json.Unmarshal(answer, &player); err != nil || player.VideoDetails.VideoID != state.id {
					return fmt.Errorf("answered by a %s body that is not the player of video %s",
						discourseContentType(contentType), state.id)
				}
				keepAnswer(doc, youtubeAnswerTag, key, "player", 0, answer)
				return nil
			},
			drop: func() { keepAnswer(doc, youtubeAnswerTag, key, "player", 0, nil) },
		}}
	}
	track, _, ok := youtubeChooseTrack(state.api.tracks(), state.lang)
	if !ok {
		return nil
	}
	target := youtubeJSON3(track.BaseURL)
	if parsed, err := url.Parse(target); err != nil || parsed.Query().Get("v") != state.id {
		obs.Logger(context.Background()).Warn("harvest: a YouTube caption track names another video; not read",
			"video", state.id)
		return nil
	}
	key := "youtube-transcript " + state.id
	return []pageLoader{{
		key:     key,
		label:   "the video's transcript",
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerAccept: mediaTypeJSON},
		graft: func(answer []byte, contentType string) error {
			var transcript youtubeTranscript
			if err := json.Unmarshal(answer, &transcript); err != nil || transcript.Events == nil {
				return fmt.Errorf("answered by a %s body that is not a json3 caption track",
					discourseContentType(contentType))
			}
			keepAnswer(doc, youtubeAnswerTag, key, "transcript", 0, answer)
			return nil
		},
		drop: func() { keepAnswer(doc, youtubeAnswerTag, key, "transcript", 0, nil) },
	}}
}

// youtubeClock renders milliseconds as m:ss, or h:mm:ss from an hour on.
func youtubeClock(ms int64) string {
	seconds := ms / 1000
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

// youtubeParagraphs renders a json3 track as timestamped paragraphs, and
// counts its caption events (an event with text; a line-break append is not
// one) and those it rendered.
func youtubeParagraphs(transcript youtubeTranscript) (paragraphs []string, stated, loaded int) {
	var current strings.Builder
	start := int64(-1)
	flush := func() {
		if text := strings.Join(strings.Fields(current.String()), " "); text != "" {
			paragraphs = append(paragraphs, "["+youtubeClock(start)+"] "+text)
		}
		current.Reset()
	}
	if transcript.Events == nil {
		return nil, 0, 0
	}
	for _, event := range *transcript.Events {
		if len(event.Segs) == 0 || event.Append != 0 {
			continue
		}
		stated++
		var text strings.Builder
		for _, seg := range event.Segs {
			text.WriteString(seg.Text)
		}
		if strings.TrimSpace(text.String()) == "" {
			continue
		}
		loaded++
		if start < 0 || event.StartMS-start >= youtubeParagraph {
			flush()
			start = event.StartMS
		}
		current.WriteString(" " + text.String())
	}
	flush()
	return paragraphs, stated, loaded
}

// youtubeCommentsStated is the comment count the page states ("5K"), "" when
// it states none.
func youtubeCommentsStated(data *youtubeData) string {
	for _, panel := range data.EngagementPanels {
		if panel.Panel.Identifier == "engagement-panel-comments-section" {
			return strings.TrimSpace(panel.Panel.Header.Title.Info.String())
		}
	}
	return ""
}

// extractYouTubeVideo renders a watch page's video from its scripts and its
// transcript from the caption track kept in its page.
func extractYouTubeVideo(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := youtubePageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	details := state.player.VideoDetails
	link := "https://www." + youtubeHost + "/watch?v=" + url.QueryEscape(state.id)
	lines := []string{"# " + details.Title, ""}
	author := details.Author
	if author == "" {
		author = unknownAuthor
	}
	lines = append(lines, "**Channel:** "+author, "**Video:** "+link)
	if published := state.player.Microformat.Renderer.PublishDate; published != "" {
		lines = append(lines, "**Published:** "+githubPosted(published))
	}
	if seconds, err := strconv.ParseInt(details.LengthSeconds, 10, 64); err == nil {
		lines = append(lines, "**Length:** "+youtubeClock(seconds*1000))
	}
	var partial string
	description := strings.TrimSpace(strings.ReplaceAll(details.ShortDescription, "\r\n", "\n"))
	if description != "" {
		// A paragraph per blank-line block; a line break inside one is kept.
		blocks := strings.Split(description, "\n\n")
		for index, block := range blocks {
			blocks[index] = strings.ReplaceAll(strings.TrimSpace(block), "\n", "  \n")
		}
		lines = append(lines, "", "## Description", "", strings.Join(blocks, "\n\n"))
	}
	var chapters []string
	for _, marker := range state.data.PlayerOverlays.Overlay.Bar.Bar.PlayerBar.Markers.Map {
		for _, chapter := range marker.Value.Chapters {
			chapters = append(chapters, "- "+youtubeClock(chapter.Chapter.StartMS)+" "+chapter.Chapter.Title.String())
		}
	}
	if len(chapters) > 0 {
		lines = append(append(lines, "", "## Chapters", ""), chapters...)
	}
	lines = append(lines, "", "## Transcript", "")
	track, how, chosen := youtubeChooseTrack(state.api.tracks(), state.lang)
	switch {
	case len(state.player.tracks()) == 0:
		lines = append(lines, "*The video has no captions: no transcript to read.*")
		partial = joinReasons(partial, "the video has no captions, so no transcript")
	case !state.apiRead:
		lines = append(lines, "**Captions:** not loaded")
		partial = joinReasons(partial, "the transcript was not loaded: the player API naming its caption "+
			"tracks did not answer")
	case !chosen:
		lines = append(lines, "**Captions:** none named by the player API")
		partial = joinReasons(partial, "the player API named no caption track, so no transcript")
	case !state.textRead:
		lines = append(lines, "**Captions:** "+track.Name.String()+" ("+how+") · not loaded")
		partial = joinReasons(partial, "the transcript ("+track.Name.String()+") was not loaded")
	default:
		paragraphs, stated, loaded := youtubeParagraphs(state.transcript)
		count := fmt.Sprintf("%d caption events stated · %d loaded", stated, loaded)
		if blank := stated - loaded; blank > 0 {
			count += fmt.Sprintf(" · %d blank", blank)
		}
		lines = append(lines, "**Captions:** "+track.Name.String()+" ("+how+") · "+count, "",
			strings.Join(paragraphs, "\n\n"))
	}
	lines = append(lines, "", "## Comments", "")
	if stated := youtubeCommentsStated(&state.data); stated != "" {
		lines = append(lines, "**Comments:** "+stated+" stated · 0 loaded")
		partial = joinReasons(partial, "the video's "+stated+" comments were not loaded: YouTube serves them "+
			"only through its continuation API, which the harvester does not follow")
	} else {
		lines = append(lines, "**Comments:** the page states no count · 0 loaded")
		partial = joinReasons(partial, "the video's comments were not loaded (YouTube serves them only through "+
			"its continuation API) and the page states no count")
	}
	markdown := strings.Join(lines, "\n") + "\n"
	return siteExtraction{markdown: markdown, partial: partial, apiRecord: state.textRead}, true
}
