package harvestcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// downloadItem is one downloaded file's receipt; --json prints these.
type downloadItem struct {
	Source      string   `json:"source"`
	Path        string   `json:"path,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	Bytes       int64    `json:"bytes,omitempty"`
	Via         string   `json:"via,omitempty"`
	Gaps        []string `json:"gaps,omitempty"`
	Error       string   `json:"error,omitempty"`
	ErrorKind   string   `json:"error_kind,omitempty"` // the failure's class, beside every error
}

// runDownload is `pfm harvest download-file <url>...`: each URL's bytes, unparsed,
// through the harvester's one file path (Harvester.Download, the MCP download_file
// tool's entry). The --header set is validated as the tool validates it,
// before any request; a failed item is a named ERROR receipt and exit 1.
func runDownload(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet(
		"harvest download-file",
		"usage: pfm harvest download-file [--json] [--header 'Name: value']... <url>...",
		stderr,
	)
	jsonOutput := flags.Bool(jsonFlag, false, "print machine-readable download objects")
	lines := harvest.HeaderFlag(flags)
	sources, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(sources) == 0 || len(sources) > maxSources {
		flags.Usage()
		return 2
	}
	headers, err := lines.Parse()
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest download-file: %v\n", err)
		return 2
	}
	harvester, err := newHarvester(harvesterRuntime(runtime))
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest download-file: configure: %v\n", err)
		return 1
	}
	items := make([]downloadItem, 0, len(sources))
	for _, source := range sources {
		item := downloadOne(context.Background(), harvester, headers, source)
		items = append(items, item)
		if !*jsonOutput {
			fmt.Fprintln(stdout, renderDownloadReceipt(item))
		}
	}
	if *jsonOutput {
		encoded, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "pfm harvest download-file: encode results: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(encoded))
	}
	for index := range items {
		if items[index].Error != "" {
			return 1
		}
	}
	return 0
}

// downloadOne scopes the headers to source's origin and downloads it, then
// reads the stored file back for its content type and size.
func downloadOne(
	ctx context.Context,
	harvester *harvest.Harvester,
	headers harvest.CallerHeaders,
	source string,
) downloadItem {
	scoped, scopedCtx, err := harvester.ForCaller(ctx, headers, source)
	if err != nil {
		// No request was sent: the headers have no origin to go to.
		kind := harvest.PublicFailure(source, harvest.Result{Error: err.Error()}).ErrorKind
		return downloadItem{Source: source, Error: err.Error(), ErrorKind: kind}
	}
	result := headers.MarkHeaderless(scoped.Download(scopedCtx, source))
	item := downloadItem{
		Source: source, Kind: result.Kind,
		Via: harvest.PublicMethod(result.Method), Gaps: harvest.PublicGaps(result.Partial), // an empty list is omitted
	}
	if result.Error != "" {
		item.Error, item.ErrorKind = result.Error, harvest.PublicFailure(source, result).ErrorKind
		return item
	}
	contentType, size, err := sniffFile(result.Path)
	if err != nil {
		item.Error = fmt.Sprintf("the file was downloaded to %s but could not be read back: %v", result.Path, err)
		item.ErrorKind = "internal" // the stored file, not the source, failed
		return item
	}
	item.Path, item.ContentType, item.Bytes = result.Path, contentType, size
	return item
}

// sniffFile names a stored file's MIME type from its first 512 bytes and
// returns its size.
func sniffFile(name string) (contentType string, size int64, returnErr error) {
	file, err := os.Open(name)
	if err != nil {
		return "", 0, fmt.Errorf("open downloaded file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close downloaded file: %w", closeErr))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat downloaded file: %w", err)
	}
	head := make([]byte, 512)
	read, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", 0, fmt.Errorf("read downloaded file: %w", err)
	}
	contentType, _, _ = strings.Cut(http.DetectContentType(head[:read]), ";")
	return contentType, info.Size(), nil
}

func renderDownloadReceipt(item downloadItem) string {
	if item.Error != "" {
		return fmt.Sprintf("# %s\nERROR: %s", item.Source, item.Error)
	}
	return fmt.Sprintf(
		"# %s\npath: %s / kind: %s / content_type: %s / bytes: %d%s",
		item.Source,
		item.Path,
		item.Kind,
		item.ContentType,
		item.Bytes,
		gapsSuffix(item.Gaps),
	)
}
