package harvestmcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	maxDownloadSources            = 50
	downloadURIPrefix             = "harvest://download/"
	downloadURITemplate           = downloadURIPrefix + "{id}"
	defaultMaxResourceBytes int64 = 25 << 20
	resourceLinkType              = "resource_link"
	downloadDescription           = `Downloads 1–50 files of any kind — a PDF, zip, image, audio, dataset — as bytes, unparsed, input order kept. Call download{sources:["https://…/data.zip"]}. Each item returns kind, content type, size, sha256 and the rung that served it, then the local path (local server) or a resource_link to read with resources/read (remote server). Nothing is converted: a page to read goes to readPage, a paper to readWork. A failing item carries its own error and the others still return.`
)

// DownloadInput is download's input.
type DownloadInput struct {
	Sources []string          `json:"sources" jsonschema:"1–50 URLs of files of any kind, each downloaded as bytes, unparsed, in the same order."`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"Optional request headers (name → value) sent only to the target's own origin; archives never receive them. At most 32 headers, 8 KiB; no hop-by-hop or framing header. A caller header overrides the default of its name."`
}

// ResourceRef is the resource_link a remote download answers: resources/read
// of URI returns the bytes as a blob.
type ResourceRef struct {
	Type     string `json:"type"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MIMEType string `json:"mimeType,omitempty"`
	Size     int64  `json:"size"`
}

// DownloadItem is one downloaded file: Path on the local server, Resource on
// the remote one. Note states a limit the file is over.
type DownloadItem struct {
	Source      string       `json:"source"`
	Kind        string       `json:"kind,omitempty"`
	ContentType string       `json:"content_type,omitempty"`
	Bytes       int64        `json:"bytes,omitempty"`
	SHA256      string       `json:"sha256,omitempty"`
	Method      string       `json:"method,omitempty"`
	Status      int          `json:"status,omitempty"`
	Path        string       `json:"path,omitempty"`
	Resource    *ResourceRef `json:"resource,omitempty"`
	Note        string       `json:"note,omitempty"`
	Error       string       `json:"error,omitempty"`
}

// DownloadOutput is download's typed output.
type DownloadOutput struct {
	Items []DownloadItem `json:"items"`
}

// downloadStore is the files this service downloaded, by sha256: the only
// ids the download resource template answers.
type downloadStore struct {
	mu    sync.Mutex
	files map[string]storedDownload
}

type storedDownload struct {
	path string
	mime string
	size int64
}

func newDownloadStore() *downloadStore {
	return &downloadStore{files: map[string]storedDownload{}}
}

func (store *downloadStore) put(id string, file storedDownload) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.files[id] = file
}

func (store *downloadStore) get(id string) (storedDownload, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, ok := store.files[id]
	return file, ok
}

func (service *Service) maxResourceBytes() int64 {
	if service.runtime.MaxResourceBytes > 0 {
		return service.runtime.MaxResourceBytes
	}
	return defaultMaxResourceBytes
}

func (service *Service) download(
	ctx context.Context, _ *mcp.CallToolRequest, input DownloadInput,
) (*mcp.CallToolResult, DownloadOutput, error) {
	if len(input.Sources) < 1 || len(input.Sources) > maxDownloadSources {
		return nil, DownloadOutput{}, fmt.Errorf("sources must contain 1-%d items", maxDownloadSources)
	}
	headers, err := harvest.ParseCallerHeaders(input.Headers)
	if err != nil {
		return nil, DownloadOutput{}, err
	}
	items := make([]DownloadItem, len(input.Sources))
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for index, source := range input.Sources {
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					items[index] = DownloadItem{
						Source: source, Error: obs.Recovered("harvester download item", recovered).Error(),
					}
				}
			}()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				items[index] = DownloadItem{Source: source, Error: "download cancelled: " + ctx.Err().Error()}
				return
			}
			defer func() { <-semaphore }()
			items[index] = service.downloadOne(ctx, source, headers)
		}()
	}
	wait.Wait()
	result := &mcp.CallToolResult{}
	for index := range items {
		item := &items[index]
		result.Content = append(result.Content, &mcp.TextContent{Text: renderDownload(*item)})
		if item.Resource != nil {
			size := item.Resource.Size
			result.Content = append(result.Content, &mcp.ResourceLink{
				URI: item.Resource.URI, Name: item.Resource.Name, MIMEType: item.Resource.MIMEType, Size: &size,
			})
		}
	}
	return result, DownloadOutput{Items: items}, nil
}

// downloadOne downloads one source through harvest.Download (Retrieve's
// file policy), hashes the stored file and records it in the store.
func (service *Service) downloadOne(ctx context.Context, source string, headers harvest.CallerHeaders) DownloadItem {
	if message := downloadMisroute(source); message != "" {
		return DownloadItem{Source: source, Error: message}
	}
	harvester, scopedCtx, err := service.harvester.ForCaller(ctx, headers, source)
	if err != nil {
		return DownloadItem{Source: source, Error: err.Error()} // no request was sent
	}
	return service.downloadItem(ctx, source, headers.MarkHeaderless(harvester.Download(scopedCtx, source)))
}

// downloadItem turns one download result into its item: hashed, recorded in
// the store, and a path (local) or a resource_link (remote).
func (service *Service) downloadItem(ctx context.Context, source string, result harvest.Result) DownloadItem {
	item := DownloadItem{
		Source: source, Kind: result.Kind, Method: harvest.PublicMethod(result.Method), Status: result.HTTPStatus,
	}
	if result.Error != "" {
		item.Error = result.Error
		if service.runtime.Remote {
			item.Error = harvest.PublicFailureMessage(result)
		}
		return item
	}
	digest, mime, size, err := hashFile(result.Path)
	if err != nil {
		obs.Logger(obs.Component(ctx, "mcp")).Warn("harvester.download.hash", obs.FieldErr, err.Error())
		item.Error = "the file was downloaded but could not be read back from the cache; retry the download"
		return item
	}
	item.SHA256, item.ContentType, item.Bytes = digest, mime, size
	service.downloads.put(digest, storedDownload{path: result.Path, mime: mime, size: size})
	if !service.runtime.Remote {
		item.Path = result.Path
		return item
	}
	item.Resource = &ResourceRef{
		Type: resourceLinkType, URI: downloadURIPrefix + digest, Name: downloadName(source, digest),
		MIMEType: mime, Size: size,
	}
	if limit := service.maxResourceBytes(); size > limit {
		item.Note = fmt.Sprintf(
			"the file is %d bytes, over harvest.maxResourceBytes (%d): resources/read will refuse it, since the MCP transport carries a blob base64 in one message",
			size,
			limit,
		)
	}
	return item
}

func downloadMisroute(source string) string {
	switch {
	case isLocalInput(source):
		return "this is a local path; download takes URLs — read a local document with `parseLocalDocuments`."
	case workNoun(source) != "":
		return "this is " + workNoun(source) + "; read it with `readWork`, or download the URL of its file."
	case !isWebURL(source):
		return "this is not a URL; download takes http(s) URLs of files."
	}
	return ""
}

// hashFile streams a stored file into sha256 and names its MIME type from its
// first 512 bytes.
func hashFile(name string) (digest, mime string, size int64, returnErr error) {
	file, err := os.Open(name)
	if err != nil {
		return "", "", 0, fmt.Errorf("open downloaded file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close downloaded file: %w", closeErr))
		}
	}()
	head := make([]byte, 512)
	read, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", 0, fmt.Errorf("read downloaded file: %w", err)
	}
	hash := sha256.New()
	hash.Write(head[:read])
	rest, err := io.Copy(hash, file)
	if err != nil {
		return "", "", 0, fmt.Errorf("hash downloaded file: %w", err)
	}
	mime, _, _ = strings.Cut(http.DetectContentType(head[:read]), ";")
	return hex.EncodeToString(hash.Sum(nil)), mime, int64(read) + rest, nil
}

func downloadName(source, digest string) string {
	if parsed, err := url.Parse(source); err == nil {
		if base := path.Base(parsed.Path); base != "" && base != "/" && base != "." {
			return base
		}
	}
	return digest
}

func renderDownload(item DownloadItem) string {
	if item.Error != "" {
		return "# " + harvest.PublicSourceLabel(item.Source) + "\nERROR: " + item.Error
	}
	where := "path: " + item.Path
	if item.Resource != nil {
		where = "resource: " + item.Resource.URI + " (resources/read returns the bytes)"
	}
	text := fmt.Sprintf(
		"# %s\nkind: %s / content_type: %s / bytes: %d / sha256: %s / method: %s / %s",
		harvest.PublicSourceLabel(
			item.Source,
		),
		item.Kind,
		item.ContentType,
		item.Bytes,
		item.SHA256,
		item.Method,
		where,
	)
	if item.Note != "" {
		text += "\nNOTE: " + item.Note
	}
	return text
}

// readDownload answers resources/read of harvest://download/{id}: only ids
// the download store holds, never any other file; a blob over
// harvest.maxResourceBytes is a named error with its size and the reason.
func (service *Service) readDownload(
	ctx context.Context, request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	uri := request.Params.URI
	id, ok := strings.CutPrefix(uri, downloadURIPrefix)
	file, held := service.downloads.get(id)
	if !ok || !held {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if limit := service.maxResourceBytes(); file.size > limit {
		return nil, fmt.Errorf(
			"%s is %d bytes, over harvest.maxResourceBytes (%d): not sent, since the MCP transport carries a blob base64 in one message",
			uri,
			file.size,
			limit,
		)
	}
	body, err := os.ReadFile(file.path)
	if err != nil {
		obs.Logger(obs.Component(ctx, "mcp")).Warn("harvester.download.read", obs.FieldErr, err.Error())
		return nil, fmt.Errorf("%s: the stored file could not be read; download it again", uri)
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != id {
		return nil, fmt.Errorf("%s: the stored file changed since it was downloaded; download it again", uri)
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: file.mime, Blob: body}},
	}, nil
}
