package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The X post extractor. An x.com (or twitter.com) post page served to a
// signed-out reader is the post's preview under a sign-in wall, and the
// readers are refused or keep that preview. The extractor reads the post
// instead from the FxTwitter API v2 (api.fxtwitter.com, the FxEmbed
// project's public JSON API, its shapes documented by FxEmbed's Zod/OpenAPI
// schemas: SocialThread, APITwitterStatus), unauthenticated, through
// loaders.go's budget: the post's thread (/2/thread/{id}), which holds the
// post and the author's own thread around it, each post's whole text (a long
// "note" post included, which X's own embed answer cuts at 280 characters),
// its media, quoted post, poll, link card, article and community note. The
// answer is checked to be this post's before it is kept in the page as a
// harvester-x-answer element, so a later conversion replays it like any
// followed loader. A post whose answer never loaded, or that the API answers
// unavailable (deleted, suspended, private), is not claimed: the page goes
// the generic path, the gap and its cause named in its partial marker. The
// replies are not read; the count the post states of them is shown as such.

const (
	xHost       = "x.com"
	twitterHost = "twitter.com"
	xAPIHost    = "api.fxtwitter.com"
	xAPI        = "https://" + xAPIHost + "/2/thread/"
	xAnswerTag  = "harvester-x-answer"
	xKindThread = "thread"
	// xStatusType and xTombstoneType are the API's discriminators of a post
	// and of a placeholder for one that is unavailable.
	xStatusType    = "status"
	xTombstoneType = "tombstone"
	// xIDMaxLen bounds a post id's digits (a 64-bit snowflake).
	xIDMaxLen = 20
)

type xAuthor struct {
	Name       string `json:"name"`
	ScreenName string `json:"screen_name"`
}

// xMedia is one media item (APIPhoto, APIVideo, APIMosaicPhoto, an external
// video, a broadcast): what a reader needs of each.
type xMedia struct {
	Type     string  `json:"type"`
	URL      string  `json:"url"`
	AltText  string  `json:"altText"`
	Duration float64 `json:"duration"`
	Title    string  `json:"title"`
	State    string  `json:"state"`
}

type xMediaSet struct {
	All       []xMedia `json:"all"`
	Photos    []xMedia `json:"photos"`
	Videos    []xMedia `json:"videos"`
	External  *xMedia  `json:"external"`
	Broadcast *xMedia  `json:"broadcast"`
}

type xPoll struct {
	Choices []struct {
		Label      string  `json:"label"`
		Count      int     `json:"count"`
		Percentage float64 `json:"percentage"`
	} `json:"choices"`
	TotalVotes int    `json:"total_votes"`
	TimeLeft   string `json:"time_left_en"`
}

type xCard struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// xArticleMedia is one of an article's media entities (Twitter's ApiImage,
// ApiVideo or ApiGif).
type xArticleMedia struct {
	MediaID   string `json:"media_id"`
	MediaInfo struct {
		Typename       string `json:"__typename"`
		OriginalImgURL string `json:"original_img_url"`
		MediaURLHTTPS  string `json:"media_url_https"`
		VideoInfo      struct {
			Variants []struct {
				ContentType string `json:"content_type"`
				URL         string `json:"url"`
			} `json:"variants"`
		} `json:"video_info"`
	} `json:"media_info"`
}

// xArticle is an X article (TwitterArticleSchema): Draft.js-style blocks and
// the entities its atomic blocks hold.
type xArticle struct {
	Title   string `json:"title"`
	Content struct {
		Blocks []struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			EntityRanges []struct {
				Key int `json:"key"`
			} `json:"entityRanges"`
		} `json:"blocks"`
		EntityMap []struct {
			Key   string `json:"key"`
			Value struct {
				Type string `json:"type"`
				Data struct {
					Markdown   string `json:"markdown"`
					TweetID    string `json:"tweetId"`
					MediaItems []struct {
						MediaID string `json:"mediaId"`
					} `json:"mediaItems"`
				} `json:"data"`
			} `json:"value"`
		} `json:"entityMap"`
	} `json:"content"`
	CoverMedia    *xArticleMedia  `json:"cover_media"`
	MediaEntities []xArticleMedia `json:"media_entities"`
}

// xStatus is one post (APITwitterStatus) or, with Type xTombstoneType, the
// API's placeholder for one that is unavailable (APIStatusTombstone).
type xStatus struct {
	Type             string     `json:"type"`
	ID               string     `json:"id"`
	URL              string     `json:"url"`
	Text             string     `json:"text"`
	CreatedAt        string     `json:"created_at"`
	CreatedTimestamp int64      `json:"created_timestamp"`
	Likes            int        `json:"likes"`
	Reposts          int        `json:"reposts"`
	Quotes           int        `json:"quotes"`
	Replies          int        `json:"replies"`
	Views            *int       `json:"views"`
	Author           *xAuthor   `json:"author"`
	Media            *xMediaSet `json:"media"`
	Quote            *xStatus   `json:"quote"`
	Poll             *xPoll     `json:"poll"`
	Card             *xCard     `json:"card"`
	Article          *xArticle  `json:"article"`
	// CommunityNote is either documented form ({text, facets} or the legacy
	// {text, entities}); its text is what both share.
	CommunityNote *struct {
		Text string `json:"text"`
	} `json:"community_note"`
	ReplyingTo *struct {
		ScreenName string `json:"screen_name"`
		Status     string `json:"status"`
		URL        string `json:"url"`
	} `json:"replying_to"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// xThreadAnswer is the API's /2/thread answer (SocialThread): the post and
// the author's thread it sits in, the post included.
type xThreadAnswer struct {
	Code   int       `json:"code"`
	Status *xStatus  `json:"status"`
	Thread []xStatus `json:"thread"`
}

// xStatusID is the post id a page's address names — /{user}/status/{id}
// (or the legacy /statuses/), optionally its /photo/{n} or /video/{n} view,
// or /i/web/status/{id} — on x.com or twitter.com.
func xStatusID(page *url.URL) (string, bool) {
	host := strings.ToLower(page.Hostname())
	if host != xHost && !strings.HasSuffix(host, "."+xHost) && host != twitterHost &&
		!strings.HasSuffix(host, "."+twitterHost) {
		return "", false
	}
	parts := strings.Split(strings.Trim(page.Path, "/"), "/")
	var id string
	var rest []string
	switch {
	case len(parts) >= 4 && parts[0] == "i" && parts[1] == "web" && parts[2] == "status":
		id, rest = parts[3], parts[4:]
	case len(parts) >= 3 && parts[0] != "" && (parts[1] == "status" || parts[1] == "statuses"):
		id, rest = parts[2], parts[3:]
	default:
		return "", false
	}
	if len(rest) != 0 && (len(rest) != 2 || (rest[0] != "photo" && rest[0] != "video") || !isDigits(rest[1])) {
		return "", false
	}
	if !isDigits(id) || len(id) > xIDMaxLen {
		return "", false
	}
	return id, true
}

func isXStatus(page *url.URL) bool {
	_, ok := xStatusID(page)
	return ok
}

// xPage is what a post page holds of its API answer.
type xPage struct {
	id      string
	answer  *xThreadAnswer
	dropped map[string]bool
}

func xPageOf(doc *html.Node, page *url.URL) (xPage, bool) {
	id, ok := xStatusID(page)
	if !ok {
		return xPage{}, false
	}
	state := xPage{id: id, dropped: map[string]bool{}}
	for _, node := range keptAnswers(doc, xAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped[nodeAttr(node, "key")] = true
			continue
		}
		var answer xThreadAnswer
		if err := json.Unmarshal([]byte(rawText(node)), &answer); err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept X answer no longer decodes; left out",
				"kind", nodeAttr(node, "kind"), obs.FieldErr, err.Error())
			continue
		}
		state.answer = &answer
	}
	return state, true
}

// xLoaders names the API answer the post page still lacks: the post's thread.
func xLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := xPageOf(doc, page)
	if !ok || state.answer != nil {
		return nil
	}
	target := xAPI + state.id
	key := "x-api " + target
	if state.dropped[key] {
		return nil
	}
	return []pageLoader{{
		key:     key,
		label:   "the post's thread from the FxTwitter API",
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerAccept: mediaTypeJSON},
		graft: func(body []byte, contentType string) error {
			if err := state.check(body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, xAnswerTag, key, xKindThread, 0, body)
			return nil
		},
		drop: func() { keepAnswer(doc, xAnswerTag, key, xKindThread, 0, nil) },
	}}
}

// check proves an API answer is this post's thread.
func (state xPage) check(body []byte, contentType string) error {
	var answer xThreadAnswer
	if err := json.Unmarshal(body, &answer); err != nil || (answer.Status == nil && answer.Code == 0) {
		return fmt.Errorf("answered by a %s body that is not the FxTwitter API's JSON for the post",
			discourseContentType(contentType))
	}
	switch post := answer.Status; {
	case post == nil:
		return fmt.Errorf("answered with no post (code %d)", answer.Code)
	case post.Type == xTombstoneType:
		return fmt.Errorf("answered that the post is %s: %s", post.Reason, post.Message)
	case post.Type != xStatusType:
		return fmt.Errorf("answered by a %q record, not a post", post.Type)
	case post.ID != state.id:
		return fmt.Errorf("answered by another post (%s)", post.ID)
	}
	return nil
}

// extractXPost renders an X post, the author's thread around it, from the API
// answer kept in its page.
func extractXPost(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := xPageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	if state.answer == nil || state.answer.Status == nil {
		return siteExtraction{unrendered: "x post: the post was not loaded from the FxTwitter API " +
			"(the page is stored as X serves it signed out)"}, false
	}
	post := state.answer.Status
	var gaps []string
	var out strings.Builder
	author := xHandle(post.Author)
	title := author + " on X"
	if post.Author != nil && post.Author.Name != "" {
		title = post.Author.Name + " (" + author + ") on X"
	}
	out.WriteString("# " + title + "\n\n")
	out.WriteString("**Author:** " + author + " · **Posted:** " + xPosted(post) + "  \n")
	out.WriteString("**Post:** " + xLink(post) + "  \n")
	if parent := post.ReplyingTo; parent != nil {
		out.WriteString("**In reply to:** @" + parent.ScreenName + " · " + parent.URL + "  \n")
	}
	counts := fmt.Sprintf("**Replies:** %d (not read) · **Reposts:** %d · **Quotes:** %d · **Likes:** %d",
		post.Replies, post.Reposts, post.Quotes, post.Likes)
	if post.Views != nil {
		counts += fmt.Sprintf(" · **Views:** %d", *post.Views)
	}
	out.WriteString(counts + "\n\n")
	at := -1
	for index := range state.answer.Thread {
		if state.answer.Thread[index].ID == post.ID {
			at = index
		}
	}
	if at > 0 {
		out.WriteString("## Earlier in the author's thread\n\n")
		xWriteThread(&out, state.answer.Thread[:at], &gaps)
		out.WriteString("\n---\n\n")
	}
	if body := xBody(post, 0, &gaps); body != "" {
		out.WriteString(body + "\n\n")
	}
	switch {
	case at >= 0 && at+1 < len(state.answer.Thread):
		out.WriteString("---\n\n## The author's thread continues\n\n")
		xWriteThread(&out, state.answer.Thread[at+1:], &gaps)
	case at < 0 && len(state.answer.Thread) > 0:
		// A thread that does not hold the post cannot be split around it: it
		// is shown whole, never dropped.
		out.WriteString("---\n\n## The author's thread\n\n")
		xWriteThread(&out, state.answer.Thread, &gaps)
	}
	partial := ""
	if len(gaps) > 0 {
		partial = "x post: " + strings.Join(gaps, "; ")
	}
	return siteExtraction{markdown: strings.TrimRight(out.String(), "\n") + "\n", partial: partial, apiRecord: true},
		true
}

// xWriteThread writes the author's thread posts as list entries.
func xWriteThread(out *strings.Builder, posts []xStatus, gaps *[]string) {
	for index := range posts {
		post := &posts[index]
		if post.Type == xTombstoneType {
			fmt.Fprintf(out, "- *a post of the thread is unavailable (%s): %s*\n", post.Reason, post.Message)
			continue
		}
		writeSocialPost(out, &socialPost{
			id:     post.ID,
			author: xHandle(post.Author),
			posted: xPosted(post),
			link:   xLink(post),
			body:   xBody(post, 1, gaps),
		}, "")
	}
}

func xHandle(author *xAuthor) string {
	if author == nil || author.ScreenName == "" {
		return unknownAuthor
	}
	return "@" + author.ScreenName
}

func xLink(post *xStatus) string {
	if post.URL != "" {
		return post.URL
	}
	handle := "i"
	if post.Author != nil && post.Author.ScreenName != "" {
		handle = post.Author.ScreenName
	}
	return "https://" + xHost + "/" + handle + "/status/" + post.ID
}

// xPosted is a post's time: its Unix timestamp, else its created_at (Ruby
// date form) as the API wrote it.
func xPosted(post *xStatus) string {
	if post.CreatedTimestamp > 0 {
		return time.Unix(post.CreatedTimestamp, 0).UTC().Format("2006-01-02 15:04 UTC")
	}
	if parsed, err := time.Parse(time.RubyDate, post.CreatedAt); err == nil {
		return parsed.UTC().Format("2006-01-02 15:04 UTC")
	}
	if post.CreatedAt != "" {
		return post.CreatedAt
	}
	return unknownPosted
}

// xBody renders a post's content: its text, media, link card, poll, quoted
// post (at depth 0 only: a quoted post's own quote is a link away), article
// and community note, as Markdown blocks.
func xBody(post *xStatus, depth int, gaps *[]string) string {
	var blocks []string
	if text := strings.TrimSpace(post.Text); text != "" {
		blocks = append(blocks, hardBreaks(text))
	}
	blocks = append(blocks, xMediaBlocks(post.Media)...)
	if card := post.Card; card != nil && card.URL != "" {
		line := card.URL
		if card.Title != "" {
			line = "[" + card.Title + "](" + card.URL + ")"
		}
		if card.Description != "" {
			line += " — " + card.Description
		}
		blocks = append(blocks, line)
	}
	if poll := post.Poll; poll != nil {
		lines := []string{fmt.Sprintf("**Poll** · %d votes · %s", poll.TotalVotes, poll.TimeLeft)}
		for _, choice := range poll.Choices {
			lines = append(lines, fmt.Sprintf("- %s: %d (%s%%)", choice.Label, choice.Count,
				strconv.FormatFloat(choice.Percentage, 'f', -1, 64)))
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	if quote := post.Quote; quote != nil && depth == 0 {
		if quote.Type == xTombstoneType {
			blocks = append(blocks, fmt.Sprintf("> *The quoted post is unavailable (%s): %s*", quote.Reason,
				quote.Message))
		} else {
			header := "**Quoting " + xHandle(quote.Author) + "** · " + xPosted(quote) + " · [" + quote.ID + "](" +
				xLink(quote) + ")"
			blocks = append(
				blocks,
				prefixLines(strings.TrimSpace(header+"\n\n"+xBody(quote, depth+1, gaps)), "> ", ">"),
			)
		}
	}
	if post.Article != nil {
		blocks = append(blocks, xArticleBlocks(post.Article, gaps)...)
	}
	if note := post.CommunityNote; note != nil && strings.TrimSpace(note.Text) != "" {
		blocks = append(blocks, prefixLines("**Community note:** "+hardBreaks(note.Text), "> ", ">"))
	}
	return strings.Join(blocks, "\n\n")
}

// xMediaBlocks renders a post's media in the API's order (all; else the
// photos, then the videos): a photo as an image, a video or GIF as a link to
// its file. A mosaic is a composite of the photos already listed; a media
// kind the schema does not name renders as a link under its own name.
func xMediaBlocks(media *xMediaSet) []string {
	if media == nil {
		return nil
	}
	items := media.All
	if len(items) == 0 {
		items = append(append([]xMedia(nil), media.Photos...), media.Videos...)
	}
	var blocks []string
	for _, item := range items {
		switch {
		case item.URL == "" || item.Type == "mosaic_photo":
		case item.Type == "photo":
			blocks = append(blocks, "!["+item.AltText+"]("+item.URL+")")
		case item.Type == "gif":
			blocks = append(blocks, "[GIF]("+item.URL+")")
		case item.Type == "video":
			label := "Video"
			if seconds := int(item.Duration); seconds > 0 {
				label += fmt.Sprintf(" (%d:%02d)", seconds/60, seconds%60)
			}
			blocks = append(blocks, "["+label+"]("+item.URL+")")
		default:
			blocks = append(blocks, "["+item.Type+"]("+item.URL+")")
		}
	}
	if external := media.External; external != nil && external.URL != "" {
		blocks = append(blocks, "[External video]("+external.URL+")")
	}
	if broadcast := media.Broadcast; broadcast != nil && broadcast.URL != "" {
		blocks = append(blocks, "[Broadcast: "+broadcast.Title+" ("+broadcast.State+")]("+broadcast.URL+")")
	}
	return blocks
}

// xArticleBlocks renders an article: its title, cover and blocks. An atomic
// block renders its entities (an image or video, a Markdown code block, an
// embedded post); an entity of a kind this reader does not know, or one the
// answer does not hold, is a gap.
func xArticleBlocks(article *xArticle, gaps *[]string) []string {
	blocks := []string{"## " + article.Title}
	if article.CoverMedia != nil {
		if media := xArticleMediaBlock(article.CoverMedia); media != "" {
			blocks = append(blocks, media)
		}
	}
	entities := map[string]int{}
	for index, entity := range article.Content.EntityMap {
		entities[entity.Key] = index
	}
	media := map[string]*xArticleMedia{}
	for index := range article.MediaEntities {
		media[article.MediaEntities[index].MediaID] = &article.MediaEntities[index]
	}
	for _, block := range article.Content.Blocks {
		text := strings.TrimSpace(block.Text)
		switch block.Type {
		case "header-one":
			blocks = append(blocks, "### "+text)
		case "header-two":
			blocks = append(blocks, "#### "+text)
		case "unordered-list-item":
			blocks = append(blocks, "- "+text)
		case "ordered-list-item":
			blocks = append(blocks, "1. "+text)
		case "blockquote":
			blocks = append(blocks, prefixLines(hardBreaks(text), "> ", ">"))
		case "code-block":
			blocks = append(blocks, "```\n"+strings.Trim(block.Text, "\n")+"\n```")
		case "atomic":
			for _, span := range block.EntityRanges {
				index, ok := entities[strconv.Itoa(span.Key)]
				if !ok {
					*gaps = append(*gaps, fmt.Sprintf("the article's block entity %d was not in the answer", span.Key))
					continue
				}
				entity := article.Content.EntityMap[index].Value
				switch entity.Type {
				case "MEDIA":
					for _, item := range entity.Data.MediaItems {
						found := media[item.MediaID]
						if found == nil {
							*gaps = append(*gaps, "the article's media "+item.MediaID+" was not in the answer")
							continue
						}
						if rendered := xArticleMediaBlock(found); rendered != "" {
							blocks = append(blocks, rendered)
						}
					}
				case "MARKDOWN":
					blocks = append(blocks, strings.TrimSpace(entity.Data.Markdown))
				case "TWEET":
					blocks = append(blocks, "[post "+entity.Data.TweetID+"](https://"+xHost+"/i/status/"+
						entity.Data.TweetID+")")
				default:
					*gaps = append(*gaps, "the article's "+entity.Type+
						" block was not rendered (a kind this reader does not know)")
				}
			}
		default:
			if text != "" {
				blocks = append(blocks, hardBreaks(text))
			}
		}
	}
	return blocks
}

// xArticleMediaBlock renders one article media entity: an image, or a link to
// a video's MP4 file (its poster image when it names none).
func xArticleMediaBlock(media *xArticleMedia) string {
	info := media.MediaInfo
	if info.OriginalImgURL != "" {
		return "![](" + info.OriginalImgURL + ")"
	}
	for _, variant := range info.VideoInfo.Variants {
		if variant.ContentType == "video/mp4" && variant.URL != "" {
			return "[Video](" + variant.URL + ")"
		}
	}
	if info.MediaURLHTTPS != "" {
		return "![](" + info.MediaURLHTTPS + ")"
	}
	return ""
}
